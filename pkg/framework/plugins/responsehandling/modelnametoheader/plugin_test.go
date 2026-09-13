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

package modelnametoheader

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/requesthandling/modelselector"
)

func TestProcessResponseHeaders_SetsHeaderFromRequestAttribute(t *testing.T) {
	p, err := PluginFactory("test", nil, nil)
	if err != nil {
		t.Fatalf("PluginFactory failed: %v", err)
	}

	request := requesthandling.NewInferenceRequest()
	request.SetAttribute(modelselector.SelectedModelAttributeKey, "llama-70b")

	response := requesthandling.NewInferenceResponse()

	rhp := p.(requesthandling.ResponseHeadersProcessor)
	if err := rhp.ProcessResponseHeaders(context.Background(), request, response); err != nil {
		t.Fatalf("ProcessResponseHeaders failed: %v", err)
	}

	got := response.MutatedHeaders()[ModelNameHeader]
	if got != "llama-70b" {
		t.Errorf("expected header %q = %q, got %q", ModelNameHeader, "llama-70b", got)
	}
}

func TestProcessResponseHeaders_NoOpWithoutRequestAttribute(t *testing.T) {
	p, err := PluginFactory("test", nil, nil)
	if err != nil {
		t.Fatalf("PluginFactory failed: %v", err)
	}

	request := requesthandling.NewInferenceRequest()
	response := requesthandling.NewInferenceResponse()

	rhp := p.(requesthandling.ResponseHeadersProcessor)
	if err := rhp.ProcessResponseHeaders(context.Background(), request, response); err != nil {
		t.Fatalf("ProcessResponseHeaders failed: %v", err)
	}

	if len(response.MutatedHeaders()) != 0 {
		t.Errorf("expected no mutated headers, got %v", response.MutatedHeaders())
	}
}

func TestProcessResponseHeaders_NoOpOnWrongType(t *testing.T) {
	p, err := PluginFactory("test", nil, nil)
	if err != nil {
		t.Fatalf("PluginFactory failed: %v", err)
	}

	request := requesthandling.NewInferenceRequest()
	request.SetAttribute(modelselector.SelectedModelAttributeKey, 12345) // wrong type

	response := requesthandling.NewInferenceResponse()

	rhp := p.(requesthandling.ResponseHeadersProcessor)
	if err := rhp.ProcessResponseHeaders(context.Background(), request, response); err != nil {
		t.Fatalf("ProcessResponseHeaders should not error on wrong type: %v", err)
	}

	if len(response.MutatedHeaders()) != 0 {
		t.Errorf("expected no mutated headers for wrong type, got %v", response.MutatedHeaders())
	}
}

func TestPluginFactory_DefaultHeaderName(t *testing.T) {
	p, err := PluginFactory("test-instance", nil, nil)
	if err != nil {
		t.Fatalf("PluginFactory failed: %v", err)
	}
	if p.TypedName().Type != PluginType {
		t.Errorf("expected type %q, got %q", PluginType, p.TypedName().Type)
	}
	if p.TypedName().Name != "test-instance" {
		t.Errorf("expected name %q, got %q", "test-instance", p.TypedName().Name)
	}

	mnp := p.(*ModelNameToHeaderPlugin)
	if mnp.headerName != ModelNameHeader {
		t.Errorf("expected default header %q, got %q", ModelNameHeader, mnp.headerName)
	}
}

func TestPluginFactory_CustomHeaderName(t *testing.T) {
	params := plugin.StrictDecoder(json.RawMessage(`{"headerName": "X-Custom-Model"}`))
	p, err := PluginFactory("custom", params, nil)
	if err != nil {
		t.Fatalf("PluginFactory failed: %v", err)
	}

	mnp := p.(*ModelNameToHeaderPlugin)
	if mnp.headerName != "X-Custom-Model" {
		t.Errorf("expected header %q, got %q", "X-Custom-Model", mnp.headerName)
	}

	request := requesthandling.NewInferenceRequest()
	request.SetAttribute(modelselector.SelectedModelAttributeKey, "gpt-4")

	response := requesthandling.NewInferenceResponse()

	rhp := p.(requesthandling.ResponseHeadersProcessor)
	if err := rhp.ProcessResponseHeaders(context.Background(), request, response); err != nil {
		t.Fatalf("ProcessResponseHeaders failed: %v", err)
	}

	got := response.MutatedHeaders()["X-Custom-Model"]
	if got != "gpt-4" {
		t.Errorf("expected header %q = %q, got %q", "X-Custom-Model", "gpt-4", got)
	}
}

func TestPluginFactory_InvalidConfig(t *testing.T) {
	params := plugin.StrictDecoder(json.RawMessage(`{invalid`))
	_, err := PluginFactory("bad", params, nil)
	if err == nil {
		t.Fatal("expected error for invalid JSON config, got nil")
	}
}
