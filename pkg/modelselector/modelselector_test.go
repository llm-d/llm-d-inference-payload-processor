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
	"testing"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
)

func TestSelect(t *testing.T) {
	modelA := datalayer.NewModel("model-a")
	modelB := datalayer.NewModel("model-b")

	tests := []struct {
		name      string
		models    []datalayer.Model
		wantModel string
		wantErr   bool
	}{
		{
			name:      "selects best model",
			models:    []datalayer.Model{modelA, modelB},
			wantModel: "model-a",
		},
		{
			name:    "no candidates returns error",
			models:  []datalayer.Model{},
			wantErr: true,
		},
		{
			name:    "nil candidates returns error",
			models:  nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scorer := &testScorer{
				typedName: plugin.TypedName{Type: "test-scorer", Name: "cost"},
				scoreFn: func(models []datalayer.Model) map[datalayer.Model]float64 {
					scores := make(map[datalayer.Model]float64)
					for _, m := range models {
						if m.GetName() == "model-a" {
							scores[m] = 0.9
						} else {
							scores[m] = 0.1
						}
					}
					return scores
				},
			}
			picker := &testPicker{typedName: plugin.TypedName{Type: "test-picker", Name: "max-score"}}

			pipeline := NewModelSelectorPipeline().WithPicker(picker)
			if err := pipeline.AddPlugins(NewWeightedScorer(scorer, 1.0)); err != nil {
				t.Fatalf("AddPlugins failed: %v", err)
			}

			selector := NewModelSelector(pipeline)

			result, err := selector.Select(context.Background(), requesthandling.NewInferenceRequest(), plugin.NewCycleState(), tt.models)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result == nil {
				t.Fatal("expected result, got nil")
			}
			if result.TargetModel.GetName() != tt.wantModel {
				t.Errorf("expected model %q, got %q", tt.wantModel, result.TargetModel.GetName())
			}
		})
	}
}

func TestSelectWithFilterAndScorer(t *testing.T) {
	modelA := datalayer.NewModel("llama-70b")
	modelB := datalayer.NewModel("llama-8b")
	modelC := datalayer.NewModel("mistral-7b")

	filter := &testFilter{
		typedName: plugin.TypedName{Type: "test-filter", Name: "rate-limit"},
		filterFn: func(models []datalayer.Model) []datalayer.Model {
			var result []datalayer.Model
			for _, m := range models {
				if m.GetName() != "mistral-7b" {
					result = append(result, m)
				}
			}
			return result
		},
	}

	scorer := &testScorer{
		typedName: plugin.TypedName{Type: "test-scorer", Name: "cost"},
		scoreFn: func(models []datalayer.Model) map[datalayer.Model]float64 {
			scores := make(map[datalayer.Model]float64)
			for _, m := range models {
				switch m.GetName() {
				case "llama-70b":
					scores[m] = 0.3
				case "llama-8b":
					scores[m] = 0.9
				}
			}
			return scores
		},
	}

	picker := &testPicker{typedName: plugin.TypedName{Type: "test-picker", Name: "max-score"}}

	pipeline := NewModelSelectorPipeline().WithPicker(picker)
	if err := pipeline.AddPlugins(filter, NewWeightedScorer(scorer, 1.0)); err != nil {
		t.Fatalf("AddPlugins failed: %v", err)
	}

	selector := NewModelSelector(pipeline)

	result, err := selector.Select(context.Background(), requesthandling.NewInferenceRequest(), plugin.NewCycleState(), []datalayer.Model{modelA, modelB, modelC})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TargetModel.GetName() != "llama-8b" {
		t.Errorf("expected llama-8b (cheapest after filtering), got %q", result.TargetModel.GetName())
	}

	if filter.callCount != 1 {
		t.Errorf("expected filter called once, got %d", filter.callCount)
	}
	if scorer.callCount != 1 {
		t.Errorf("expected scorer called once, got %d", scorer.callCount)
	}
	if picker.callCount != 1 {
		t.Errorf("expected picker called once, got %d", picker.callCount)
	}
}
