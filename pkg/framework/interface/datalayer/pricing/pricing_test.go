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

package pricing

import "testing"

// TestTokenPricesClone verifies that Clone returns an independent *TokenPrices
// carrying the same field values, and that mutating either field on the clone
// does not affect the original.
func TestTokenPricesClone(t *testing.T) {
	original := &TokenPrices{InputTokenPrice: 1.5, OutputTokenPrice: 4.5}
	cloned := original.Clone()

	c, ok := cloned.(*TokenPrices)
	if !ok {
		t.Fatal("Clone() did not return *TokenPrices type")
	}
	if c.InputTokenPrice != original.InputTokenPrice || c.OutputTokenPrice != original.OutputTokenPrice {
		t.Errorf("Clone() = %+v, want %+v", c, original)
	}

	c.InputTokenPrice = 100.0
	c.OutputTokenPrice = 200.0
	if original.InputTokenPrice == 100.0 || original.OutputTokenPrice == 200.0 {
		t.Errorf("Clone() did not create an independent copy: original mutated to %+v", original)
	}
}

// TestToTokenPrices_ZeroValue verifies that a zero-valued ModelPriceShape produces
// a zero-valued TokenPrices ("free model"), which is the invariant downstream consumers
// rely on when an operator omits the pricing block from a model entry.
func TestToTokenPrices_ZeroValue(t *testing.T) {
	tp := ToTokenPrices(ModelPriceShape{})
	if tp == nil {
		t.Fatal("ToTokenPrices(ModelPriceShape{}) returned nil; want zero-valued *TokenPrices")
	}
	if tp.InputTokenPrice != 0 || tp.OutputTokenPrice != 0 {
		t.Errorf("ToTokenPrices(ModelPriceShape{}) = %+v, want {0, 0}", tp)
	}
}
