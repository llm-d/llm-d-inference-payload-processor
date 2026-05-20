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

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/modelselector"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
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

// CostScorerFactory defines the factory function for the CostScorer scorer
func CostScorerFactory(name string, _ json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
	return NewCostScorer().WithName(name), nil
}

// NewCostScorer creates a new lowest price scorer
func NewCostScorer() *CostScorer {
	return &CostScorer{
		typedName: plugin.TypedName{Type: CostScorerType, Name: CostScorerType},
	}
}

// CostScorer scorer that scores models based on their price
// Lower-priced models receive higher scores
type CostScorer struct {
	typedName plugin.TypedName
}

// TypedName returns the typed name of the plugin.
func (s *CostScorer) TypedName() plugin.TypedName {
	return s.typedName
}

// WithName sets the name of the plugin.
func (s *CostScorer) WithName(name string) *CostScorer {
	s.typedName.Name = name
	return s
}

// Score scores the given models in range of [0.0-1.0] based on their price using inverted sum normalization.
// Scoring behavior:
//   - Lower-priced models receive higher scores
//   - Score formula: 1.0 - price / sum(prices)
//   - Higher score indicates better (cheaper) model
//   - If only one model, it receives neutral score 0.5
//   - If all models have zero price, each receives score 1.0
func (s *CostScorer) Score(_ context.Context, _ *plugin.CycleState, _ *requesthandling.InferenceRequest, models []datalayer.Model) map[datalayer.Model]float64 {
	// Create a map to hold the score of each model candidate
	scores := make(map[datalayer.Model]float64, len(models))

	// Special case: single model gets neutral score
	if len(models) == 1 {
		scores[models[0]] = 0.5
		return scores
	}

	// Calculate the sum of all prices
	var sumPrices float64
	for _, model := range models {
		priceValue, _ := model.GetAttributes().Get(PriceAttributeKey)
		price := priceValue.(*PriceValue).Value
		sumPrices += price
	}

	// If sum is zero (all prices are zero), all models are free - assign perfect score
	if sumPrices == 0 {
		for _, model := range models {
			scores[model] = 1.0
		}
		return scores
	}

	// Calculate scores using inverted sum normalization: 1 - price/sum(prices)
	for _, model := range models {
		priceValue, _ := model.GetAttributes().Get(PriceAttributeKey)
		price := priceValue.(*PriceValue).Value
		scores[model] = 1.0 - price/sumPrices
	}

	return scores
}
