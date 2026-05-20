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
	"errors"
	"fmt"
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log"

	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/modelselector"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/metrics"
)

// compile-time interface validation
var _ modelselector.ModelSelectorProfile = &ModelSelectorProfile{}

const (
	filterExtensionPoint = "ModelSelectorFilter"
	scorerExtensionPoint = "ModelSelectorScorer"
	pickerExtensionPoint = "ModelSelectorPicker"
)

// NewModelSelectorProfile creates a new ModelSelectorProfile object and returns its pointer.
func NewModelSelectorProfile() *ModelSelectorProfile {
	return &ModelSelectorProfile{
		filters: []modelselector.Filter{},
		scorers: []*WeightedScorer{},
	}
}

// ModelSelectorProfile provides a profile configuration for the model-selector which influence model decisions.
type ModelSelectorProfile struct {
	filters []modelselector.Filter
	scorers []*WeightedScorer
	picker  modelselector.Picker
}

// WithFilters sets the given filter plugins as the Filter plugins.
func (p *ModelSelectorProfile) WithFilters(filters ...modelselector.Filter) *ModelSelectorProfile {
	p.filters = filters
	return p
}

// WithScorers sets the given scorer plugins as the Scorer plugins.
func (p *ModelSelectorProfile) WithScorers(scorers ...*WeightedScorer) *ModelSelectorProfile {
	p.scorers = scorers
	return p
}

// WithPicker sets the given picker plugin as the Picker plugin.
func (p *ModelSelectorProfile) WithPicker(picker modelselector.Picker) *ModelSelectorProfile {
	p.picker = picker
	return p
}

// AddPlugins adds the given plugins to the profile according to the interfaces each plugin implements.
// A plugin may implement more than one interface.
// Special Case: In order to add a scorer, one must use NewWeightedScorer in order to provide a weight.
// If a scorer implements more than one interface, supplying a WeightedScorer is sufficient.
func (p *ModelSelectorProfile) AddPlugins(pluginObjects ...plugin.Plugin) error {
	// Validate all plugins before modifying state to avoid inconsistent profile
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

func (p *ModelSelectorProfile) String() string {
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

// Run runs the ModelSelectorProfile pipeline: Filter → Score → Pick.
func (p *ModelSelectorProfile) Run(ctx context.Context, request *requesthandling.InferenceRequest, cycleState *plugin.CycleState, candidateModels []datalayer.Model) (*modelselector.ProfileRunResult, error) {
	models := p.runFilterPlugins(ctx, request, cycleState, candidateModels)
	if len(models) == 0 {
		return nil, errors.New("no models available after filtering")
	}

	weightedScorePerModel := p.runScorerPlugins(ctx, request, cycleState, models)

	result := p.runPickerPlugin(ctx, cycleState, weightedScorePerModel)

	return result, nil
}

func (p *ModelSelectorProfile) runFilterPlugins(ctx context.Context, request *requesthandling.InferenceRequest, cycleState *plugin.CycleState, models []datalayer.Model) []datalayer.Model {
	logger := log.FromContext(ctx)
	filteredModels := models
	logger.V(logutil.DEBUG).Info("Before running filter plugins", "models", len(filteredModels))

	for _, filter := range p.filters {
		logger.V(logutil.VERBOSE).Info("Running filter plugin", "plugin", filter.TypedName())
		before := time.Now()
		filteredModels = filter.Filter(ctx, cycleState, request, filteredModels)
		metrics.RecordPluginProcessingLatency(filterExtensionPoint, filter.TypedName().Type, filter.TypedName().Name, time.Since(before))
		logger.V(logutil.DEBUG).Info("Completed running filter plugin", "plugin", filter.TypedName(), "remainingModels", len(filteredModels))
		if len(filteredModels) == 0 {
			logger.V(logutil.VERBOSE).Info("Filter eliminated all models", "plugin", filter.TypedName())
			break
		}
	}
	logger.V(logutil.VERBOSE).Info("Completed running filter plugins")

	return filteredModels
}

func (p *ModelSelectorProfile) runScorerPlugins(ctx context.Context, request *requesthandling.InferenceRequest, cycleState *plugin.CycleState, models []datalayer.Model) map[string]*modelselector.ScoredModel {
	logger := log.FromContext(ctx)

	scoredModels := make(map[string]*modelselector.ScoredModel, len(models))
	for _, model := range models {
		scoredModels[model.GetName()] = &modelselector.ScoredModel{Model: model, Score: 0}
	}

	for _, scorer := range p.scorers {
		logger.V(logutil.VERBOSE).Info("Running scorer plugin", "plugin", scorer.TypedName())
		before := time.Now()
		scores := scorer.Score(ctx, cycleState, request, models)
		metrics.RecordPluginProcessingLatency(scorerExtensionPoint, scorer.TypedName().Type, scorer.TypedName().Name, time.Since(before))
		for model, score := range scores {
			if sm, exists := scoredModels[model.GetName()]; exists {
				sm.Score += enforceScoreRange(score) * scorer.Weight()
			}
		}
		logger.V(logutil.DEBUG).Info("Completed running scorer plugin", "plugin", scorer.TypedName())
	}
	logger.V(logutil.VERBOSE).Info("Completed running scorer plugins")

	return scoredModels
}

func (p *ModelSelectorProfile) runPickerPlugin(ctx context.Context, cycleState *plugin.CycleState, scoredModelMap map[string]*modelselector.ScoredModel) *modelselector.ProfileRunResult {
	logger := log.FromContext(ctx)

	scoredModels := make([]*modelselector.ScoredModel, len(scoredModelMap))
	i := 0
	for _, sm := range scoredModelMap {
		scoredModels[i] = sm
		i++
	}

	logger.V(logutil.VERBOSE).Info("Running picker plugin", "plugin", p.picker.TypedName())
	before := time.Now()
	result := p.picker.Pick(ctx, cycleState, scoredModels)
	metrics.RecordPluginProcessingLatency(pickerExtensionPoint, p.picker.TypedName().Type, p.picker.TypedName().Name, time.Since(before))
	logger.V(logutil.DEBUG).Info("Completed running picker plugin", "plugin", p.picker.TypedName(), "result", result)

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
