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

package datalayer

import "testing"

// Verifies non-nil model
// Verifies name is preserved
// Verifies attributes are initialized
func TestNewModel(t *testing.T) {
	m := NewModel("test-model")

	if m == nil {
		t.Fatal("expected model to be non-nil")
	}
	if got := m.GetName(); got != "test-model" {
		t.Fatalf("expected model name %q, got %q", "test-model", got)
	}
	if m.GetAttributes() == nil {
		t.Fatal("expected model attributes to be initialized")
	}
}
