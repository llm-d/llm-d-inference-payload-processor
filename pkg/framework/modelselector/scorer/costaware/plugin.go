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

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/modelselector"
)

// Package costaware provides a scorer that scores candidate models based on expected cost
// for an inference request, by ranking nominal prices of the models.
// Model prices are expressed in USD per 1M tokens.
// Each model in the model selector has a valid price.
// The actual cost is calculated as the product of the number of tokens and the price per 1M tokens.
// This scorer assumes that there are no price reversals and the lowest nominal price of a model
// results in the lowest cost for the request.

const (
	// CostScorerType is the type of the CostScorer scorer
	CostScorerType = "cost-scorer"

	// PriceAttributeKey is the key used to retrieve the price from model attributes
	PriceAttributeKey = "price"
)

// PriceValue is a Cloneable wrapper for float64 price values
type PriceValue struct {
	Value float64
}

// Clone implements the Cloneable interface
func (p *PriceValue) Clone() datalayer.Cloneable {
	return &PriceValue{Value: p.Value}
}

// compile-time type assertion
var _ modelselector.Scorer = &CostScorer{}

// Factory defines the factory function for the CostScorer scorer
func CostScorerFactory(name string, _ json.RawMessage) (framework.Plugin, error) {
	return NewCostScorer().WithName(name), nil
}

// NewCostScorer creates a new lowest price scorer
func NewCostScorer() *CostScorer {
	return &CostScorer{
		typedName: framework.TypedName{Type: CostScorerType},
	}
}

// CostScorer scorer that scores models based on their price
// Lower-priced models receive higher scores
type CostScorer struct {
	typedName framework.TypedName
}

// TypedName returns the typed name of the plugin.
func (s *CostScorer) TypedName() framework.TypedName {
	return s.typedName
}

// WithName sets the name of the plugin.
func (s *CostScorer) WithName(name string) *CostScorer {
	s.typedName.Name = name
	return s
}

// Score scores the given models in range of [0.0-1.0] based on their price.
// Scoring behavior:
//   - Cheapest model gets score 1.0, most expensive gets 0.0
//   - If all models have same price, all receive score 0.5
//   - Score formula: 1.0 - (price - minPrice) / (maxPrice - minPrice)
func (s *CostScorer) Score(_ context.Context, _ *framework.CycleState, _ *framework.InferenceRequest, models []datalayer.Model) map[datalayer.Model]float64 {
	priceValue, _ := models[0].GetAttributes().Get(PriceAttributeKey)
	minPrice := priceValue.(*PriceValue).Value
	maxPrice := minPrice

	for _, model := range models {
		priceValue, _ := model.GetAttributes().Get(PriceAttributeKey)
		price := priceValue.(*PriceValue).Value
		if price < minPrice {
			minPrice = price
		}
		if price > maxPrice {
			maxPrice = price
		}
	}

	modelScoreFunc := func(model datalayer.Model) float64 {
		if maxPrice == minPrice {
			// All models have the same price, assign neutral score
			return 0.5
		}
		priceValue, _ := model.GetAttributes().Get(PriceAttributeKey)
		price := priceValue.(*PriceValue).Value
		// Invert the score so that lower price = higher score
		return 1.0 - (price-minPrice)/(maxPrice-minPrice)
	}

	// Create a map to hold the score of each model candidate
	scores := make(map[datalayer.Model]float64, len(models))
	for _, model := range models {
		scores[model] = modelScoreFunc(model)
	}
	return scores
}
