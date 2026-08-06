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

package random

import (
	"context"
	"testing"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/modelselector"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
)

func TestRandomPicker_Pick(t *testing.T) {
	modelA := datalayer.NewModel("model-a")
	modelB := datalayer.NewModel("model-b")
	modelC := datalayer.NewModel("model-c")

	tests := []struct {
		name  string
		input []*modelselector.ScoredModel
	}{
		{
			name: "single model returns that model",
			input: []*modelselector.ScoredModel{
				{Model: modelA, Score: 1.0},
			},
		},
		{
			name: "returns a model from multiple candidates",
			input: []*modelselector.ScoredModel{
				{Model: modelA, Score: 0.1},
				{Model: modelB, Score: 0.2},
				{Model: modelC, Score: 0.7},
			},
		},
		{
			name: "works with zero scores",
			input: []*modelselector.ScoredModel{
				{Model: modelA, Score: 0},
				{Model: modelB, Score: 0},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewRandomPicker()
			result := p.Pick(context.Background(), plugin.NewCycleState(), tt.input)

			if result == nil {
				t.Fatal("expected result, got nil")
			}
			if result.TargetModel == nil {
				t.Fatal("expected target model, got nil")
			}

			// Verify returned model is one of the inputs
			found := false
			for _, sm := range tt.input {
				if result.TargetModel.GetName() == sm.GetName() {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("returned model %q not in input list", result.TargetModel.GetName())
			}
		})
	}
}

func TestRandomPicker_Pick_IgnoresScores(t *testing.T) {
	const (
		iterations = 1000
		minPercent = 30
		maxPercent = 70
	)

	modelA := datalayer.NewModel("model-a")
	modelB := datalayer.NewModel("model-b")

	input := []*modelselector.ScoredModel{
		{Model: modelA, Score: 0.99},
		{Model: modelB, Score: 0.01},
	}

	p := NewRandomPicker()
	counts := map[string]int{}

	for range iterations {
		result := p.Pick(context.Background(), plugin.NewCycleState(), input)
		counts[result.TargetModel.GetName()]++
	}

	minExpected := iterations * minPercent / 100
	maxExpected := iterations * maxPercent / 100

	for _, sm := range input {
		name := sm.GetName()
		count := counts[name]
		if count < minExpected || count > maxExpected {
			t.Errorf("model %q selected %d times (%.1f%%), expected between %d%% and %d%%",
				name, count, float64(count)/float64(iterations)*100,
				minPercent, maxPercent)
		}
	}
}

func TestRandomPicker_TypedName(t *testing.T) {
	p := NewRandomPicker()
	typed := p.TypedName()

	if typed.Type != RandomPickerType {
		t.Errorf("Type: expected %q, got %q", RandomPickerType, typed.Type)
	}
	if typed.Name != RandomPickerType {
		t.Errorf("Name: expected %q, got %q", RandomPickerType, typed.Name)
	}
}

func TestRandomPicker_WithName(t *testing.T) {
	tests := []struct {
		name     string
		newName  string
		wantName string
		wantType string
	}{
		{
			name:     "changes name",
			newName:  "custom-name",
			wantName: "custom-name",
			wantType: RandomPickerType,
		},
		{
			name:     "empty name",
			newName:  "",
			wantName: "",
			wantType: RandomPickerType,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := NewRandomPicker()
			returned := original.WithName(tt.newName)

			if returned.TypedName().Name != tt.wantName {
				t.Errorf("Name: expected %q, got %q", tt.wantName, returned.TypedName().Name)
			}
			if returned.TypedName().Type != tt.wantType {
				t.Errorf("Type: expected %q, got %q", tt.wantType, returned.TypedName().Type)
			}
			if original != returned {
				t.Error("WithName should return the same instance for method chaining")
			}
		})
	}
}

func TestRandomPickerFactory(t *testing.T) {
	t.Run("returns valid picker without error", func(t *testing.T) {
		p, err := RandomPickerFactory("my-picker", nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := p.(*RandomPicker); !ok {
			t.Fatal("expected *RandomPicker type")
		}
	})

	t.Run("ignores config parameter", func(t *testing.T) {
		_, err := RandomPickerFactory("test", []byte(`{"some": "config"}`), nil)
		if err != nil {
			t.Fatalf("config should be ignored, got error: %v", err)
		}
	})
}
