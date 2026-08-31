/*
Copyright 2026 The llm-d Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package modelselector

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"sigs.k8s.io/controller-runtime/pkg/log"

	errcommon "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/error"
	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/tracing"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/modelselector"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/metrics"
)

// compile-time interface validation
var _ modelselector.ModelSelectorPipeline = &ModelSelectorPipeline{}

const (
	filterExtensionPoint = "ModelSelectorFilter"
	scorerExtensionPoint = "ModelSelectorScorer"
	pickerExtensionPoint = "ModelSelectorPicker"

	// modelSelectorTracerScope is the OTel instrumentation scope for spans
	// emitted by the model-selector pipeline, following the package-path
	// naming convention.
	modelSelectorTracerScope = "llm-d-ipp/pkg/modelselector"
)

// NewModelSelectorPipeline creates a new ModelSelectorPipeline object and returns its pointer.
func NewModelSelectorPipeline() *ModelSelectorPipeline {
	return &ModelSelectorPipeline{
		filters: []modelselector.Filter{},
		scorers: []*WeightedScorer{},
	}
}

// ModelSelectorPipeline provides a pipeline configuration for the model-selector which influence model decisions.
type ModelSelectorPipeline struct {
	filters []modelselector.Filter
	scorers []*WeightedScorer
	picker  modelselector.Picker
}

// Filters returns the filter plugins registered in the pipeline.
func (p *ModelSelectorPipeline) Filters() []modelselector.Filter {
	return p.filters
}

// Scorers returns the weighted scorer plugins registered in the pipeline.
func (p *ModelSelectorPipeline) Scorers() []*WeightedScorer {
	return p.scorers
}

// Picker returns the picker plugin registered in the pipeline.
func (p *ModelSelectorPipeline) Picker() modelselector.Picker {
	return p.picker
}

// WithPicker sets the given picker plugin as the Picker plugin.
func (p *ModelSelectorPipeline) WithPicker(picker modelselector.Picker) *ModelSelectorPipeline {
	p.picker = picker
	return p
}

// AddPlugins adds the given plugins to the pipeline according to the interfaces each plugin implements.
// A plugin may implement more than one interface.
// Special Case: In order to add a scorer, one must use NewWeightedScorer in order to provide a weight.
// If a scorer implements more than one interface, supplying a WeightedScorer is sufficient.
func (p *ModelSelectorPipeline) AddPlugins(pluginObjects ...plugin.Plugin) error {
	// Validate all plugins before modifying state to avoid inconsistent pipeline
	var newFilters []modelselector.Filter
	var newScorers []*WeightedScorer
	var newPicker modelselector.Picker

	for _, plug := range pluginObjects {
		if weightedScorer, ok := plug.(*WeightedScorer); ok {
			newScorers = append(newScorers, weightedScorer)
			plug = weightedScorer.Scorer
		} else if scorer, ok := plug.(modelselector.Scorer); ok {
			return fmt.Errorf("failed to register scorer '%s' without a weight. use NewWeightedScorer to register a scorer", scorer.TypedName())
		}
		if filter, ok := plug.(modelselector.Filter); ok {
			newFilters = append(newFilters, filter)
		}
		if picker, ok := plug.(modelselector.Picker); ok {
			if p.picker != nil || newPicker != nil {
				existing := p.picker
				if newPicker != nil {
					existing = newPicker
				}
				return fmt.Errorf("failed to set '%s' as picker, already have a registered picker plugin '%s'", picker.TypedName(), existing.TypedName())
			}
			newPicker = picker
		}
	}

	// Apply after successful validation
	p.filters = append(p.filters, newFilters...)
	p.scorers = append(p.scorers, newScorers...)
	if newPicker != nil {
		p.picker = newPicker
	}
	return nil
}

func (p *ModelSelectorPipeline) String() string {
	filterNames := make([]string, len(p.filters))
	for i, filter := range p.filters {
		filterNames[i] = filter.TypedName().String()
	}
	scorerNames := make([]string, len(p.scorers))
	for i, scorer := range p.scorers {
		scorerNames[i] = fmt.Sprintf("%s: %f", scorer.TypedName(), scorer.Weight())
	}

	pickerName := "<none>"
	if p.picker != nil {
		pickerName = p.picker.TypedName().String()
	}

	return fmt.Sprintf(
		"{Filters: [%s], Scorers: [%s], Picker: %s}",
		strings.Join(filterNames, ", "),
		strings.Join(scorerNames, ", "),
		pickerName,
	)
}

// Run runs the ModelSelectorPipeline: Filter → Score → Pick.
func (p *ModelSelectorPipeline) Run(ctx context.Context, request *requesthandling.InferenceRequest, cycleState *plugin.CycleState, candidateModels []datalayer.Model) (*modelselector.PipelineRunResult, error) {
	models := p.runFilterPlugins(ctx, request, cycleState, candidateModels)
	if len(models) == 0 {
		// Typed so the handler maps it to an HTTP ImmediateResponse instead of
		// failing the ext_proc stream.
		return nil, errcommon.Error{Code: errcommon.ResourceExhausted, Msg: "no models available after filtering"}
	}

	weightedScorePerModel := p.runScorerPlugins(ctx, request, cycleState, models)

	result := p.runPickerPlugin(ctx, cycleState, weightedScorePerModel)

	return result, nil
}

func (p *ModelSelectorPipeline) runFilterPlugins(ctx context.Context, request *requesthandling.InferenceRequest, cycleState *plugin.CycleState, models []datalayer.Model) []datalayer.Model {
	logger := log.FromContext(ctx)

	// Cache loggers and check Enabled() once to avoid per-iteration allocations
	// from argument boxing when logging at that level is disabled.
	verboseLogger := logger.V(logutil.VERBOSE)
	verboseEnabled := verboseLogger.Enabled()
	debugLogger := logger.V(logutil.DEBUG)
	debugEnabled := debugLogger.Enabled()

	filteredModels := models

	if debugEnabled {
		debugLogger.Info("Before running filter plugins", "models", len(filteredModels))
	}

	tracer := tracing.Tracer(modelSelectorTracerScope)
	for _, filter := range p.filters {
		typedName := filter.TypedName()
		if verboseEnabled {
			verboseLogger.Info("Running filter plugin", "plugin", typedName)
		}
		spanCtx, span := tracer.Start(ctx, "plugin."+typedName.Type,
			trace.WithSpanKind(trace.SpanKindInternal),
			trace.WithAttributes(
				attribute.String("llm_d.plugin.extension_point", filterExtensionPoint),
				attribute.String("llm_d.plugin.type", typedName.Type),
				attribute.String("llm_d.plugin.name", typedName.Name),
				attribute.Int("llm_d.filter.candidates_in", len(filteredModels)),
			))
		before := time.Now()
		filteredModels = filter.Filter(spanCtx, cycleState, request, filteredModels)
		metrics.RecordPluginProcessingLatency(filterExtensionPoint, typedName.Type, typedName.Name, time.Since(before))
		span.SetAttributes(attribute.Int("llm_d.filter.candidates_out", len(filteredModels)))
		span.End()
		if debugEnabled {
			debugLogger.Info("Completed running filter plugin", "plugin", typedName, "remainingModels", len(filteredModels))
		}
		if len(filteredModels) == 0 {
			if verboseEnabled {
				verboseLogger.Info("Filter eliminated all models", "plugin", typedName)
			}
			break
		}
	}
	verboseLogger.Info("Completed running filter plugins")

	return filteredModels
}

func (p *ModelSelectorPipeline) runScorerPlugins(ctx context.Context, request *requesthandling.InferenceRequest, cycleState *plugin.CycleState, models []datalayer.Model) map[string]*modelselector.ScoredModel {
	logger := log.FromContext(ctx)

	// Cache loggers and check Enabled() once to avoid per-iteration allocations
	// from argument boxing when logging at that level is disabled.
	verboseLogger := logger.V(logutil.VERBOSE)
	verboseEnabled := verboseLogger.Enabled()
	debugLogger := logger.V(logutil.DEBUG)
	debugEnabled := debugLogger.Enabled()

	// Create one big array for all ScoredModels instead of allocating each one
	// separately. This reduces memory allocations from N to 1.
	n := len(models)
	storage := make([]modelselector.ScoredModel, n)
	scoredModels := make(map[string]*modelselector.ScoredModel, n)
	for i, model := range models {
		storage[i] = modelselector.ScoredModel{Model: model, Score: 0}
		scoredModels[model.GetName()] = &storage[i]
	}

	tracer := tracing.Tracer(modelSelectorTracerScope)
	for _, scorer := range p.scorers {
		typedName := scorer.TypedName()
		if verboseEnabled {
			verboseLogger.Info("Running scorer plugin", "plugin", typedName)
		}
		spanCtx, span := tracer.Start(ctx, "plugin."+typedName.Type,
			trace.WithSpanKind(trace.SpanKindInternal),
			trace.WithAttributes(
				attribute.String("llm_d.plugin.extension_point", scorerExtensionPoint),
				attribute.String("llm_d.plugin.type", typedName.Type),
				attribute.String("llm_d.plugin.name", typedName.Name),
				attribute.Int("llm_d.scorer.candidate_count", len(models)),
				attribute.Float64("llm_d.scorer.weight", scorer.Weight()),
			))
		before := time.Now()
		scores := scorer.Score(spanCtx, cycleState, request, models)
		metrics.RecordPluginProcessingLatency(scorerExtensionPoint, typedName.Type, typedName.Name, time.Since(before))
		if len(scores) > 0 {
			var maxScore, totalScore float64
			first := true
			for _, score := range scores {
				if first || score > maxScore {
					maxScore = score
				}
				first = false
				totalScore += score
			}
			span.SetAttributes(
				attribute.Float64("llm_d.scorer.score.max", maxScore),
				attribute.Float64("llm_d.scorer.score.avg", totalScore/float64(len(scores))),
			)
		}
		span.End()
		for model, score := range scores {
			if sm, exists := scoredModels[model.GetName()]; exists {
				sm.Score += enforceScoreRange(score) * scorer.Weight()
			}
		}
		if debugEnabled {
			debugLogger.Info("Completed running scorer plugin", "plugin", typedName)
		}
	}
	verboseLogger.Info("Completed running scorer plugins")

	return scoredModels
}

func (p *ModelSelectorPipeline) runPickerPlugin(ctx context.Context, cycleState *plugin.CycleState, scoredModelMap map[string]*modelselector.ScoredModel) *modelselector.PipelineRunResult {
	logger := log.FromContext(ctx)

	// Cache loggers and check Enabled() once to avoid allocations from argument
	// boxing when logging at that level is disabled.
	verboseLogger := logger.V(logutil.VERBOSE)
	verboseEnabled := verboseLogger.Enabled()
	debugLogger := logger.V(logutil.DEBUG)
	debugEnabled := debugLogger.Enabled()

	scoredModels := make([]*modelselector.ScoredModel, len(scoredModelMap))
	i := 0
	for _, sm := range scoredModelMap {
		scoredModels[i] = sm
		i++
	}

	typedName := p.picker.TypedName()
	if verboseEnabled {
		verboseLogger.Info("Running picker plugin", "plugin", typedName)
	}
	spanCtx, span := tracing.Tracer(modelSelectorTracerScope).Start(ctx, "plugin."+typedName.Type,
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("llm_d.plugin.extension_point", pickerExtensionPoint),
			attribute.String("llm_d.plugin.type", typedName.Type),
			attribute.String("llm_d.plugin.name", typedName.Name),
			attribute.Int("llm_d.picker.candidate_count", len(scoredModels)),
		))
	before := time.Now()
	result := p.picker.Pick(spanCtx, cycleState, scoredModels)
	metrics.RecordPluginProcessingLatency(pickerExtensionPoint, typedName.Type, typedName.Name, time.Since(before))
	span.SetAttributes(attribute.String("llm_d.picker.selected_model", result.TargetModel.GetName()))
	span.End()
	if debugEnabled {
		debugLogger.Info("Completed running picker plugin", "plugin", typedName, "result", result)
	}

	return result
}

func enforceScoreRange(score float64) float64 {
	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}
