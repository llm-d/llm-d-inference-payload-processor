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

package maxscore

import (
	"context"
	"encoding/json"
	"slices"

	"sigs.k8s.io/controller-runtime/pkg/log"

	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/modelselector"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/modelselector/picker"
)

const (
	// MaxScorePickerType is the registered name of the max score picker plugin.
	MaxScorePickerType = "max-score-picker"
)

// compile-time type validation
var _ modelselector.Picker = &MaxScorePicker{}

// MaxScorePickerFactory defines the factory function for MaxScorePicker.
func MaxScorePickerFactory(name string, _ json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
	return NewMaxScorePicker().WithName(name), nil
}

// NewMaxScorePicker initializes a new MaxScorePicker and returns its pointer.
func NewMaxScorePicker() *MaxScorePicker {
	return &MaxScorePicker{
		typedName: plugin.TypedName{Type: MaxScorePickerType, Name: MaxScorePickerType},
	}
}

// MaxScorePicker picks the model with the highest score calculated during the scoring phase.
type MaxScorePicker struct {
	typedName plugin.TypedName
}

// WithName sets the plugin name
func (p *MaxScorePicker) WithName(name string) *MaxScorePicker {
	p.typedName.Name = name
	return p
}

// TypedName returns the type and name tuple of this plugin instance.
func (p *MaxScorePicker) TypedName() plugin.TypedName {
	return p.typedName
}

// Pick selects the model with the highest score.
func (p *MaxScorePicker) Pick(ctx context.Context, _ *plugin.CycleState, scoredModels []*modelselector.ScoredModel) *modelselector.ProfileRunResult {
	log.FromContext(ctx).V(logutil.DEBUG).Info("selecting model from candidates by max score", "numCandidates", len(scoredModels),
		"scoredModels", scoredModels)

	// Shuffle in-place - needed for random tie break when scores are equal
	picker.ShuffleScoredModels(scoredModels)

	slices.SortStableFunc(scoredModels, func(i, j *modelselector.ScoredModel) int { // highest score first
		if i.Score > j.Score {
			return -1
		}
		if i.Score < j.Score {
			return 1
		}
		return 0
	})

	return &modelselector.ProfileRunResult{TargetModel: scoredModels[0].Model}
}
