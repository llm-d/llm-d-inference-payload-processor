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

	"sigs.k8s.io/controller-runtime/pkg/log"

	errcommon "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/error"
	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
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

	for _, filter := range p.filters {
		if verboseEnabled {
			verboseLogger.Info("Running filter plugin", "plugin", filter.TypedName())
		}
		before := time.Now()
		filteredModels = filter.Filter(ctx, cycleState, request, filteredModels)
		metrics.RecordPluginProcessingLatency(filterExtensionPoint, filter.TypedName().Type, filter.TypedName().Name, time.Since(before))
		if debugEnabled {
			debugLogger.Info("Completed running filter plugin", "plugin", filter.TypedName(), "remainingModels", len(filteredModels))
		}
		if len(filteredModels) == 0 {
			if verboseEnabled {
				verboseLogger.Info("Filter eliminated all models", "plugin", filter.TypedName())
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

	for _, scorer := range p.scorers {
		if verboseEnabled {
			verboseLogger.Info("Running scorer plugin", "plugin", scorer.TypedName())
		}
		before := time.Now()
		scores := scorer.Score(ctx, cycleState, request, models)
		metrics.RecordPluginProcessingLatency(scorerExtensionPoint, scorer.TypedName().Type, scorer.TypedName().Name, time.Since(before))
		for model, score := range scores {
			if sm, exists := scoredModels[model.GetName()]; exists {
				sm.Score += enforceScoreRange(score) * scorer.Weight()
			}
		}
		if debugEnabled {
			debugLogger.Info("Completed running scorer plugin", "plugin", scorer.TypedName())
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

	if verboseEnabled {
		verboseLogger.Info("Running picker plugin", "plugin", p.picker.TypedName())
	}
	before := time.Now()
	result := p.picker.Pick(ctx, cycleState, scoredModels)
	metrics.RecordPluginProcessingLatency(pickerExtensionPoint, p.picker.TypedName().Type, p.picker.TypedName().Name, time.Since(before))
	if debugEnabled {
		debugLogger.Info("Completed running picker plugin", "plugin", p.picker.TypedName(), "result", result)
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
