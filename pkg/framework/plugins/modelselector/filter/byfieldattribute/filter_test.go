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

package byfieldattribute

import (
	"context"
	"encoding/json"
	"sort"
	"testing"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
)

// candidateModels builds datalayer models; attrs optionally maps a model name
// to a byField attribute name/value pair to set on it.
func candidateModels(attrs map[string][2]string, names ...string) []datalayer.Model {
	models := make([]datalayer.Model, len(names))
	for idx, name := range names {
		m := datalayer.NewModel(name)
		if kv, ok := attrs[name]; ok {
			m.GetAttributes().Put(kv[0], StringAttribute(kv[1]))
		}
		models[idx] = m
	}
	return models
}

func modelNames(models []datalayer.Model) []string {
	out := make([]string, len(models))
	for idx, model := range models {
		out[idx] = model.GetName()
	}
	sort.Strings(out)
	return out
}

// requestWithField builds a request whose body holds value under field;
// nil leaves the field absent.
func requestWithField(field string, value any) *requesthandling.InferenceRequest {
	r := requesthandling.NewInferenceRequest()
	if value != nil {
		r.Body[field] = value
	}
	return r
}

func TestModelNameFilterFactory(t *testing.T) {
	t.Run("ignores parameters", func(t *testing.T) {
		p, err := ModelNameFilterFactory("my-filter", json.RawMessage(`{"fieldName":"anthropic_version","byField":"id"}`), nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		f := p.(*ByFieldFilter)
		if got := f.TypedName().Name; got != "my-filter" {
			t.Errorf("Name = %s, want my-filter", got)
		}
		if got := f.TypedName().Type; got != ModelNameFilterType {
			t.Errorf("Type = %s, want %s", got, ModelNameFilterType)
		}
		if f.fieldName != requestModelField {
			t.Errorf("fieldName = %s, want %s", f.fieldName, requestModelField)
		}
		if f.byField != defaultByFieldValue {
			t.Errorf("byField = %q, want default", f.byField)
		}
	})

	t.Run("nil parameters", func(t *testing.T) {
		p, err := ModelNameFilterFactory("my-filter", nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := p.(*ByFieldFilter).TypedName().Type; got != ModelNameFilterType {
			t.Errorf("Type = %s, want %s", got, ModelNameFilterType)
		}
	})
}

func TestByFieldFilterFactory(t *testing.T) {
	tests := []struct {
		name       string
		params     json.RawMessage
		wantErr    bool
		wantField  string
		wantByAttr string
	}{
		{
			name:      "fieldName only",
			params:    json.RawMessage(`{"fieldName":"anthropic_version"}`),
			wantField: "anthropic_version",
		},
		{
			name:       "fieldName with byField override",
			params:     json.RawMessage(`{"fieldName":"anthropic_version","byField":"apiVersion"}`),
			wantField:  "anthropic_version",
			wantByAttr: "apiVersion",
		},
		{
			name:       "fieldName with empty byField",
			params:     json.RawMessage(`{"fieldName":"model","byField":""}`),
			wantField:  "model",
			wantByAttr: "",
		},
		{
			name:    "missing fieldName",
			params:  json.RawMessage(`{}`),
			wantErr: true,
		},
		{
			name:    "nil parameters",
			params:  nil,
			wantErr: true,
		},
		{
			name:    "malformed JSON",
			params:  json.RawMessage(`{"fieldName":`),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := ByFieldFilterFactory("my-filter", tt.params, nil)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			f := p.(*ByFieldFilter)
			if got := f.TypedName().Type; got != ByFieldFilterType {
				t.Errorf("Type = %s, want %s", got, ByFieldFilterType)
			}
			if f.fieldName != tt.wantField {
				t.Errorf("fieldName = %s, want %s", f.fieldName, tt.wantField)
			}
			if f.byField != tt.wantByAttr {
				t.Errorf("byField = %q, want %q", f.byField, tt.wantByAttr)
			}
		})
	}
}

func TestModelNameFilter_Filter(t *testing.T) {
	all := []string{"qwen3-8b", "qwen3-32b", "llama3-8b"}

	tests := []struct {
		name      string
		modelBody any
		want      []string
	}{
		{name: "missing model field passes all through", modelBody: nil, want: all},
		{name: "empty string passes all through", modelBody: "", want: all},
		{name: "matching model name returns single candidate", modelBody: "qwen3-8b", want: []string{"qwen3-8b"}},
		{name: "unregistered model name yields empty", modelBody: "gpt-4", want: []string{}},
		{name: "non-string model field yields empty (malformed)", modelBody: 42, want: []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := NewModelNameFilter()
			req := requestWithField(requestModelField, tt.modelBody)

			got := modelNames(f.Filter(context.Background(), nil, req, candidateModels(nil, all...)))
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

func TestByFieldFilter_Filter_ByName(t *testing.T) {
	f := NewByFieldFilter("model", "")
	candidates := candidateModels(nil, "qwen3-8b", "qwen3-32b")

	req := requestWithField("model", "qwen3-32b")
	got := modelNames(f.Filter(context.Background(), nil, req, candidates))
	want := []string{"qwen3-32b"}

	if len(got) != len(want) || got[0] != want[0] {
		t.Errorf("Filter() = %v, want %v", got, want)
	}
}

func TestByFieldFilter_Filter_ByAttribute(t *testing.T) {
	attrs := map[string][2]string{
		"claude-bedrock-a": {"anthropicVersion", "bedrock-2023-05-31"},
		"claude-bedrock-b": {"anthropicVersion", "bedrock-2023-05-31"},
		"claude-legacy":    {"anthropicVersion", "bedrock-2022-11-01"},
		// "gpt-oss" has no anthropicVersion attribute.
	}
	candidates := candidateModels(attrs, "claude-bedrock-a", "claude-bedrock-b", "claude-legacy", "gpt-oss")

	f := NewByFieldFilter("anthropic_version", "anthropicVersion")

	t.Run("matches all candidates sharing the requested version", func(t *testing.T) {
		req := requestWithField("anthropic_version", "bedrock-2023-05-31")
		got := modelNames(f.Filter(context.Background(), nil, req, candidates))
		want := []string{"claude-bedrock-a", "claude-bedrock-b"}
		sort.Strings(want)
		if len(got) != len(want) {
			t.Fatalf("Filter() = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("Filter() = %v, want %v", got, want)
			}
		}
	})

	t.Run("candidate missing the attribute is skipped, not erroring", func(t *testing.T) {
		req := requestWithField("anthropic_version", "bedrock-2022-11-01")
		got := modelNames(f.Filter(context.Background(), nil, req, candidates))
		want := []string{"claude-legacy"}
		if len(got) != len(want) || got[0] != want[0] {
			t.Errorf("Filter() = %v, want %v", got, want)
		}
	})

	t.Run("no requested field keeps all candidates including those missing the attribute", func(t *testing.T) {
		req := requestWithField("anthropic_version", nil)
		got := modelNames(f.Filter(context.Background(), nil, req, candidates))
		if len(got) != len(candidates) {
			t.Errorf("Filter() = %v, want all %d candidates", got, len(candidates))
		}
	})

	t.Run("unmatched version yields empty", func(t *testing.T) {
		req := requestWithField("anthropic_version", "bedrock-1999-01-01")
		got := modelNames(f.Filter(context.Background(), nil, req, candidates))
		if len(got) != 0 {
			t.Errorf("Filter() = %v, want empty", got)
		}
	})

	t.Run("non-string request field yields empty (malformed)", func(t *testing.T) {
		req := requestWithField("anthropic_version", 42)
		got := modelNames(f.Filter(context.Background(), nil, req, candidates))
		if len(got) != 0 {
			t.Errorf("Filter() = %v, want empty", got)
		}
	})
}
