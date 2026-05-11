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
	"testing"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/modelselector"
)

func TestWeightedRandomPicker(t *testing.T) {
	modelA := datalayer.NewModel("model-a")
	modelB := datalayer.NewModel("model-b")

	t.Run("returns a model", func(t *testing.T) {
		p := NewWeightedRandomPicker()
		input := []*modelselector.ScoredModel{
			{Model: modelA, Score: 0.9},
			{Model: modelB, Score: 0.1},
		}

		result := p.Pick(context.Background(), framework.NewCycleState(), input)
		if result == nil {
			t.Fatal("expected result, got nil")
		}
		if result.TargetModel == nil {
			t.Fatal("expected target model, got nil")
		}
	})

	t.Run("single model returns that model", func(t *testing.T) {
		p := NewWeightedRandomPicker()
		input := []*modelselector.ScoredModel{
			{Model: modelA, Score: 1.0},
		}

		result := p.Pick(context.Background(), framework.NewCycleState(), input)
		if result.TargetModel.GetName() != "model-a" {
			t.Errorf("expected model-a, got %q", result.TargetModel.GetName())
		}
	})

	t.Run("zero scores selects uniformly at random", func(t *testing.T) {
		p := NewWeightedRandomPicker()
		input := []*modelselector.ScoredModel{
			{Model: modelA, Score: 0},
			{Model: modelB, Score: 0},
		}

		result := p.Pick(context.Background(), framework.NewCycleState(), input)
		if result == nil || result.TargetModel == nil {
			t.Fatal("expected a result even with zero scores")
		}
	})

	t.Run("higher score model selected more often", func(t *testing.T) {
		p := NewWeightedRandomPicker()
		counts := map[string]int{}
		iterations := 1000

		for range iterations {
			input := []*modelselector.ScoredModel{
				{Model: modelA, Score: 0.99},
				{Model: modelB, Score: 0.01},
			}
			result := p.Pick(context.Background(), framework.NewCycleState(), input)
			counts[result.TargetModel.GetName()]++
		}

		if counts["model-a"] <= counts["model-b"] {
			t.Errorf("expected model-a (score 0.99) to be selected more often than model-b (score 0.01), got a=%d b=%d", counts["model-a"], counts["model-b"])
		}
	})
}

func TestWeightedRandomPickerTypedName(t *testing.T) {
	p := NewWeightedRandomPicker()
	if p.TypedName().Type != WeightedRandomPickerType {
		t.Errorf("expected type %q, got %q", WeightedRandomPickerType, p.TypedName().Type)
	}
}
