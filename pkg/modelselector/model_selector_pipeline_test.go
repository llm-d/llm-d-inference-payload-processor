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
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/modelselector"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
)

type testFilter struct {
	typedName plugin.TypedName
	filterFn  func(models []datalayer.Model) []datalayer.Model
	callCount int
}

func (f *testFilter) TypedName() plugin.TypedName { return f.typedName }
func (f *testFilter) Filter(_ context.Context, _ *plugin.CycleState, _ *requesthandling.InferenceRequest, models []datalayer.Model) []datalayer.Model {
	f.callCount++
	if f.filterFn != nil {
		return f.filterFn(models)
	}
	return models
}

type testScorer struct {
	typedName plugin.TypedName
	scoreFn   func(models []datalayer.Model) map[datalayer.Model]float64
	callCount int
}

func (s *testScorer) TypedName() plugin.TypedName { return s.typedName }
func (s *testScorer) Score(_ context.Context, _ *plugin.CycleState, _ *requesthandling.InferenceRequest, models []datalayer.Model) map[datalayer.Model]float64 {
	s.callCount++
	if s.scoreFn != nil {
		return s.scoreFn(models)
	}
	scores := make(map[datalayer.Model]float64, len(models))
	for _, m := range models {
		scores[m] = 0.5
	}
	return scores
}

type testPicker struct {
	typedName plugin.TypedName
	callCount int
}

func (p *testPicker) TypedName() plugin.TypedName { return p.typedName }
func (p *testPicker) Pick(_ context.Context, _ *plugin.CycleState, scoredModels []*modelselector.ScoredModel) *modelselector.PipelineRunResult {
	p.callCount++
	if len(scoredModels) == 0 {
		return nil
	}
	best := scoredModels[0]
	for _, sm := range scoredModels[1:] {
		if sm.Score > best.Score {
			best = sm
		}
	}
	return &modelselector.PipelineRunResult{TargetModel: best.Model}
}

func TestPipelineRun(t *testing.T) {
	modelA := datalayer.NewModel("model-a")
	modelB := datalayer.NewModel("model-b")
	modelC := datalayer.NewModel("model-c")

	tests := []struct {
		name      string
		models    []datalayer.Model
		filter    *testFilter
		scorer    *testScorer
		wantModel string
		wantErr   bool
	}{
		{
			name:   "basic selection with scorer",
			models: []datalayer.Model{modelA, modelB, modelC},
			scorer: &testScorer{
				typedName: plugin.TypedName{Type: "test-scorer", Name: "cost"},
				scoreFn: func(models []datalayer.Model) map[datalayer.Model]float64 {
					scores := make(map[datalayer.Model]float64)
					for _, m := range models {
						switch m.GetName() {
						case "model-a":
							scores[m] = 0.9
						case "model-b":
							scores[m] = 0.3
						case "model-c":
							scores[m] = 0.6
						}
					}
					return scores
				},
			},
			wantModel: "model-a",
		},
		{
			name:   "filter removes models before scoring",
			models: []datalayer.Model{modelA, modelB, modelC},
			filter: &testFilter{
				typedName: plugin.TypedName{Type: "test-filter", Name: "availability"},
				filterFn: func(models []datalayer.Model) []datalayer.Model {
					var result []datalayer.Model
					for _, m := range models {
						if m.GetName() != "model-a" {
							result = append(result, m)
						}
					}
					return result
				},
			},
			scorer: &testScorer{
				typedName: plugin.TypedName{Type: "test-scorer", Name: "cost"},
				scoreFn: func(models []datalayer.Model) map[datalayer.Model]float64 {
					scores := make(map[datalayer.Model]float64)
					for _, m := range models {
						switch m.GetName() {
						case "model-b":
							scores[m] = 0.3
						case "model-c":
							scores[m] = 0.8
						}
					}
					return scores
				},
			},
			wantModel: "model-c",
		},
		{
			name:   "all models filtered returns error",
			models: []datalayer.Model{modelA, modelB},
			filter: &testFilter{
				typedName: plugin.TypedName{Type: "test-filter", Name: "block-all"},
				filterFn: func(_ []datalayer.Model) []datalayer.Model {
					return []datalayer.Model{}
				},
			},
			wantErr: true,
		},
		{
			name:      "picker runs with zero scores when no scorers configured",
			models:    []datalayer.Model{modelA, modelB},
			wantModel: "", // any model is valid since all have score 0
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			picker := &testPicker{typedName: plugin.TypedName{Type: "test-picker", Name: "max-score"}}
			pipeline := NewModelSelectorPipeline().WithPicker(picker)

			if tt.filter != nil {
				if err := pipeline.AddPlugins(tt.filter); err != nil {
					t.Fatalf("AddPlugins(filter) failed: %v", err)
				}
			}
			if tt.scorer != nil {
				if err := pipeline.AddPlugins(NewWeightedScorer(tt.scorer, 1.0)); err != nil {
					t.Fatalf("AddPlugins(scorer) failed: %v", err)
				}
			}

			result, err := pipeline.Run(context.Background(), requesthandling.NewInferenceRequest(), plugin.NewCycleState(), tt.models)

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
			if tt.wantModel != "" && result.TargetModel.GetName() != tt.wantModel {
				t.Errorf("expected model %q, got %q", tt.wantModel, result.TargetModel.GetName())
			}
		})
	}
}

func TestScoreWeightAccumulation(t *testing.T) {
	modelA := datalayer.NewModel("model-a")
	modelB := datalayer.NewModel("model-b")

	scorer1 := &testScorer{
		typedName: plugin.TypedName{Type: "test-scorer", Name: "cost"},
		scoreFn: func(models []datalayer.Model) map[datalayer.Model]float64 {
			scores := make(map[datalayer.Model]float64)
			for _, m := range models {
				if m.GetName() == "model-a" {
					scores[m] = 1.0
				} else {
					scores[m] = 0.0
				}
			}
			return scores
		},
	}
	scorer2 := &testScorer{
		typedName: plugin.TypedName{Type: "test-scorer", Name: "latency"},
		scoreFn: func(models []datalayer.Model) map[datalayer.Model]float64 {
			scores := make(map[datalayer.Model]float64)
			for _, m := range models {
				if m.GetName() == "model-a" {
					scores[m] = 0.0
				} else {
					scores[m] = 1.0
				}
			}
			return scores
		},
	}

	// cost weight=3, latency weight=1 → model-a should win (3*1.0 + 1*0.0 = 3.0 vs 3*0.0 + 1*1.0 = 1.0)
	picker := &testPicker{typedName: plugin.TypedName{Type: "test-picker", Name: "max-score"}}
	pipeline := NewModelSelectorPipeline().WithPicker(picker)
	if err := pipeline.AddPlugins(NewWeightedScorer(scorer1, 3.0), NewWeightedScorer(scorer2, 1.0)); err != nil {
		t.Fatalf("AddPlugins failed: %v", err)
	}

	result, err := pipeline.Run(context.Background(), requesthandling.NewInferenceRequest(), plugin.NewCycleState(), []datalayer.Model{modelA, modelB})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TargetModel.GetName() != "model-a" {
		t.Errorf("expected model-a (higher weighted score), got %q", result.TargetModel.GetName())
	}
}

func TestScoreRangeEnforcement(t *testing.T) {
	tests := []struct {
		name  string
		score float64
		want  float64
	}{
		{"normal score", 0.5, 0.5},
		{"zero score", 0.0, 0.0},
		{"max score", 1.0, 1.0},
		{"negative score clamped to 0", -0.5, 0.0},
		{"score above 1 clamped to 1", 1.5, 1.0},
		{"large negative clamped to 0", -100.0, 0.0},
		{"large positive clamped to 1", 100.0, 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := enforceScoreRange(tt.score)
			if got != tt.want {
				t.Errorf("enforceScoreRange(%f) = %f, want %f", tt.score, got, tt.want)
			}
		})
	}
}

func TestAddPlugins(t *testing.T) {
	t.Run("scorer without weight returns error", func(t *testing.T) {
		pipeline := NewModelSelectorPipeline()
		scorer := &testScorer{typedName: plugin.TypedName{Type: "test-scorer", Name: "cost"}}
		err := pipeline.AddPlugins(scorer)
		if err == nil {
			t.Fatal("expected error for scorer without weight")
		}
	})

	t.Run("duplicate picker returns error", func(t *testing.T) {
		pipeline := NewModelSelectorPipeline()
		picker1 := &testPicker{typedName: plugin.TypedName{Type: "test-picker", Name: "first"}}
		picker2 := &testPicker{typedName: plugin.TypedName{Type: "test-picker", Name: "second"}}
		if err := pipeline.AddPlugins(picker1); err != nil {
			t.Fatalf("first picker should succeed: %v", err)
		}
		err := pipeline.AddPlugins(picker2)
		if err == nil {
			t.Fatal("expected error for duplicate picker")
		}
	})

	t.Run("weighted scorer registered correctly", func(t *testing.T) {
		pipeline := NewModelSelectorPipeline()
		scorer := &testScorer{typedName: plugin.TypedName{Type: "test-scorer", Name: "cost"}}
		ws := NewWeightedScorer(scorer, 2.0)
		if err := pipeline.AddPlugins(ws); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(pipeline.scorers) != 1 {
			t.Errorf("expected 1 scorer, got %d", len(pipeline.scorers))
		}
		if pipeline.scorers[0].Weight() != 2.0 {
			t.Errorf("expected weight 2.0, got %f", pipeline.scorers[0].Weight())
		}
	})
}
