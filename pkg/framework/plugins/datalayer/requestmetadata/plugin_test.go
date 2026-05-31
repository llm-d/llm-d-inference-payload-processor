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

package requestmetadata

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/datastore"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	dlsrc "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer/datasource"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	ctrlbuilder "sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// fakeHandle implements plugin.Handle for unit tests, providing only a Datastore.
type fakeHandle struct{ ds datalayer.Datastore }

func (f *fakeHandle) Context() context.Context                         { return context.Background() }
func (f *fakeHandle) Client() client.Client                            { return nil }
func (f *fakeHandle) ReconcilerBuilder() *ctrlbuilder.Builder          { return nil }
func (f *fakeHandle) Datastore() datalayer.Datastore                   { return f.ds }
func (f *fakeHandle) Plugin(name string) plugin.Plugin                 { return nil }
func (f *fakeHandle) AddPlugin(name string, plugin plugin.Plugin)      {}
func (f *fakeHandle) GetAllPlugins() []plugin.Plugin                   { return nil }
func (f *fakeHandle) GetAllPluginsWithNames() map[string]plugin.Plugin { return nil }

// makeRequestEvent creates a RequestEventType event with model and max_tokens.
func makeRequestEvent(model string, maxTokens float64) dlsrc.Event {
	req := requesthandling.NewInferenceRequest()
	req.Body["model"] = model
	req.Body["max_tokens"] = maxTokens
	return dlsrc.Event{
		Type:    dlsrc.RequestEventType,
		Payload: dlsrc.RequestPayload{Request: req},
	}
}

// makeResponseEvent creates a ResponseEventType event with model, duration, and max_tokens.
// maxTokens mirrors the original request's max_tokens so the extractor can decrement correctly.
func makeResponseEvent(model string, durationMs int, maxTokens float64) dlsrc.Event {
	req := requesthandling.NewInferenceRequest()
	req.Body["model"] = model
	req.Body["max_tokens"] = maxTokens
	return dlsrc.Event{
		Type: dlsrc.ResponseEventType,
		Payload: dlsrc.ResponsePayload{
			Request:  req,
			Response: requesthandling.NewInferenceResponse(),
			Duration: time.Duration(durationMs) * time.Millisecond,
		},
	}
}

// getInflightRequests asserts the inflight-requests attribute exists for model and returns it.
func getRequestMetadata(t testing.TB, ds datalayer.Datastore, model string) RequestMetadataCount {
	t.Helper()
	val, ok := ds.GetOrCreateModel(model).GetAttributes().Get(RequestMetadataAttributeKey)
	if !ok {
		t.Fatalf("expected %q attribute for model %q", RequestMetadataAttributeKey, model)
	}
	rc, ok := val.(RequestMetadataCount)
	if !ok {
		t.Fatalf("expected RequestMetadataCount for model %q", model)
	}
	return rc
}

func newRequestMetadataTest(t *testing.T) (*RequestMetadataExtractor, datalayer.Datastore) {
	t.Helper()
	ds := datastore.NewFakeDataStore()
	return NewRequestMetadataExtractor(ds), ds
}

func TestRequestIncrementsCounter(t *testing.T) {
	ext, ds := newRequestMetadataTest(t)

	batch := []dlsrc.Event{makeRequestEvent("m1", 100)}
	if err := ext.Extract(context.Background(), batch); err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	rc := getRequestMetadata(t, ds, "m1")
	if rc.Requests != 1 {
		t.Errorf("expected Requests=1, got %d", rc.Requests)
	}
	if rc.Tokens != 100 {
		t.Errorf("expected Tokens=100, got %d", rc.Tokens)
	}
}

func TestResponseDecrementsCounter(t *testing.T) {
	ext, ds := newRequestMetadataTest(t)

	// Response carries the original request's max_tokens so the extractor can decrement correctly.
	batch := []dlsrc.Event{
		makeRequestEvent("m1", 100),
		makeResponseEvent("m1", 50, 100),
	}
	if err := ext.Extract(context.Background(), batch); err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	rc := getRequestMetadata(t, ds, "m1")
	if rc.Requests != 0 {
		t.Errorf("expected Requests=0, got %d", rc.Requests)
	}
	if rc.Tokens != 0 {
		t.Errorf("expected Tokens=0, got %d", rc.Tokens)
	}
}

func TestCounterFloorsAtZero(t *testing.T) {
	ext, ds := newRequestMetadataTest(t)

	// Response with no prior request — both counters must floor at zero.
	batch := []dlsrc.Event{makeResponseEvent("m1", 50, 100)}
	if err := ext.Extract(context.Background(), batch); err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	rc := getRequestMetadata(t, ds, "m1")
	if rc.Requests != 0 {
		t.Errorf("expected Requests=0, got %d", rc.Requests)
	}
	if rc.Tokens != 0 {
		t.Errorf("expected Tokens=0, got %d", rc.Tokens)
	}
}

func TestRequestMetadataMultipleModels(t *testing.T) {
	ext, ds := newRequestMetadataTest(t)

	batch := []dlsrc.Event{
		makeRequestEvent("m1", 10),
		makeRequestEvent("m2", 20),
	}
	if err := ext.Extract(context.Background(), batch); err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	rc1 := getRequestMetadata(t, ds, "m1")
	if rc1.Requests != 1 || rc1.Tokens != 10 {
		t.Errorf("m1: expected {Requests:1, Tokens:10}, got %+v", rc1)
	}

	rc2 := getRequestMetadata(t, ds, "m2")
	if rc2.Requests != 1 || rc2.Tokens != 20 {
		t.Errorf("m2: expected {Requests:1, Tokens:20}, got %+v", rc2)
	}
}

func TestRequestMetadataUnknownEventTypeIgnored(t *testing.T) {
	ext, ds := newRequestMetadataTest(t)

	batch := []dlsrc.Event{{Type: "unknown"}}
	if err := ext.Extract(context.Background(), batch); err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	modelCount := len(ds.Models())
	if modelCount != 0 {
		t.Errorf("expected no models in datastore, got %d", modelCount)
	}
}

func TestRequestMetadataMissingModelFieldIgnored(t *testing.T) {
	ext, ds := newRequestMetadataTest(t)

	// Payload without a "model" key — no counter should be updated.
	req := requesthandling.NewInferenceRequest()
	req.Body["max_tokens"] = float64(50)
	batch := []dlsrc.Event{
		{Type: dlsrc.RequestEventType, Payload: dlsrc.RequestPayload{Request: req}},
	}
	if err := ext.Extract(context.Background(), batch); err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	modelCount := len(ds.Models())
	if modelCount != 0 {
		t.Errorf("expected no models in datastore, got %d", modelCount)
	}
}

func TestExtractorFactoryWiresDatastore(t *testing.T) {
	ds := datastore.NewFakeDataStore()
	h := &fakeHandle{ds: ds}

	p, err := ExtractorFactory("my-extractor", json.RawMessage(`{}`), h)
	if err != nil {
		t.Fatalf("ExtractorFactory returned error: %v", err)
	}

	ext, ok := p.(*RequestMetadataExtractor)
	if !ok {
		t.Fatalf("expected *RequestMetadataExtractor, got %T", p)
	}
	if ext.ds != ds {
		t.Error("factory did not wire the datastore from the handle")
	}
	if ext.TypedName().Name != "my-extractor" {
		t.Errorf("expected name %q, got %q", "my-extractor", ext.TypedName().Name)
	}
}
