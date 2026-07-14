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

package requestcostmetadata

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	ctrlbuilder "sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/datastore"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer/accumulator"
	dlsrc "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer/datasource"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer/pricing"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
)

// fakeHandle implements plugin.Handle for unit tests.
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

// makeResponseEvent builds a ResponseEventType event for the named model whose
// usage block reports promptTokens and completionTokens. Pass <= 0 to omit a
// field; pass omitUsage=true to omit the entire usage block.
func makeResponseEvent(model string, promptTokens, completionTokens float64, omitUsage bool) dlsrc.Event {
	req := requesthandling.NewInferenceRequest()
	req.Body["model"] = model
	resp := requesthandling.NewInferenceResponse()
	if !omitUsage {
		usage := map[string]any{}
		if promptTokens > 0 {
			usage["prompt_tokens"] = promptTokens
		}
		if completionTokens > 0 {
			usage["completion_tokens"] = completionTokens
		}
		resp.Body["usage"] = usage
	}
	return dlsrc.Event{
		Type:    dlsrc.ResponseEventType,
		Payload: dlsrc.ResponsePayload{Request: req, Response: resp},
	}
}

// setTokenPrices attaches a TokenPrices attribute to the named model in ds.
func setTokenPrices(ds datalayer.Datastore, model string, in, out float64) {
	ds.GetOrCreateModel(model).GetAttributes().Put(
		pricing.TokenPricesAttributeKey,
		&pricing.TokenPrices{InputTokenPrice: in, OutputTokenPrice: out},
	)
}

// readDigest fetches the *accumulator.CostDigest for model from ds,
// returning nil if the attribute is absent or of the wrong type.
func readDigest(ds datalayer.Datastore, model string) *accumulator.CostDigest {
	v, ok := ds.GetOrCreateModel(model).GetAttributes().Get(accumulator.CostDigestAttributeKey)
	if !ok {
		return nil
	}
	cd, _ := v.(*accumulator.CostDigest)
	return cd
}

// newTestExtractor builds an extractor with flushInterval=0 so every event
// flushes immediately, mirroring the requestmetadata test pattern. Tests
// exercising non-zero flush intervals build their extractor inline.
func newTestExtractor(t *testing.T) (*RequestCostMetadataExtractor, datalayer.Datastore) {
	t.Helper()
	ds := datastore.NewFakeDataStore()
	ext := NewRequestCostMetadataExtractor(ds, defaultCompression, 0)
	return ext, ds
}

// --- Factory tests ---


func TestExtractorFactory_HonorsConfig(t *testing.T) {
	ds := datastore.NewFakeDataStore()
	raw := json.RawMessage(`{"compression":50,"flushIntervalDuration":"1m"}`)
	p, err := ExtractorFactory("x", raw, &fakeHandle{ds: ds})
	if err != nil {
		t.Fatalf("ExtractorFactory: %v", err)
	}
	ext := p.(*RequestCostMetadataExtractor)
	if ext.compression != 50 {
		t.Errorf("compression = %f, want 50", ext.compression)
	}
	if ext.flushInterval != time.Minute {
		t.Errorf("flushInterval = %v, want 1m", ext.flushInterval)
	}
}

func TestExtractorFactory_RejectsInvalidFlushInterval(t *testing.T) {
	tests := []struct {
		name string
		raw  json.RawMessage
	}{
		{"malformed duration", json.RawMessage(`{"compression":200,"flushIntervalDuration":"not-a-duration"}`)},
		{"negative duration", json.RawMessage(`{"compression":200,"flushIntervalDuration":"-1s"}`)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ds := datastore.NewFakeDataStore()
			if _, err := ExtractorFactory("x", tc.raw, &fakeHandle{ds: ds}); err == nil {
				t.Errorf("expected error for %s, got nil", tc.name)
			}
		})
	}
}

// --- Extract tests ---

// TestExtract_PublishesCostDigest verifies the happy path: a response event
// for a model with TokenPrices produces a digest snapshot on the AttributeMap
// whose count includes the new sample.
func TestExtract_PublishesCostDigest(t *testing.T) {
	ext, ds := newTestExtractor(t)
	setTokenPrices(ds, "m1", 1e-6, 4e-6) // input $1/M, output $4/M (per token)

	ev := makeResponseEvent("m1", 100, 50, false)
	if err := ext.Extract(context.Background(), []dlsrc.Event{ev}); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	cd := readDigest(ds, "m1")
	if cd == nil {
		t.Fatal("expected CostDigest attribute to be present")
	}
	if cd.Digest.Count() != 1 {
		t.Errorf("digest count = %d, want 1", cd.Digest.Count())
	}
	// Cost = 100 * 1e-6 + 50 * 4e-6 = 1e-4 + 2e-4 = 3e-4. With one sample,
	// the digest's median should equal the inserted value.
	wantCost := 100.0*1e-6 + 50.0*4e-6
	if got := cd.Digest.Quantile(0.5); got != wantCost {
		t.Errorf("Quantile(0.5) = %f, want %f", got, wantCost)
	}
}

// TestExtract_SkipsEmptyModel verifies that responses with an empty model string
// are skipped without panicking or creating a model side-effect.
func TestExtract_SkipsEmptyModel(t *testing.T) {
	ext, ds := newTestExtractor(t)
	setTokenPrices(ds, "", 1e-6, 1e-6)

	ev := makeResponseEvent("", 100, 50, false)
	if err := ext.Extract(context.Background(), []dlsrc.Event{ev}); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if readDigest(ds, "") != nil {
		t.Error("expected no CostDigest attribute for empty model string")
	}
}

// TestExtract_SkipsNonStringModel verifies that responses with non-string model
// types are skipped without panicking or creating a side-effect.
func TestExtract_SkipsNonStringModel(t *testing.T) {
	ext, ds := newTestExtractor(t)
	setTokenPrices(ds, "m1", 1e-6, 1e-6)

	req := requesthandling.NewInferenceRequest()
	resp := requesthandling.NewInferenceResponse()
	req.Body["model"] = 123 // integer, not string
	resp.Body["usage"] = map[string]any{"prompt_tokens": 100.0, "completion_tokens": 50.0}

	ev := dlsrc.Event{
		Type:    dlsrc.ResponseEventType,
		Payload: dlsrc.ResponsePayload{Request: req, Response: resp},
	}
	if err := ext.Extract(context.Background(), []dlsrc.Event{ev}); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if readDigest(ds, "m1") != nil {
		t.Error("expected no CostDigest attribute for non-string model type")
	}
}

// TestExtract_SkipsRequestEvents verifies that RequestEventType events do not
// produce cost samples. (Cost is observable only on the response.)
func TestExtract_SkipsRequestEvents(t *testing.T) {
	ext, ds := newTestExtractor(t)
	setTokenPrices(ds, "m1", 1e-6, 1e-6)

	req := requesthandling.NewInferenceRequest()
	req.Body["model"] = "m1"
	ev := dlsrc.Event{Type: dlsrc.RequestEventType, Payload: dlsrc.RequestPayload{Request: req}}

	if err := ext.Extract(context.Background(), []dlsrc.Event{ev}); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if readDigest(ds, "m1") != nil {
		t.Error("expected no CostDigest attribute after request-only batch")
	}
}

// TestExtract_SkipsMissingUsage verifies that a response with no usage block
// is skipped without panicking and without publishing a digest.
func TestExtract_SkipsMissingUsage(t *testing.T) {
	ext, ds := newTestExtractor(t)
	setTokenPrices(ds, "m1", 1e-6, 1e-6)

	ev := makeResponseEvent("m1", 0, 0, true)
	if err := ext.Extract(context.Background(), []dlsrc.Event{ev}); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if readDigest(ds, "m1") != nil {
		t.Error("expected no CostDigest attribute after missing-usage batch")
	}
}

// TestExtract_SkipsNonPositiveTokens verifies that responses with missing,
// zero, or negative token counts are skipped without publishing.
func TestExtract_SkipsNonPositiveTokens(t *testing.T) {
	ext, ds := newTestExtractor(t)
	setTokenPrices(ds, "m1", 1e-6, 1e-6)

	tests := []struct {
		name string
		ev   dlsrc.Event
	}{
		{
			name: "missing prompt_tokens",
			ev:   makeResponseEvent("m1", 0, 50, false),
		},
		{
			name: "missing completion_tokens",
			ev:   makeResponseEvent("m1", 100, 0, false),
		},
		{
			name: "both missing",
			ev:   makeResponseEvent("m1", 0, 0, false),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := ext.Extract(context.Background(), []dlsrc.Event{tc.ev}); err != nil {
				t.Fatalf("Extract: %v", err)
			}
			if readDigest(ds, "m1") != nil {
				t.Errorf("expected no CostDigest attribute for %s", tc.name)
			}
		})
	}
}

// TestExtract_SkipsMissingPricing verifies that a response for a model with
// no TokenPrices attribute is skipped without publishing or creating a model
// side-effect.
func TestExtract_SkipsMissingPricing(t *testing.T) {
	ext, ds := newTestExtractor(t)
	// Intentionally do NOT set pricing for m1

	ev := makeResponseEvent("m1", 100, 50, false)
	if err := ext.Extract(context.Background(), []dlsrc.Event{ev}); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if readDigest(ds, "m1") != nil {
		t.Error("expected no CostDigest attribute for model without pricing")
	}
}

// TestExtract_EmptyBatch verifies that passing an empty event slice does not
// panic and returns nil with no state changes.
func TestExtract_EmptyBatch(t *testing.T) {
	ext, ds := newTestExtractor(t)
	setTokenPrices(ds, "m1", 1e-6, 1e-6)

	// Extract with empty batch
	if err := ext.Extract(context.Background(), []dlsrc.Event{}); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	// No digest should be published
	if readDigest(ds, "m1") != nil {
		t.Error("expected no CostDigest after empty batch")
	}
}

// TestExtract_MultipleModels verifies that multiple distinct models in a single
// batch accumulate independently with correct cost samples in each digest.
func TestExtract_MultipleModels(t *testing.T) {
	ext, ds := newTestExtractor(t)
	setTokenPrices(ds, "m1", 1e-6, 2e-6) // input $1/M, output $2/M
	setTokenPrices(ds, "m2", 5e-7, 1e-6) // input $0.5/M, output $1/M

	// Batch with interleaved models: m1, m2, m1
	ev1 := makeResponseEvent("m1", 100, 50, false)   // cost = 100*1e-6 + 50*2e-6 = 1e-4 + 1e-4 = 2e-4
	ev2 := makeResponseEvent("m2", 200, 100, false)  // cost = 200*5e-7 + 100*1e-6 = 1e-4 + 1e-4 = 2e-4
	ev3 := makeResponseEvent("m1", 50, 100, false)   // cost = 50*1e-6 + 100*2e-6 = 5e-5 + 2e-4 = 2.5e-4

	if err := ext.Extract(context.Background(), []dlsrc.Event{ev1, ev2, ev3}); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	// m1 should have 2 samples (ev1 + ev3)
	cd1 := readDigest(ds, "m1")
	if cd1 == nil {
		t.Fatal("expected CostDigest for m1")
	}
	if cd1.Digest.Count() != 2 {
		t.Errorf("m1 digest count = %d, want 2", cd1.Digest.Count())
	}

	// m2 should have 1 sample (ev2)
	cd2 := readDigest(ds, "m2")
	if cd2 == nil {
		t.Fatal("expected CostDigest for m2")
	}
	if cd2.Digest.Count() != 1 {
		t.Errorf("m2 digest count = %d, want 1", cd2.Digest.Count())
	}

	// Verify m1's quantile includes both samples (first and second cost values)
	// With 2 samples [2e-4, 2.5e-4], median should be between them
	q1 := cd1.Digest.Quantile(0.5)
	if q1 < 2e-4 || q1 > 2.5e-4 {
		t.Errorf("m1 Quantile(0.5) = %f, want between 2e-4 and 2.5e-4", q1)
	}

	// Verify m2's quantile is the single sample (with floating-point tolerance)
	wantCost2 := 200.0*5e-7 + 100.0*1e-6
	q2 := cd2.Digest.Quantile(0.5)
	tolerance := 1e-10
	if diff := q2 - wantCost2; diff < -tolerance || diff > tolerance {
		t.Errorf("m2 Quantile(0.5) = %f, want %f", q2, wantCost2)
	}
}

// TestExtract_FlushIntervalGating verifies that snapshots are not published
// before the flush interval elapses, but are published once it does.
func TestExtract_FlushIntervalGating(t *testing.T) {
	ds := datastore.NewFakeDataStore()
	// Create an extractor with a 10ms flush interval (short for testing)
	ext := NewRequestCostMetadataExtractor(ds, defaultCompression, 10*time.Millisecond)
	setTokenPrices(ds, "m1", 1e-6, 1e-6)

	// First event: should not publish (first lastFlush is now, no interval elapsed)
	ev1 := makeResponseEvent("m1", 100, 100, false)
	if err := ext.Extract(context.Background(), []dlsrc.Event{ev1}); err != nil {
		t.Fatalf("Extract 1: %v", err)
	}
	if readDigest(ds, "m1") != nil {
		t.Error("expected no CostDigest after first event (before interval)")
	}

	// Wait for interval to pass
	time.Sleep(20 * time.Millisecond)

	// Second event: should publish (interval elapsed)
	ev2 := makeResponseEvent("m1", 50, 50, false)
	if err := ext.Extract(context.Background(), []dlsrc.Event{ev2}); err != nil {
		t.Fatalf("Extract 2: %v", err)
	}
	cd := readDigest(ds, "m1")
	if cd == nil {
		t.Fatal("expected CostDigest after interval elapsed")
	}
	// After publishing, the snapshot is a clone of the internal digest
	// The internal digest has both samples (100+50 tokens each), but we're
	// testing that the snapshot was published — just verify count > 0.
	if cd.Digest.Count() == 0 {
		t.Errorf("expected published digest to have samples, got count=0")
	}
}

