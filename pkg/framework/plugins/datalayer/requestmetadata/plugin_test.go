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
func (f *fakeHandle) EventNotifier() datalayer.EventNotifier           { return nil }
func (f *fakeHandle) Plugin(name string) plugin.Plugin                 { return nil }
func (f *fakeHandle) AddPlugin(name string, plugin plugin.Plugin)      {}
func (f *fakeHandle) GetAllPlugins() []plugin.Plugin                   { return nil }
func (f *fakeHandle) GetAllPluginsWithNames() map[string]plugin.Plugin { return nil }

// makeRequestEvent creates a RequestEventType event with the given model.
func makeRequestEvent(model string) dlsrc.Event {
	req := requesthandling.NewInferenceRequest()
	req.Body["model"] = model
	return dlsrc.Event{
		Type:    dlsrc.RequestEventType,
		Payload: dlsrc.RequestPayload{Request: req},
	}
}

// makeResponseEvent creates a ResponseEventType event for model "m1".
func makeResponseEvent(durationMs int) dlsrc.Event {
	return makeResponseEventWithTTFT(durationMs, 0)
}

// makeResponseEventWithTTFT is like makeResponseEvent but sets the TTFT field.
func makeResponseEventWithTTFT(durationMs int, ttft time.Duration) dlsrc.Event {
	return makeResponseEventFull(durationMs, ttft, 0)
}

// makeResponseEventFull creates a ResponseEventType event for model "m1" with all fields.
func makeResponseEventFull(durationMs int, ttft time.Duration, completionTokens float64) dlsrc.Event {
	req := requesthandling.NewInferenceRequest()
	req.Body["model"] = "m1"
	resp := requesthandling.NewInferenceResponse()
	if completionTokens > 0 {
		resp.Body["usage"] = map[string]any{"completion_tokens": completionTokens}
	}
	return dlsrc.Event{
		Type: dlsrc.ResponseEventType,
		Payload: dlsrc.ResponsePayload{
			Request:  req,
			Response: resp,
			Duration: time.Duration(durationMs) * time.Millisecond,
			TTFT:     ttft,
		},
	}
}

// getInflightRequests asserts the inflight-requests attribute exists for model and returns it.
func getRequestMetadata(t testing.TB, ds datalayer.Datastore, model string) ModelMetrics {
	t.Helper()
	val, ok := ds.GetOrCreateModel(model).GetAttributes().Get(RequestMetadataAttributeKey)
	if !ok {
		t.Fatalf("expected %q attribute for model %q", RequestMetadataAttributeKey, model)
	}
	rc, ok := val.(ModelMetrics)
	if !ok {
		t.Fatalf("expected ModelMetrics for model %q", model)
	}
	return rc
}

func newRequestMetadataTest(t *testing.T) (*RequestMetadataExtractor, datalayer.Datastore) {
	t.Helper()
	ds := datastore.NewFakeDataStore()
	// intervalDuration=0 disables interval-based batching so EMA updates are immediate,
	// allowing unit tests to verify behaviour without advancing real time.
	return NewRequestMetadataExtractor(ds).WithIntervalDuration(0), ds
}

func TestRequestIncrementsCounter(t *testing.T) {
	ext, ds := newRequestMetadataTest(t)

	if err := ext.Extract(context.Background(), []dlsrc.Event{makeRequestEvent("m1")}); err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	rc := getRequestMetadata(t, ds, "m1")
	if rc.Requests != 1 {
		t.Errorf("expected Requests=1, got %d", rc.Requests)
	}
}

func TestResponseDecrementsCounter(t *testing.T) {
	ext, ds := newRequestMetadataTest(t)

	batch := []dlsrc.Event{
		makeRequestEvent("m1"),
		makeResponseEvent(0),
	}
	if err := ext.Extract(context.Background(), batch); err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	rc := getRequestMetadata(t, ds, "m1")
	if rc.Requests != 0 {
		t.Errorf("expected Requests=0, got %d", rc.Requests)
	}
}

func TestCounterFloorsAtZero(t *testing.T) {
	ext, ds := newRequestMetadataTest(t)

	// Response with no prior request — Requests must floor at zero.
	if err := ext.Extract(context.Background(), []dlsrc.Event{makeResponseEvent(0)}); err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	rc := getRequestMetadata(t, ds, "m1")
	if rc.Requests != 0 {
		t.Errorf("expected Requests=0, got %d", rc.Requests)
	}
}

func TestRequestMetadataMultipleModels(t *testing.T) {
	ext, ds := newRequestMetadataTest(t)

	batch := []dlsrc.Event{
		makeRequestEvent("m1"),
		makeRequestEvent("m2"),
	}
	if err := ext.Extract(context.Background(), batch); err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	rc1 := getRequestMetadata(t, ds, "m1")
	if rc1.Requests != 1 {
		t.Errorf("m1: expected Requests=1, got %d", rc1.Requests)
	}

	rc2 := getRequestMetadata(t, ds, "m2")
	if rc2.Requests != 1 {
		t.Errorf("m2: expected Requests=1, got %d", rc2.Requests)
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

// TestAvgTTFTFirstObservation verifies that the first TTFT sets AvgTTFT directly (no EMA blend).
func TestAvgTTFTFirstObservation(t *testing.T) {
	ext, ds := newRequestMetadataTest(t)

	if err := ext.Extract(context.Background(), []dlsrc.Event{
		makeResponseEventWithTTFT(0, 500*time.Millisecond),
	}); err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	rc := getRequestMetadata(t, ds, "m1")
	if rc.AvgTTFT != 0.5 {
		t.Errorf("expected AvgTTFT=0.5, got %f", rc.AvgTTFT)
	}
}

// TestAvgTTFTEMABlend verifies that each Extract() call blends with α=0.1.
// Batch 1: TTFT=1s → AvgTTFT=1.0
// Batch 2: TTFT=2s → AvgTTFT = 0.1×2 + 0.9×1 = 1.1
func TestAvgTTFTEMABlend(t *testing.T) {
	ext, ds := newRequestMetadataTest(t)

	if err := ext.Extract(context.Background(), []dlsrc.Event{
		makeResponseEventWithTTFT(0, 1*time.Second),
	}); err != nil {
		t.Fatalf("Extract failed: %v", err)
	}
	if err := ext.Extract(context.Background(), []dlsrc.Event{
		makeResponseEventWithTTFT(0, 2*time.Second),
	}); err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	rc := getRequestMetadata(t, ds, "m1")
	want := 0.1*2.0 + 0.9*1.0 // 1.1
	if rc.AvgTTFT != want {
		t.Errorf("expected AvgTTFT=%f, got %f", want, rc.AvgTTFT)
	}
}

// TestAvgTTFTZeroIgnored verifies that a zero TTFT does not update AvgTTFT.
func TestAvgTTFTZeroIgnored(t *testing.T) {
	ext, ds := newRequestMetadataTest(t)

	if err := ext.Extract(context.Background(), []dlsrc.Event{
		makeResponseEventWithTTFT(0, 1*time.Second),
	}); err != nil {
		t.Fatalf("Extract failed: %v", err)
	}
	if err := ext.Extract(context.Background(), []dlsrc.Event{
		makeResponseEvent(0),
	}); err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	rc := getRequestMetadata(t, ds, "m1")
	if rc.AvgTTFT != 1.0 {
		t.Errorf("expected AvgTTFT=1.0 (unchanged), got %f", rc.AvgTTFT)
	}
}

// TestAvgTPOTFirstObservation verifies that the first TPOT sets AvgTPOT directly.
// duration=3s, ttft=1s → decodeTime=2s, completion_tokens=4 → TPOT = 0.5s/token
func TestAvgTPOTFirstObservation(t *testing.T) {
	ext, ds := newRequestMetadataTest(t)

	if err := ext.Extract(context.Background(), []dlsrc.Event{
		makeResponseEventFull(3000, 1*time.Second, 4),
	}); err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	rc := getRequestMetadata(t, ds, "m1")
	if rc.AvgTPOT != 0.5 {
		t.Errorf("expected AvgTPOT=0.5, got %f", rc.AvgTPOT)
	}
}

// TestAvgTPOTEMABlend verifies that each Extract() call blends with α=0.1.
// Batch 1: decodeTime=2s/4tokens=0.5 → AvgTPOT=0.5
// Batch 2: decodeTime=2s/2tokens=1.0 → AvgTPOT = 0.1×1.0 + 0.9×0.5 = 0.55
func TestAvgTPOTEMABlend(t *testing.T) {
	ext, ds := newRequestMetadataTest(t)

	if err := ext.Extract(context.Background(), []dlsrc.Event{
		makeResponseEventFull(3000, 1*time.Second, 4),
	}); err != nil {
		t.Fatalf("Extract failed: %v", err)
	}
	if err := ext.Extract(context.Background(), []dlsrc.Event{
		makeResponseEventFull(3000, 1*time.Second, 2),
	}); err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	rc := getRequestMetadata(t, ds, "m1")
	want := 0.1*1.0 + 0.9*0.5 // 0.55
	if rc.AvgTPOT != want {
		t.Errorf("expected AvgTPOT=%f, got %f", want, rc.AvgTPOT)
	}
}

// TestAvgTPOTZeroCompletionTokensIgnored verifies that a response with no completion_tokens
// does not update AvgTPOT.
func TestAvgTPOTZeroCompletionTokensIgnored(t *testing.T) {
	ext, ds := newRequestMetadataTest(t)

	if err := ext.Extract(context.Background(), []dlsrc.Event{
		makeResponseEventFull(3000, 1*time.Second, 4),
	}); err != nil {
		t.Fatalf("Extract failed: %v", err)
	}
	if err := ext.Extract(context.Background(), []dlsrc.Event{
		makeResponseEvent(1000),
	}); err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	rc := getRequestMetadata(t, ds, "m1")
	if rc.AvgTPOT != 0.5 {
		t.Errorf("expected AvgTPOT=0.5 (unchanged), got %f", rc.AvgTPOT)
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
