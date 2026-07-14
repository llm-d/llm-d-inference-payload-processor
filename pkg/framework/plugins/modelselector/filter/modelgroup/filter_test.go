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

package modelgroup

import (
	"context"
	"encoding/json"
	"sort"
	"testing"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
)

// candidateModels builds the datalayer models handed to the filter.
func candidateModels(names ...string) []datalayer.Model {
	models := make([]datalayer.Model, len(names))
	for idx, name := range names {
		models[idx] = datalayer.NewModel(name)
	}
	return models
}

// modelNames extracts the sorted model names of a filter result, for
// order-insensitive comparison against the expected names.
func modelNames(models []datalayer.Model) []string {
	out := make([]string, len(models))
	for idx, model := range models {
		out[idx] = model.GetName()
	}
	sort.Strings(out)
	return out
}

// requestWithModel builds an inference request whose body holds the given
// value under the "model" field; a nil value leaves the field absent.
func requestWithModel(value any) *requesthandling.InferenceRequest {
	r := requesthandling.NewInferenceRequest()
	if value != nil {
		r.Body[requestModelField] = value
	}
	return r
}

// TestModelGroupFilterFactory verifies that the factory parses parameters
// correctly and carries the right type and instance name.
func TestModelGroupFilterFactory(t *testing.T) {
	params := json.RawMessage(`{"qwen3models": ["qwen3-8b", "qwen3-32b"]}`)
	p, err := ModelGroupFilterFactory("my-filter", params, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f := p.(*ModelGroupFilter)
	if got := f.TypedName().Name; got != "my-filter" {
		t.Errorf("Name = %s, want my-filter", got)
	}
	if got := f.TypedName().Type; got != ModelGroupFilterType {
		t.Errorf("Type = %s, want %s", got, ModelGroupFilterType)
	}
}

// TestModelGroupFilterFactory_InvalidJSON verifies that malformed parameters
// cause the factory to return an error.
func TestModelGroupFilterFactory_InvalidJSON(t *testing.T) {
	_, err := ModelGroupFilterFactory("f", json.RawMessage(`{invalid`), nil)
	if err == nil {
		t.Fatal("expected error for invalid JSON params, got nil")
	}
}

// TestModelGroupFilterFactory_EmptyParams verifies that an empty/nil params
// payload creates a filter with no groups (pass-all on "auto").
func TestModelGroupFilterFactory_EmptyParams(t *testing.T) {
	p, err := ModelGroupFilterFactory("f", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil plugin")
	}
}

// TestModelGroupFilter_NoGroupsConfigured verifies filtering behavior when no
// groups are configured at all: an "auto/<group-name>" selector always fails
// (there is nothing to match), while an explicit, available model name still
// succeeds via the exact-match path.
func TestModelGroupFilter_NoGroupsConfigured(t *testing.T) {
	f := NewModelGroupFilter(nil)
	candidates := candidateModels("qwen3-8b", "qwen3-32b")

	t.Run("auto/somegroup fails with no groups defined", func(t *testing.T) {
		req := requestWithModel("auto/somegroup")
		got := modelNames(f.Filter(context.Background(), nil, req, candidates))
		if len(got) != 0 {
			t.Errorf("Filter() = %v, want empty", got)
		}
	})

	t.Run("explicit valid model name succeeds with no groups defined", func(t *testing.T) {
		req := requestWithModel("qwen3-8b")
		got := modelNames(f.Filter(context.Background(), nil, req, candidates))
		want := []string{"qwen3-8b"}
		if len(got) != len(want) || got[0] != want[0] {
			t.Errorf("Filter() = %v, want %v", got, want)
		}
	})
}

// TestNewModelGroupFilter_SkipsInvalidGroups verifies that groups with an
// empty name, an empty model list, or an empty model name in the list are
// skipped, while valid groups configured alongside them still load.
func TestNewModelGroupFilter_SkipsInvalidGroups(t *testing.T) {
	groups := map[string][]string{
		"valid":        {"qwen3-8b", "qwen3-32b"},
		"":             {"llama3-8b"},
		"empty-models": {},
		"blank-model":  {"llama3-8b", ""},
	}

	f := NewModelGroupFilter(groups)

	if _, ok := f.groups["valid"]; !ok {
		t.Error("expected valid group to be loaded")
	}
	if _, ok := f.groups[""]; ok {
		t.Error("expected group with empty name to be skipped")
	}
	if _, ok := f.groups["empty-models"]; ok {
		t.Error("expected group with empty model list to be skipped")
	}
	if _, ok := f.groups["blank-model"]; ok {
		t.Error("expected group with a blank model name to be skipped")
	}
	if len(f.groups) != 1 {
		t.Errorf("expected exactly 1 loaded group, got %d", len(f.groups))
	}
}

// TestModelGroupFilter_Filter covers all behavioural cases described in the README.
func TestModelGroupFilter_Filter(t *testing.T) {
	groups := map[string][]string{
		"qwen3models": {"qwen3-8b", "qwen3-32b"},
		"llama3":      {"llama3-8b", "llama3-70b"},
	}
	// All models available in the data layer.
	all := []string{"qwen3-8b", "qwen3-32b", "llama3-8b", "llama3-70b", "mistral-7b"}

	tests := []struct {
		name      string
		modelBody any
		want      []string
	}{
		// "auto/group-name" returns only models in that group that are also
		// present in the candidate list.
		{
			name:      "auto/qwen3models returns group members",
			modelBody: "auto/qwen3models",
			want:      []string{"qwen3-8b", "qwen3-32b"},
		},
		{
			name:      "auto/llama3 returns group members",
			modelBody: "auto/llama3",
			want:      []string{"llama3-8b", "llama3-70b"},
		},
		// Absent model field → all candidates pass through.
		{
			name:      "missing model field passes all through",
			modelBody: nil,
			want:      all,
		},
		// Empty string → all candidates pass through.
		{
			name:      "empty string passes all through",
			modelBody: "",
			want:      all,
		},
		// Bare "auto" (no slash) → all candidates pass through.
		{
			name:      "bare auto passes all through",
			modelBody: "auto",
			want:      all,
		},
		// "auto/" with no group name → empty result.
		{
			name:      "auto/ with empty group name yields empty",
			modelBody: "auto/",
			want:      []string{},
		},
		// Unknown group → empty result.
		{
			name:      "auto/unknown-group yields empty",
			modelBody: "auto/unknowngroup",
			want:      []string{},
		},
		// A plain model name without the "auto/" prefix that IS in the
		// candidate list is now matched explicitly, pinning the result to it.
		{
			name:      "registered plain model name is matched explicitly",
			modelBody: "qwen3-8b",
			want:      []string{"qwen3-8b"},
		},
		// A plain model name without the "auto/" prefix that is NOT in the
		// candidate list yields no candidates (pipeline rejects with 429).
		{
			name:      "unregistered plain model name yields empty",
			modelBody: "gpt-4",
			want:      []string{},
		},
		// Non-string type → malformed → empty result.
		{
			name:      "non-string model field yields empty (malformed)",
			modelBody: 42,
			want:      []string{},
		},
		// JSON array is not a string → malformed → empty result.
		{
			name:      "JSON array model field yields empty (malformed)",
			modelBody: []any{"qwen3-8b", "qwen3-32b"},
			want:      []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := NewModelGroupFilter(groups)
			req := requestWithModel(tt.modelBody)

			got := modelNames(f.Filter(context.Background(), nil, req, candidateModels(all...)))
			want := append([]string{}, tt.want...)
			sort.Strings(want)

			if len(got) != len(want) {
				t.Fatalf("Filter() = %v, want %v", got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("Filter() = %v, want %v", got, want)
					break
				}
			}
		})
	}
}

// TestModelGroupFilter_GroupModelsNotInCandidates verifies that when a
// requested group is known but none of its models appear in the candidate
// list, the filter returns no candidates.
func TestModelGroupFilter_GroupModelsNotInCandidates(t *testing.T) {
	groups := map[string][]string{
		"qwen3models": {"qwen3-8b", "qwen3-32b"},
	}
	// Candidate set excludes every model in the qwen3models group.
	candidates := candidateModels("mistral-7b")

	f := NewModelGroupFilter(groups)
	req := requestWithModel("auto/qwen3models")

	got := modelNames(f.Filter(context.Background(), nil, req, candidates))
	if len(got) != 0 {
		t.Errorf("Filter() = %v, want empty", got)
	}
}

// TestModelGroupFilter_PartialGroupInCandidates verifies that when only a
// subset of the group's models are present in the candidate list, only those
// present are returned.
func TestModelGroupFilter_PartialGroupInCandidates(t *testing.T) {
	groups := map[string][]string{
		"qwen3models": {"qwen3-8b", "qwen3-32b", "qwen3-72b"},
	}
	// Only qwen3-8b and qwen3-72b are in the data layer; qwen3-32b is absent.
	candidates := candidateModels("qwen3-8b", "qwen3-72b", "mistral-7b")

	f := NewModelGroupFilter(groups)
	req := requestWithModel("auto/qwen3models")

	got := modelNames(f.Filter(context.Background(), nil, req, candidates))
	want := []string{"qwen3-72b", "qwen3-8b"}
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("Filter() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Filter() = %v, want %v", got, want)
		}
	}
}
