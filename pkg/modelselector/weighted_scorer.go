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
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/modelselector"
)

// NewWeightedScorer initializes a new WeightedScorer and returns its pointer.
func NewWeightedScorer(scorer modelselector.Scorer, weight float64) *WeightedScorer {
	return &WeightedScorer{
		Scorer: scorer,
		weight: weight,
	}
}

// WeightedScorer is a struct that encapsulates a scorer with its weight.
type WeightedScorer struct {
	modelselector.Scorer
	weight float64
}

// Weight returns the weight of the scorer.
func (s *WeightedScorer) Weight() float64 {
	return s.weight
}
