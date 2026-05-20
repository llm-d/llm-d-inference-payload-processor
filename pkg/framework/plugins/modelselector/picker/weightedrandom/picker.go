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

package weightedrandom

import (
	"cmp"
	"context"
	"encoding/json"
	"math"
	"slices"

	"sigs.k8s.io/controller-runtime/pkg/log"

	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/modelselector"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/modelselector/picker"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/modelselector/picker/random"
)

const (
	// WeightedRandomPickerType is the registered name of the weighted random picker plugin.
	WeightedRandomPickerType = "weighted-random-picker"
)

// compile-time type validation
var _ modelselector.Picker = &WeightedRandomPicker{}

// weightedScoredModel represents a scored model with its A-Res sampling key
type weightedScoredModel struct {
	*modelselector.ScoredModel
	key float64
}

// WeightedRandomPickerFactory defines the factory function for WeightedRandomPicker.
func WeightedRandomPickerFactory(name string, _ json.RawMessage, _ framework.Handle) (framework.Plugin, error) {
	return NewWeightedRandomPicker().WithName(name), nil
}

// NewWeightedRandomPicker initializes a new WeightedRandomPicker and returns its pointer.
func NewWeightedRandomPicker() *WeightedRandomPicker {
	return &WeightedRandomPicker{
		typedName:    framework.TypedName{Type: WeightedRandomPickerType, Name: WeightedRandomPickerType},
		randomPicker: random.NewRandomPicker(),
	}
}

// WeightedRandomPicker picks model from the list of candidates based on weighted random sampling using A-Res algorithm.
// Reference: https://utopia.duth.gr/~pefraimi/research/data/2007EncOfAlg.pdf.
//
// The picker at its core is picking model randomly, where the probability of the model to get picked is derived
// from its weighted score.
// Algorithm:
// - Uses A-Res (Algorithm for Reservoir Sampling): keyᵢ = Uᵢ^(1/wᵢ)
// - Selects k items with largest keys for mathematically correct weighted sampling
// - More efficient than traditional cumulative probability approach
//
// Key characteristics:
// - Mathematically correct weighted random sampling
// - Single pass algorithm with O(n + k log k) complexity
type WeightedRandomPicker struct {
	typedName    framework.TypedName
	randomPicker *random.RandomPicker // fallback for zero weights

}

// WithName sets the name of the picker.
func (p *WeightedRandomPicker) WithName(name string) *WeightedRandomPicker {
	p.typedName.Name = name
	p.randomPicker.WithName(name)
	return p
}

// TypedName returns the type and name tuple of this plugin instance.
func (p *WeightedRandomPicker) TypedName() framework.TypedName {
	return p.typedName
}

// Pick selects the model randomly from the list of candidates, where the probability of the model to get picked is derived
// from its weighted score.
func (p *WeightedRandomPicker) Pick(ctx context.Context, cycleState *framework.CycleState, scoredModels []*modelselector.ScoredModel) *modelselector.ProfileRunResult {
	// Check if there is at least one model with Score > 0, if not let random picker run
	if slices.IndexFunc(scoredModels, func(scoredModel *modelselector.ScoredModel) bool { return scoredModel.Score > 0 }) == -1 {
		log.FromContext(ctx).V(logutil.DEBUG).Info("All scores are zero, delegating to RandomPicker for uniform selection")
		return p.randomPicker.Pick(ctx, cycleState, scoredModels)
	}

	log.FromContext(ctx).V(logutil.DEBUG).Info("Selecting model by weighted random", "numCandidates", len(scoredModels),
		"scoredModels", scoredModels)

	// A-Res algorithm: keyᵢ = Uᵢ^(1/wᵢ)
	weightedModels := make([]weightedScoredModel, len(scoredModels))

	for i, scoredModel := range scoredModels {
		// Handle zero score
		if scoredModel.Score <= 0 {
			// Assign key=0 for zero-score model (effectively excludes them from selection)
			weightedModels[i] = weightedScoredModel{ScoredModel: scoredModel, key: 0}
			continue
		}

		// If we're here the scoredModel.Score > 0. Generate a random number U in (0,1)
		u := picker.PickerRand.Float64()
		if u == 0 {
			u = 1e-10 // Avoid 0 to ensure positive key
		}

		weightedModels[i] = weightedScoredModel{ScoredModel: scoredModel, key: math.Pow(u, 1.0/scoredModel.Score)} // key = U^(1/weight)
	}

	slices.SortFunc(weightedModels, func(a, b weightedScoredModel) int {
		return cmp.Compare(b.key, a.key)
	})

	return &modelselector.ProfileRunResult{TargetModel: weightedModels[0].Model}
}
