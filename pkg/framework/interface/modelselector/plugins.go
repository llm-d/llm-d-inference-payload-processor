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

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/datalayer"
)

// Filter defines the interface for filtering a list of candidate models based on context.
type Filter interface {
	framework.Plugin
	Filter(ctx context.Context, cycleState *framework.CycleState, request *framework.InferenceRequest, models []datalayer.Model) []datalayer.Model
}

// Scorer defines the interface for scoring a list of models based on context.
// Scorers must score models with a value within the range of [0,1] where 1 is the highest score.
// If a scorer returns value greater than 1, it will be treated as score 1.
// If a scorer returns value lower than 0, it will be treated as score 0.
type Scorer interface {
	framework.Plugin
	Score(ctx context.Context, cycleState *framework.CycleState, request *framework.InferenceRequest, models []datalayer.Model) map[datalayer.Model]float64
}

// Picker picks the final model(s) to send the request to.
type Picker interface {
	framework.Plugin
	Pick(ctx context.Context, cycleState *framework.CycleState, scoredModels []*ScoredModel) *ProfileRunResult
}
