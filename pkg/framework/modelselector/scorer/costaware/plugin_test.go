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

package costaware

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/datalayer"
)

// TestFactory tests the Factory function
func TestFactory(t *testing.T) {
	tests := []struct {
		name          string
		pluginName    string
		rawParameters json.RawMessage
		expectError   bool
	}{
		{
			name:          "factory with nil parameters",
			pluginName:    "test-scorer",
			rawParameters: nil,
			expectError:   false,
		},
		{
			name:          "factory with empty parameters",
			pluginName:    "my-scorer",
			rawParameters: json.RawMessage(`{}`),
			expectError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin, err := CostScorerFactory(tt.pluginName, tt.rawParameters, nil)
			if (err != nil) != tt.expectError {
				t.Errorf("Factory() error = %v, expectError %v", err, tt.expectError)
				return
			}
			if err == nil && plugin == nil {
				t.Error("Factory() returned nil plugin without error")
			}
		})
	}
}

// TestNewCostScorer tests the constructor
func TestNewCostScorer(t *testing.T) {
	scorer := NewCostScorer()
	if scorer == nil {
		t.Fatal("NewCostScorer() returned nil")
	}
}

// TestWithName tests the WithName method
func TestWithName(t *testing.T) {
	scorer := NewCostScorer()
	result := scorer.WithName("custom-name")

	if result != scorer {
		t.Error("WithName() should return the same instance for method chaining")
	}
}

// TestScore tests the Score method with various scenarios
func TestScore(t *testing.T) {
	ctx := context.Background()
	cycleState := framework.NewCycleState()
	request := framework.NewInferenceRequest()

	tests := []struct {
		name           string
		models         []datalayer.Model
		expectedScores map[string]float64
		description    string
	}{
		{
			name: "single model with price",
			models: []datalayer.Model{
				createModelWithPrice("model1", 10.0),
			},
			expectedScores: map[string]float64{
				"model1": 0.5,
			},
			description: "single model should get neutral score 0.5",
		},
		{
			name: "two models with different prices",
			models: []datalayer.Model{
				createModelWithPrice("model1", 10.0),
				createModelWithPrice("model2", 20.0),
			},
			expectedScores: map[string]float64{
				"model1": 0.6667, // 1 - 10/30 = 0.6667
				"model2": 0.3333, // 1 - 20/30 = 0.3333
			},
			description: "inverted sum normalization: 1 - price/sum(prices)",
		},
		{
			name: "three models with different prices",
			models: []datalayer.Model{
				createModelWithPrice("model1", 10.0),
				createModelWithPrice("model2", 20.0),
				createModelWithPrice("model3", 15.0),
			},
			expectedScores: map[string]float64{
				"model1": 0.7778, // 1 - 10/45 = 0.7778
				"model2": 0.5556, // 1 - 20/45 = 0.5556
				"model3": 0.6667, // 1 - 15/45 = 0.6667
			},
			description: "sum=45: cheapest gets highest score",
		},
		{
			name: "all models same price",
			models: []datalayer.Model{
				createModelWithPrice("model1", 10.0),
				createModelWithPrice("model2", 10.0),
				createModelWithPrice("model3", 10.0),
			},
			expectedScores: map[string]float64{
				"model1": 0.6667, // 1 - 10/30 = 0.6667
				"model2": 0.6667,
				"model3": 0.6667,
			},
			description: "all same price: each gets 1 - 10/30 = 0.6667",
		},
		{
			name: "models with zero price",
			models: []datalayer.Model{
				createModelWithPrice("model1", 0.0),
				createModelWithPrice("model2", 10.0),
			},
			expectedScores: map[string]float64{
				"model1": 1.0,
				"model2": 0.0,
			},
			description: "zero price is valid and should be the cheapest",
		},
		{
			name: "models with very close prices",
			models: []datalayer.Model{
				createModelWithPrice("model1", 10.0),
				createModelWithPrice("model2", 10.1),
			},
			expectedScores: map[string]float64{
				"model1": 0.5025, // 1 - 10.0/20.1 = 0.5025
				"model2": 0.4975, // 1 - 10.1/20.1 = 0.4975
			},
			description: "close prices produce close scores",
		},
		{
			name: "models with large price range",
			models: []datalayer.Model{
				createModelWithPrice("model1", 1.0),
				createModelWithPrice("model2", 1000.0),
				createModelWithPrice("model3", 500.5),
			},
			expectedScores: map[string]float64{
				"model1": 0.9993, // 1 - 1.0/1501.5 = 0.9993
				"model2": 0.3340, // 1 - 1000.0/1501.5 = 0.3340
				"model3": 0.6666, // 1 - 500.5/1501.5 = 0.6666
			},
			description: "large range: cheapest gets highest score",
		},
		{
			name: "all models with zero price",
			models: []datalayer.Model{
				createModelWithPrice("model1", 0.0),
				createModelWithPrice("model2", 0.0),
				createModelWithPrice("model3", 0.0),
			},
			expectedScores: map[string]float64{
				"model1": 1.0,
				"model2": 1.0,
				"model3": 1.0,
			},
			description: "all free models get perfect score 1.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scorer := NewCostScorer()
			scores := scorer.Score(ctx, cycleState, request, tt.models)

			// Check that we got the expected number of scores
			if len(scores) != len(tt.expectedScores) {
				t.Errorf("Score() returned %d scores, want %d. Description: %s",
					len(scores), len(tt.expectedScores), tt.description)
			}

			// Check each expected score
			for _, model := range tt.models {
				modelName := model.GetName()
				expectedScore, shouldBeScored := tt.expectedScores[modelName]
				actualScore, wasScored := scores[model]

				if shouldBeScored != wasScored {
					t.Errorf("Model %s: scored=%v, want scored=%v. Description: %s",
						modelName, wasScored, shouldBeScored, tt.description)
					continue
				}

				if shouldBeScored {
					// Allow small floating point differences
					if !floatEquals(actualScore, expectedScore, 0.0001) {
						t.Errorf("Model %s: score=%v, want %v. Description: %s",
							modelName, actualScore, expectedScore, tt.description)
					}
				}
			}
		})
	}
}

// TestPriceValueClone tests the Clone method of PriceValue
func TestPriceValueClone(t *testing.T) {
	original := &PriceValue{Value: 42.5}
	cloned := original.Clone()

	clonedPrice, ok := cloned.(*PriceValue)
	if !ok {
		t.Fatal("Clone() did not return *PriceValue type")
	}

	if clonedPrice.Value != original.Value {
		t.Errorf("Clone() value = %v, want %v", clonedPrice.Value, original.Value)
	}

	// Verify it's a copy, not a reference
	clonedPrice.Value = 100.0
	if original.Value == 100.0 {
		t.Error("Clone() did not create an independent copy")
	}
}

// Helper functions

func createModelWithPrice(name string, price float64) datalayer.Model {
	model := datalayer.NewModel(name)
	model.GetAttributes().Put(PriceAttributeKey, &PriceValue{Value: price})
	return model
}

func floatEquals(a, b, epsilon float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < epsilon
}
