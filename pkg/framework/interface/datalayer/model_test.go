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
	"bytes"
	"encoding/json"
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

// TestModel_MarshalJSON pins the load-bearing contract: a Model value
// must serialize to a JSON object exposing its name, not {}. Regression
// guard for the observability bug where every picker/pipeline log line
// rendered TargetModel as {} because encoding/json cannot see unexported
// fields.
func TestModel_MarshalJSON(t *testing.T) {
	m := NewModel("test-model")

	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("json.Marshal(model): %v", err)
	}

	const want = `{"name":"test-model"}`
	if string(b) != want {
		t.Fatalf("got %s, want %s", b, want)
	}
}

// TestModel_MarshalJSON_NestedInStruct exercises the actual failure
// mode from the field: the Model nested inside a larger struct (as it
// appears in PipelineRunResult / ScoredModel logs). Asserts the nested
// object is not {}.
func TestModel_MarshalJSON_NestedInStruct(t *testing.T) {
	wrapper := struct {
		TargetModel Model `json:"TargetModel"`
	}{TargetModel: NewModel("nested-model")}

	b, err := json.Marshal(wrapper)
	if err != nil {
		t.Fatalf("json.Marshal(wrapper): %v", err)
	}

	if bytes.Contains(b, []byte(`"TargetModel":{}`)) {
		t.Fatalf("Model rendered as {} when nested in a struct — MarshalJSON not being called; raw=%s", b)
	}
	if !bytes.Contains(b, []byte(`"name":"nested-model"`)) {
		t.Fatalf("expected name %q in nested JSON, raw=%s", "nested-model", b)
	}
}
