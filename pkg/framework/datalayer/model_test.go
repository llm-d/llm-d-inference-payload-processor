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

import (
	"strings"
	"testing"
)

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

// Verifies success case: key is found and value is of expected type.
// Verifies error case: key is not found.
// Verifies error case: value is not of expected type.
func TestReadModelKey(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		m := NewModel("test-model")
		expected := testCloneableValue{Value: 42}
		m.GetAttributes().Put("score", expected)

		got, err := ReadModelKey[testCloneableValue](m, "score")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if got != expected {
			t.Fatalf("expected value %+v, got %+v", expected, got)
		}
	})

	t.Run("missing key", func(t *testing.T) {
		m := NewModel("test-model")

		got, err := ReadModelKey[testCloneableValue](m, "missing")
		if err == nil {
			t.Fatal("expected error for missing key")
		}
		if got != (testCloneableValue{}) {
			t.Fatalf("expected zero value, got %+v", got)
		}
		if !strings.Contains(err.Error(), `attribute "missing": not found`) {
			t.Fatalf("expected not found error, got %v", err)
		}
	})

	t.Run("wrong type", func(t *testing.T) {
		m := NewModel("test-model")
		m.GetAttributes().Put("score", testCloneableValue{Value: 42})

		got, err := ReadModelKey[*testCloneableValue](m, "score")
		if err == nil {
			t.Fatal("expected error for wrong type")
		}
		if got != nil {
			t.Fatalf("expected zero value nil, got %+v", got)
		}
		if !strings.Contains(err.Error(), `unexpected type for key "score"`) {
			t.Fatalf("expected type mismatch error, got %v", err)
		}
	})
}
