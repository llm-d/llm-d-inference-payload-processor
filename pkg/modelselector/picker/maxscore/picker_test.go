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
	"testing"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/modelselector"
)

func TestMaxScorePicker(t *testing.T) {
	modelA := datalayer.NewModel("model-a")
	modelB := datalayer.NewModel("model-b")
	modelC := datalayer.NewModel("model-c")

	tests := []struct {
		name      string
		input     []*modelselector.ScoredModel
		wantModel string
	}{
		{
			name: "picks highest scored model",
			input: []*modelselector.ScoredModel{
				{Model: modelA, Score: 10},
				{Model: modelB, Score: 25},
				{Model: modelC, Score: 15},
			},
			wantModel: "model-b",
		},
		{
			name: "single model returns that model",
			input: []*modelselector.ScoredModel{
				{Model: modelA, Score: 5},
			},
			wantModel: "model-a",
		},
		{
			name: "equal scores still returns a model",
			input: []*modelselector.ScoredModel{
				{Model: modelA, Score: 50},
				{Model: modelB, Score: 50},
			},
			// either model is valid with equal scores
		},
		{
			name: "zero scores still returns a model",
			input: []*modelselector.ScoredModel{
				{Model: modelA, Score: 0},
				{Model: modelB, Score: 0},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewMaxScorePicker()
			result := p.Pick(context.Background(), framework.NewCycleState(), tt.input)

			if result == nil {
				t.Fatal("expected result, got nil")
			}
			if result.TargetModel == nil {
				t.Fatal("expected target model, got nil")
			}
			if tt.wantModel != "" && result.TargetModel.GetName() != tt.wantModel {
				t.Errorf("expected %q, got %q", tt.wantModel, result.TargetModel.GetName())
			}
		})
	}
}

func TestMaxScorePickerTypedName(t *testing.T) {
	p := NewMaxScorePicker()
	if p.TypedName().Type != MaxScorePickerType {
		t.Errorf("expected type %q, got %q", MaxScorePickerType, p.TypedName().Type)
	}
}
