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

// Package pricing defines the shared types and the attribute key used to attach
// per-token pricing information to a Model in the datalayer.
//
// The package owns two representations:
//
//   - ModelPriceShape — the on-disk JSON DTO consumed by configuration loaders.
//     Prices are expressed in USD per 1,000,000 tokens (the unit operators write).
//   - TokenPrices    — the in-memory cloneable stored on a Model's AttributeMap
//     under TokenPricesAttributeKey. Prices are in USD per single token.
//
// ToTokenPrices bridges the two, dividing the per-million values by 1e6.
// Producers (config loaders) and consumers (cost-aware scorers) both reference
// these types so that the storage contract has a single source of truth.
package pricing

import "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"

// TokenPricesAttributeKey is the AttributeMap key under which a model's
// *TokenPrices is stored. A registered Model is expected to have this attribute
// populated (a free model uses a zero-valued TokenPrices), so consumers may read
// the key unconditionally.
const TokenPricesAttributeKey = "token_prices"

// ModelPriceShape is the on-disk pricing block: prices in USD per 1,000,000 tokens.
// It is the JSON DTO consumed by configuration loaders and is NOT stored on the
// Model directly — convert to a *TokenPrices via ToTokenPrices first.
// ModelPriceShape and its reciprocal TokenPrices are intentionally minimalistic.
// A more elaborate struct might be introduced in the future
// to reflect caching, batching, volume, tiering, etc. discounts
type ModelPriceShape struct {
	InputPerMillion  float64 `json:"input_per_million"`
	OutputPerMillion float64 `json:"output_per_million"`
}

// TokenPrices bundles the per-token input and output prices for a model. It is
// stored in the Model's AttributeMap by TokenPricesAttributeKey.
type TokenPrices struct {
	InputTokenPrice  float64
	OutputTokenPrice float64
}

// Clone implements datalayer.Cloneable.
func (p *TokenPrices) Clone() datalayer.Cloneable {
	return &TokenPrices{InputTokenPrice: p.InputTokenPrice, OutputTokenPrice: p.OutputTokenPrice}
}

// ToTokenPrices converts a ModelPriceShape (per-million-tokens DTO) into the
// in-memory *TokenPrices (per-token). A zero-valued shape produces a free model
// (both fields 0).
func ToTokenPrices(s ModelPriceShape) *TokenPrices {
	return &TokenPrices{
		InputTokenPrice:  s.InputPerMillion / 1e6,
		OutputTokenPrice: s.OutputPerMillion / 1e6,
	}
}
