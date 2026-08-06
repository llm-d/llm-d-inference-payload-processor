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
	"sort"
	"testing"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer/modelgroups"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
)

// candidateModels builds the datalayer models handed to the filter. membership
// optionally maps a model name to the group names it belongs to (mirroring what
// the model-config-datasource plugin would have written to the model's
// AttributeMap); a model name absent from membership has no groups attribute.
func candidateModels(membership map[string][]string, names ...string) []datalayer.Model {
	models := make([]datalayer.Model, len(names))
	for idx, name := range names {
		m := datalayer.NewModel(name)
		if groups, ok := membership[name]; ok {
			m.GetAttributes().Put(modelgroups.GroupsAttributeKey, modelgroups.Groups(groups))
		}
		models[idx] = m
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

// TestModelGroupFilterFactory verifies that the factory (which takes no
// parameters — group membership comes from the datalayer, not plugin config)
// carries the right type and instance name.
func TestModelGroupFilterFactory(t *testing.T) {
	p, err := ModelGroupFilterFactory("my-filter", nil, nil)
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

// TestModelGroupFilter_NoGroupsConfigured verifies filtering behavior when no
// candidate model carries any group attribute: an "auto/<group-name>" selector
// always fails (there is nothing to match), while an explicit, available model
// name still succeeds via the exact-match path.
func TestModelGroupFilter_NoGroupsConfigured(t *testing.T) {
	f := NewModelGroupFilter()
	candidates := candidateModels(nil, "qwen3-8b", "qwen3-32b")

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

// TestModelGroupFilter_Filter covers all behavioural cases described in the README.
// Group membership is now attached directly to each candidate model's
// modelgroups.GroupsAttributeKey attribute (as the model-config-datasource plugin
// would populate it), rather than passed as filter constructor parameters.
func TestModelGroupFilter_Filter(t *testing.T) {
	membership := map[string][]string{
		"qwen3-8b":   {"qwen3models"},
		"qwen3-32b":  {"qwen3models"},
		"llama3-8b":  {"llama3"},
		"llama3-70b": {"llama3"},
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
			f := NewModelGroupFilter()
			req := requestWithModel(tt.modelBody)

			got := modelNames(f.Filter(context.Background(), nil, req, candidateModels(membership, all...)))
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
	// Candidate set excludes every model in the qwen3models group.
	candidates := candidateModels(nil, "mistral-7b")

	f := NewModelGroupFilter()
	req := requestWithModel("auto/qwen3models")

	got := modelNames(f.Filter(context.Background(), nil, req, candidates))
	if len(got) != 0 {
		t.Errorf("Filter() = %v, want empty", got)
	}
}

// TestModelGroupFilter_PartialGroupInCandidates verifies that when only a
// subset of a group's models are present in the candidate list, only those
// present are returned.
func TestModelGroupFilter_PartialGroupInCandidates(t *testing.T) {
	membership := map[string][]string{
		"qwen3-8b":  {"qwen3models"},
		"qwen3-72b": {"qwen3models"},
	}
	// Only qwen3-8b and qwen3-72b are in the data layer; qwen3-32b is absent.
	candidates := candidateModels(membership, "qwen3-8b", "qwen3-72b", "mistral-7b")

	f := NewModelGroupFilter()
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

// TestModelGroupFilter_ModelInMultipleGroups verifies that a model carrying
// more than one group in its attribute matches on any of them.
func TestModelGroupFilter_ModelInMultipleGroups(t *testing.T) {
	membership := map[string][]string{
		"qwen3-32b": {"qwen3models", "large-models"},
	}
	candidates := candidateModels(membership, "qwen3-32b")

	f := NewModelGroupFilter()

	for _, group := range []string{"qwen3models", "large-models"} {
		req := requestWithModel("auto/" + group)
		got := modelNames(f.Filter(context.Background(), nil, req, candidates))
		if len(got) != 1 || got[0] != "qwen3-32b" {
			t.Errorf("Filter() for group %q = %v, want [qwen3-32b]", group, got)
		}
	}
}
