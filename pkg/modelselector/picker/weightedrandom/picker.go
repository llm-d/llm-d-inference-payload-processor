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
	"context"
	"math"
	"sort"

	"sigs.k8s.io/controller-runtime/pkg/log"

	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/modelselector"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/modelselector/picker"
)

const (
	WeightedRandomPickerType = "weighted-random-picker"
)

var _ modelselector.Picker = &WeightedRandomPicker{}

type weightedScoredModel struct {
	*modelselector.ScoredModel
	key float64
}

// NewWeightedRandomPicker initializes a new WeightedRandomPicker and returns its pointer.
func NewWeightedRandomPicker() *WeightedRandomPicker {
	return &WeightedRandomPicker{
		typedName: framework.TypedName{Type: WeightedRandomPickerType, Name: WeightedRandomPickerType},
	}
}

// WeightedRandomPicker picks a model from the list of candidates based on weighted random
// sampling using the A-Res algorithm.
// The probability of a model being selected is proportional to its score.
// Reference: https://utopia.duth.gr/~pefraimi/research/data/2007EncOfAlg.pdf
type WeightedRandomPicker struct {
	typedName framework.TypedName
}

func (p *WeightedRandomPicker) TypedName() framework.TypedName {
	return p.typedName
}

// Pick selects a model randomly, where the probability is derived from its weighted score.
func (p *WeightedRandomPicker) Pick(ctx context.Context, _ *framework.CycleState, scoredModels []*modelselector.ScoredModel) *modelselector.ProfileRunResult {
	log.FromContext(ctx).V(logutil.DEBUG).Info("Selecting model by weighted random", "numCandidates", len(scoredModels))

	hasPositiveScore := false
	for _, sm := range scoredModels {
		if sm.Score > 0 {
			hasPositiveScore = true
			break
		}
	}

	if !hasPositiveScore {
		log.FromContext(ctx).V(logutil.DEBUG).Info("All scores are zero, selecting uniformly at random")
		picker.ShuffleScoredModels(scoredModels)
		return &modelselector.ProfileRunResult{TargetModel: scoredModels[0].Model}
	}

	weightedModels := make([]weightedScoredModel, len(scoredModels))
	for i, sm := range scoredModels {
		if sm.Score <= 0 {
			weightedModels[i] = weightedScoredModel{ScoredModel: sm, key: 0}
			continue
		}

		u := picker.PickerRand.Float64()
		if u == 0 {
			u = 1e-10
		}

		weightedModels[i] = weightedScoredModel{ScoredModel: sm, key: math.Pow(u, 1.0/sm.Score)}
	}

	sort.Slice(weightedModels, func(i, j int) bool {
		return weightedModels[i].key > weightedModels[j].key
	})

	return &modelselector.ProfileRunResult{TargetModel: weightedModels[0].Model}
}
