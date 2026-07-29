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

// makeAnthropicResponseEvent builds a ResponseEventType event whose usage block
// uses Anthropic field names (input_tokens / output_tokens).
func makeAnthropicResponseEvent(model string, inputTokens, outputTokens float64, omitUsage bool) dlsrc.Event { //nolint:unparam
	req := requesthandling.NewInferenceRequest()
	req.Body["model"] = model
	resp := requesthandling.NewInferenceResponse()
	if !omitUsage {
		usage := map[string]any{}
		if inputTokens > 0 {
			usage["input_tokens"] = inputTokens
		}
		if outputTokens > 0 {
			usage["output_tokens"] = outputTokens
		}
		resp.Body["usage"] = usage
	}
	return dlsrc.Event{
		Type:    dlsrc.ResponseEventType,
		Payload: dlsrc.ResponsePayload{Request: req, Response: resp},
	}
}

// makeGoogleResponseEvent builds a ResponseEventType event whose usage block
// uses Google/Gemini field names (usageMetadata.promptTokenCount / candidatesTokenCount).
func makeGoogleResponseEvent(model string, promptTokenCount, candidatesTokenCount float64, omitUsage bool) dlsrc.Event { //nolint:unparam
	req := requesthandling.NewInferenceRequest()
	req.Body["model"] = model
	resp := requesthandling.NewInferenceResponse()
	if !omitUsage {
		usageMetadata := map[string]any{}
		if promptTokenCount > 0 {
			usageMetadata["promptTokenCount"] = promptTokenCount
		}
		if candidatesTokenCount > 0 {
			usageMetadata["candidatesTokenCount"] = candidatesTokenCount
		}
		if promptTokenCount > 0 && candidatesTokenCount > 0 {
			usageMetadata["totalTokenCount"] = promptTokenCount + candidatesTokenCount
		}
		resp.Body["usageMetadata"] = usageMetadata
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
	ext := NewRequestCostMetadataExtractor(ds, defaultCompression, 0, 0)
	return ext, ds
}

// --- Factory tests ---

func TestExtractorFactory_HonorsConfig(t *testing.T) {
	ds := datastore.NewFakeDataStore()
	raw := json.RawMessage(`{"compression":50,"flushIntervalDuration":"1m","windowDuration":"30m"}`)
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
	if ext.windowDuration != 30*time.Minute {
		t.Errorf("windowDuration = %v, want 30m", ext.windowDuration)
	}
}

func TestExtractorFactory_RejectsInvalidDurations(t *testing.T) {
	tests := []struct {
		name string
		raw  json.RawMessage
	}{
		{"flushIntervalDuration malformed", json.RawMessage(`{"compression":200,"flushIntervalDuration":"not-a-duration"}`)},
		{"flushIntervalDuration negative", json.RawMessage(`{"compression":200,"flushIntervalDuration":"-1s"}`)},
		{"windowDuration malformed", json.RawMessage(`{"compression":200,"windowDuration":"not-a-duration"}`)},
		{"windowDuration negative", json.RawMessage(`{"compression":200,"windowDuration":"-1s"}`)},
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
// TestExtract_MissingTokenFields verifies that extractTokenCounts rejects the
// event and no digest is published when either token field is missing or the
// usage block is omitted entirely. Covers all three supported response formats
// (OpenAI, Anthropic, Google/Gemini) against the same missing-field matrix.
func TestExtract_MissingTokenFields(t *testing.T) {
	formats := []struct {
		name     string
		model    string
		priceIn  float64
		priceOut float64
		build    func(model string, prompt, completion float64, omitUsage bool) dlsrc.Event
	}{
		{"openai", "m1", 1e-6, 1e-6, makeResponseEvent},
		{"anthropic", "claude", 3e-6, 15e-6, makeAnthropicResponseEvent},
		{"google", "gemini", 1.25e-6, 5e-6, makeGoogleResponseEvent},
	}
	cases := []struct {
		name               string
		prompt, completion float64
		omitUsage          bool
	}{
		{"missing prompt", 0, 50, false},
		{"missing completion", 100, 0, false},
		{"both missing", 0, 0, false},
		{"no usage block", 0, 0, true},
	}
	for _, f := range formats {
		for _, c := range cases {
			t.Run(f.name+"/"+c.name, func(t *testing.T) {
				ext, ds := newTestExtractor(t)
				setTokenPrices(ds, f.model, f.priceIn, f.priceOut)
				ev := f.build(f.model, c.prompt, c.completion, c.omitUsage)
				if err := ext.Extract(context.Background(), []dlsrc.Event{ev}); err != nil {
					t.Fatalf("Extract: %v", err)
				}
				if readDigest(ds, f.model) != nil {
					t.Errorf("expected no CostDigest attribute")
				}
			})
		}
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
	ev1 := makeResponseEvent("m1", 100, 50, false)  // cost = 100*1e-6 + 50*2e-6 = 1e-4 + 1e-4 = 2e-4
	ev2 := makeResponseEvent("m2", 200, 100, false) // cost = 200*5e-7 + 100*1e-6 = 1e-4 + 1e-4 = 2e-4
	ev3 := makeResponseEvent("m1", 50, 100, false)  // cost = 50*1e-6 + 100*2e-6 = 5e-5 + 2e-4 = 2.5e-4

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

// --- Anthropic format tests ---

func TestExtract_AnthropicFormat(t *testing.T) {
	ext, ds := newTestExtractor(t)
	setTokenPrices(ds, "claude", 3e-6, 15e-6)

	ev := makeAnthropicResponseEvent("claude", 100, 50, false)
	if err := ext.Extract(context.Background(), []dlsrc.Event{ev}); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	cd := readDigest(ds, "claude")
	if cd == nil {
		t.Fatal("expected CostDigest attribute to be present for Anthropic format")
	}
	if cd.Digest.Count() != 1 {
		t.Errorf("digest count = %d, want 1", cd.Digest.Count())
	}
	wantCost := 100.0*3e-6 + 50.0*15e-6
	got := cd.Digest.Quantile(0.5)
	if diff := got - wantCost; diff < -1e-10 || diff > 1e-10 {
		t.Errorf("Quantile(0.5) = %f, want %f", got, wantCost)
	}
}

// --- Google format tests ---

func TestExtract_GoogleFormat(t *testing.T) {
	ext, ds := newTestExtractor(t)
	setTokenPrices(ds, "gemini", 1.25e-6, 5e-6)

	ev := makeGoogleResponseEvent("gemini", 200, 100, false)
	if err := ext.Extract(context.Background(), []dlsrc.Event{ev}); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	cd := readDigest(ds, "gemini")
	if cd == nil {
		t.Fatal("expected CostDigest attribute to be present for Google format")
	}
	if cd.Digest.Count() != 1 {
		t.Errorf("digest count = %d, want 1", cd.Digest.Count())
	}
	wantCost := 200.0*1.25e-6 + 100.0*5e-6
	got := cd.Digest.Quantile(0.5)
	if diff := got - wantCost; diff < -1e-10 || diff > 1e-10 {
		t.Errorf("Quantile(0.5) = %f, want %f", got, wantCost)
	}
}

// --- Mixed format tests ---

func TestExtract_MixedFormatsInBatch(t *testing.T) {
	ext, ds := newTestExtractor(t)
	setTokenPrices(ds, "gpt4", 2.5e-6, 10e-6)
	setTokenPrices(ds, "claude", 3e-6, 15e-6)
	setTokenPrices(ds, "gemini", 1.25e-6, 5e-6)

	evOpenAI := makeResponseEvent("gpt4", 100, 50, false)
	evAnthropic := makeAnthropicResponseEvent("claude", 100, 50, false)
	evGoogle := makeGoogleResponseEvent("gemini", 100, 50, false)

	if err := ext.Extract(context.Background(), []dlsrc.Event{evOpenAI, evAnthropic, evGoogle}); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	for _, tc := range []struct {
		model    string
		inPrice  float64
		outPrice float64
	}{
		{"gpt4", 2.5e-6, 10e-6},
		{"claude", 3e-6, 15e-6},
		{"gemini", 1.25e-6, 5e-6},
	} {
		cd := readDigest(ds, tc.model)
		if cd == nil {
			t.Fatalf("expected CostDigest for %s", tc.model)
		}
		if cd.Digest.Count() != 1 {
			t.Errorf("%s digest count = %d, want 1", tc.model, cd.Digest.Count())
		}
		wantCost := 100.0*tc.inPrice + 50.0*tc.outPrice
		got := cd.Digest.Quantile(0.5)
		if diff := got - wantCost; diff < -1e-10 || diff > 1e-10 {
			t.Errorf("%s Quantile(0.5) = %f, want %f", tc.model, got, wantCost)
		}
	}
}

func TestExtract_OpenAIStillWorks(t *testing.T) {
	ext, ds := newTestExtractor(t)
	setTokenPrices(ds, "gpt4", 2.5e-6, 10e-6)

	ev := makeResponseEvent("gpt4", 150, 75, false)
	if err := ext.Extract(context.Background(), []dlsrc.Event{ev}); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	cd := readDigest(ds, "gpt4")
	if cd == nil {
		t.Fatal("expected CostDigest attribute to be present for OpenAI format")
	}
	if cd.Digest.Count() != 1 {
		t.Errorf("digest count = %d, want 1", cd.Digest.Count())
	}
	wantCost := 150.0*2.5e-6 + 75.0*10e-6
	got := cd.Digest.Quantile(0.5)
	if diff := got - wantCost; diff < -1e-10 || diff > 1e-10 {
		t.Errorf("Quantile(0.5) = %f, want %f", got, wantCost)
	}
}

func TestExtract_UnknownFormatSkipped(t *testing.T) {
	ext, ds := newTestExtractor(t)
	setTokenPrices(ds, "custom", 1e-6, 1e-6)

	req := requesthandling.NewInferenceRequest()
	req.Body["model"] = "custom"
	resp := requesthandling.NewInferenceResponse()
	resp.Body["token_info"] = map[string]any{"in": 100.0, "out": 50.0}

	ev := dlsrc.Event{
		Type:    dlsrc.ResponseEventType,
		Payload: dlsrc.ResponsePayload{Request: req, Response: resp},
	}
	if err := ext.Extract(context.Background(), []dlsrc.Event{ev}); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if readDigest(ds, "custom") != nil {
		t.Error("expected no CostDigest attribute for unknown format")
	}
}

// TestExtract_FlushIntervalGating verifies that snapshots are not published
// before the flush interval elapses, but are published once it does.
func TestExtract_FlushIntervalGating(t *testing.T) {
	ds := datastore.NewFakeDataStore()
	// Create an extractor with a 10ms flush interval (short for testing)
	ext := NewRequestCostMetadataExtractor(ds, defaultCompression, 10*time.Millisecond, 0)
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

// TestExtract_Window verifies the per-model t-digest reset that fires when
// windowDuration > 0 and now.Sub(lastWindowStart) >= windowDuration. Uses
// the extractor's unexported `now` test hook so the wall clock is
// deterministic across all four subtests.
//
// All subtests use flushInterval=0 (publish on every event) so the live
// state and the published snapshot coincide; assertions read the live
// t-digest via ext.state[model].digest for clearest intent.
func TestExtract_Window(t *testing.T) {
	// Fixed anchor time so subtests can advance a local clock without wall-time flakes.
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// newWindowExtractor builds an extractor with the specified windowDuration
	// and installs a test clock initialized to base. Returns the extractor,
	// datastore, and a *time.Time knob the test mutates to advance time.
	newWindowExtractor := func(t *testing.T, windowDuration time.Duration) (*RequestCostMetadataExtractor, datalayer.Datastore, *time.Time) {
		t.Helper()
		ds := datastore.NewFakeDataStore()
		ext := NewRequestCostMetadataExtractor(ds, defaultCompression, 0, windowDuration)
		clock := base
		ext.now = func() time.Time { return clock }
		return ext, ds, &clock
	}

	// extract feeds one OpenAI-format event at the current clock time. Prices
	// are 1 in / 1 out so cost is trivially N tokens; these tests care about
	// count and reset semantics, not cost values.
	extract := func(t *testing.T, ext *RequestCostMetadataExtractor, model string, prompt, completion float64) {
		t.Helper()
		if err := ext.Extract(context.Background(), []dlsrc.Event{
			makeResponseEvent(model, prompt, completion, false),
		}); err != nil {
			t.Fatalf("Extract: %v", err)
		}
	}

	t.Run("window-disabled", func(t *testing.T) {
		ext, ds, clock := newWindowExtractor(t, 0)
		setTokenPrices(ds, "m1", 1, 1)
		// Feed 3 samples over 3 simulated hours; nothing should reset.
		extract(t, ext, "m1", 10, 1)
		*clock = base.Add(1 * time.Hour)
		extract(t, ext, "m1", 20, 1)
		*clock = base.Add(3 * time.Hour)
		extract(t, ext, "m1", 30, 1)
		if got := ext.state["m1"].digest.Count(); got != 3 {
			t.Errorf("count = %d, want 3 (windowing disabled → no reset)", got)
		}
	})

	t.Run("resets-across-boundary", func(t *testing.T) {
		ext, ds, clock := newWindowExtractor(t, 1*time.Second)
		setTokenPrices(ds, "m1", 1, 1)
		// t=0: first sample.
		extract(t, ext, "m1", 10, 1)
		if got := ext.state["m1"].digest.Count(); got != 1 {
			t.Fatalf("pre-reset count = %d, want 1", got)
		}
		// t=2s: >= windowDuration since lastWindowStart → reset fires,
		// then this call's sample is added.
		*clock = base.Add(2 * time.Second)
		extract(t, ext, "m1", 20, 1)
		if got := ext.state["m1"].digest.Count(); got != 1 {
			t.Errorf("post-reset count = %d, want 1 (previous sample dropped)", got)
		}
		if got := ext.state["m1"].lastWindowStart; !got.Equal(base.Add(2 * time.Second)) {
			t.Errorf("lastWindowStart = %v, want %v", got, base.Add(2*time.Second))
		}
	})

	t.Run("boundary-exact", func(t *testing.T) {
		// The guard is `now - lastWindowStart >= windowDuration`, so a sample
		// arriving at *exactly* windowDuration must trigger the reset.
		ext, ds, clock := newWindowExtractor(t, 1*time.Second)
		setTokenPrices(ds, "m1", 1, 1)
		extract(t, ext, "m1", 10, 1)
		*clock = base.Add(1 * time.Second)
		extract(t, ext, "m1", 20, 1)
		if got := ext.state["m1"].digest.Count(); got != 1 {
			t.Errorf("boundary-exact count = %d, want 1", got)
		}
	})

	t.Run("multiple-models-independent", func(t *testing.T) {
		// m1 crosses a boundary; m2 does not (its lastWindowStart is set
		// later, so it never accumulates a full windowDuration in this test).
		ext, ds, clock := newWindowExtractor(t, 1*time.Second)
		setTokenPrices(ds, "m1", 1, 1)
		setTokenPrices(ds, "m2", 1, 1)

		// t=0: m1's first sample. m2 not yet seen.
		extract(t, ext, "m1", 10, 1)

		// t=500ms: m2's first sample. m2.lastWindowStart = 500ms.
		*clock = base.Add(500 * time.Millisecond)
		extract(t, ext, "m2", 100, 1)

		// t=1.2s: m1 has been alive 1.2s (>= 1s → reset); m2 has been
		// alive 0.7s (< 1s → no reset).
		*clock = base.Add(1200 * time.Millisecond)
		extract(t, ext, "m1", 20, 1)
		extract(t, ext, "m2", 200, 1)

		if got := ext.state["m1"].digest.Count(); got != 1 {
			t.Errorf("m1 count = %d, want 1 (reset dropped first sample)", got)
		}
		if got := ext.state["m2"].digest.Count(); got != 2 {
			t.Errorf("m2 count = %d, want 2 (no reset, both samples retained)", got)
		}
	})

	t.Run("batch-spans-boundary", func(t *testing.T) {
		// `now := e.clockNow()` is captured once at the top of Extract, so
		// every event in a single batch sees the same clock. When that batch
		// arrives past a window boundary, the reset fires once, and every
		// event in the batch lands in the fresh digest.
		ext, ds, clock := newWindowExtractor(t, 1*time.Second)
		setTokenPrices(ds, "m1", 1, 1)

		// t=0: seed a pre-boundary sample so the reset has something to drop.
		extract(t, ext, "m1", 10, 1)
		if got := ext.state["m1"].digest.Count(); got != 1 {
			t.Fatalf("pre-batch count = %d, want 1", got)
		}

		// t=2s: batch of two events arrives after the boundary. The reset
		// fires once for m1, then both events are added — count becomes 2.
		*clock = base.Add(2 * time.Second)
		ev1 := makeResponseEvent("m1", 20, 1, false)
		ev2 := makeResponseEvent("m1", 30, 1, false)
		if err := ext.Extract(context.Background(), []dlsrc.Event{ev1, ev2}); err != nil {
			t.Fatalf("Extract: %v", err)
		}
		if got := ext.state["m1"].digest.Count(); got != 2 {
			t.Errorf("post-batch count = %d, want 2 (reset once, both new samples retained)", got)
		}
	})
}
