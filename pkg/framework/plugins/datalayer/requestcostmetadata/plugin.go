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

// Package requestcostmetadata implements a datasource extractor that
// turns each completed inference response into a per-request cost sample
// and folds it into a per-model t-digest stored on the Model's
// AttributeMap under accumulator.CostDigestAttributeKey. It is a building
// block for the CostGuard scorer; see the package README for behavioral
// intent and configuration.
package requestcostmetadata

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/caio/go-tdigest/v5"
	"sigs.k8s.io/controller-runtime/pkg/log"

	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer/accumulator"
	dlsrc "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer/datasource"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer/pricing"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
)

const (
	// PluginType is the identifier used when registering this extractor.
	PluginType = "model-cost-extractor"

	// defaultCompression matches the t-digest compression value used in the
	// CostGuard proposal.
	defaultCompression = 200.0

	// defaultFlushIntervalDuration is the aggregation window before a per-model
	// digest snapshot is published to the AttributeMap. Mirrors the pattern in
	// the requestmetadata extractor.
	defaultFlushIntervalDuration = 5 * time.Second
)

// compile-time interface assertion
var _ dlsrc.Extractor = &RequestCostMetadataExtractor{}

// RequestCostMetadataExtractorConfig holds the JSON-configurable parameters
// for the extractor.
type RequestCostMetadataExtractorConfig struct {
	// Compression is the t-digest compression. Higher values trade memory
	// for quantile accuracy. Must be > 0. Defaults to 200 if not specified.
	Compression float64 `json:"compression,omitempty"`

	// FlushIntervalDuration is the aggregation window before a per-model digest
	// snapshot is published to the AttributeMap (e.g. "5s", "1m"). Set to "0s"
	// to publish on every event (used in unit tests). Defaults to "5s".
	FlushIntervalDuration string `json:"flushIntervalDuration,omitempty"`
}

// ExtractorFactory creates a RequestCostMetadataExtractor wired to the shared Datastore.
func ExtractorFactory(name string, parameters json.RawMessage, h plugin.Handle) (plugin.Plugin, error) {
	config := RequestCostMetadataExtractorConfig{
		Compression:           defaultCompression,
		FlushIntervalDuration: defaultFlushIntervalDuration.String(),
	}
	if len(parameters) > 0 {
		if err := json.Unmarshal(parameters, &config); err != nil {
			return nil, fmt.Errorf("failed to parse parameters for plugin %q: %w", name, err)
		}
	}

	flushInterval, err := time.ParseDuration(config.FlushIntervalDuration)
	if err != nil {
		return nil, fmt.Errorf("invalid flushIntervalDuration %q for plugin %q: %w", config.FlushIntervalDuration, name, err)
	}
	if flushInterval < 0 {
		return nil, fmt.Errorf("invalid flushIntervalDuration %q for plugin %q: must be >= 0", config.FlushIntervalDuration, name)
	}

	ext := NewRequestCostMetadataExtractor(h.Datastore(), config.Compression, flushInterval)
	ext.typedName.Name = name
	return ext, nil
}

// modelCostAccumulator holds the running t-digest for a single model and the
// timestamp of its last flush, so the extractor can decide when to publish a
// snapshot to the AttributeMap.
type modelCostAccumulator struct {
	digest    *tdigest.TDigest
	lastFlush time.Time
}

// RequestCostMetadataExtractor accumulates per-model cost samples derived from
// response usage metadata and pricing attributes, and publishes a t-digest
// snapshot to the Model's AttributeMap on flush.
//
// Extract is assumed to be called from a single goroutine (the
// NotificationSource event loop).
type RequestCostMetadataExtractor struct {
	typedName     plugin.TypedName
	ds            datalayer.Datastore
	state         map[string]*modelCostAccumulator
	compression   float64
	flushInterval time.Duration
}

// NewRequestCostMetadataExtractor constructs an extractor wired to ds with
// the specified compression and flush interval.
func NewRequestCostMetadataExtractor(ds datalayer.Datastore, compression float64, flushInterval time.Duration) *RequestCostMetadataExtractor {
	return &RequestCostMetadataExtractor{
		typedName:     plugin.TypedName{Type: PluginType, Name: PluginType},
		ds:            ds,
		state:         make(map[string]*modelCostAccumulator),
		compression:   compression,
		flushInterval: flushInterval,
	}
}

func (e *RequestCostMetadataExtractor) TypedName() plugin.TypedName { return e.typedName }

// Extract processes a batch of events. RequestEventType events are ignored;
// each ResponseEventType produces (at most) one cost sample, which is added
// to that model's running t-digest. Per-model snapshots are published to the
// AttributeMap when the flush interval has elapsed since the last publish.
func (e *RequestCostMetadataExtractor) Extract(ctx context.Context, events []dlsrc.Event) error {
	debugLogger := log.FromContext(ctx).V(logutil.DEBUG)

	now := time.Now()
	updated := make(map[string]*modelCostAccumulator)
	// Cache token prices per-model to avoid repeated lookups within this batch
	tokenPricesCache := make(map[string]*pricing.TokenPrices)

	for _, ev := range events {
		if ev.Type != dlsrc.ResponseEventType {
			continue
		}
		p, ok := ev.Payload.(dlsrc.ResponsePayload)
		if !ok {
			continue
		}
		// Distinguish "model field absent" from "model field present but
		// not a string" so a malformed upstream is visible in debug logs
		// rather than indistinguishable from a request with no model.
		rawModel, hasModel := p.Request.Body["model"]
		if !hasModel {
			continue
		}
		model, isString := rawModel.(string)
		if !isString {
			debugLogger.Info("response request body has non-string model field, skipping", "model_type", fmt.Sprintf("%T", rawModel))
			continue
		}
		if model == "" {
			continue
		}

		// Check cache first; only lookup if not already cached
		tp, ok := tokenPricesCache[model]
		if !ok {
			found := false
			tp, found = lookupTokenPrices(e.ds, model)
			if !found {
				debugLogger.Info("model has no TokenPrices attribute, skipping cost sample", "model", model)
				continue
			}
			tokenPricesCache[model] = tp
		}

		if tp == nil {
			debugLogger.Info("TokenPrices is nil, skipping cost sample", "model", model)
			continue
		}

		promptTokens, completionTokens, ok := extractTokenCounts(p)
		if !ok {
			debugLogger.Info("response missing usable usage fields, skipping", "model", model)
			continue
		}

		cost := promptTokens*tp.InputTokenPrice + completionTokens*tp.OutputTokenPrice

		acc, err := e.getOrCreateAccumulator(model, now)
		if err != nil {
			debugLogger.Info("failed to create tdigest accumulator, skipping sample", "model", model, "err", err)
			continue
		}
		if err := acc.digest.Add(cost); err != nil {
			debugLogger.Info("tdigest.Add returned an unexpected error, skipping sample", "model", model, "err", err)
			continue
		}
		updated[model] = acc
	}

	// After extracting all valid samples, iterate over models that were updated
	// in this batch to decide which ones to flush.
	for model, acc := range updated {
		// flushInterval == 0 means publish on every event
		if e.flushInterval > 0 && now.Sub(acc.lastFlush) < e.flushInterval {
			continue
		}
		acc.lastFlush = now
		// Clone never fails per tdigest contract; it returns a deep copy of the digest.
		snapshot := acc.digest.Clone()
		e.ds.GetOrCreateModel(model).GetAttributes().Put(
			accumulator.CostDigestAttributeKey,
			&accumulator.CostDigest{Digest: snapshot},
		)
		debugLogger.Info("request-cost-metadata published cost digest snapshot",
			"model", model,
			"count", snapshot.Count(),
		)
	}
	return nil
}

// extractTokenCounts pulls prompt_tokens and completion_tokens from the
// response's usage block. Both must be present and positive; any failure
// returns ok=false so the sample is skipped.
func extractTokenCounts(p dlsrc.ResponsePayload) (prompt, completion float64, ok bool) {
	usage, ok := p.Response.Body["usage"].(map[string]any)
	if !ok {
		return 0, 0, false
	}
	prompt, ok = usage["prompt_tokens"].(float64)
	if !ok || prompt <= 0 {
		return 0, 0, false
	}
	completion, ok = usage["completion_tokens"].(float64)
	if !ok || completion <= 0 {
		return 0, 0, false
	}
	return prompt, completion, true
}

// lookupTokenPrices fetches the *pricing.TokenPrices stored on the model's
// AttributeMap. Returns ok=false if the attribute is absent or of the wrong
// type, in which case the caller skips the cost sample (a model with no
// pricing has no defined cost to record).
//
// Note: This function uses GetOrCreateModel, which creates an empty model
// in the datastore if the model name does not exist. When a response arrives
// for an unconfigured model (no pricing), the extractor skips the cost sample
// but the empty model remains in Datastore.Models(), which may cause
// operator confusion when querying the model registry. This is harmless
// functionally but is a known side-effect; a follow-up PR will add a
// read-only GetModel method to avoid this.
func lookupTokenPrices(ds datalayer.Datastore, model string) (*pricing.TokenPrices, bool) {
	v, ok := ds.GetOrCreateModel(model).GetAttributes().Get(pricing.TokenPricesAttributeKey)
	if !ok {
		return nil, false
	}
	tp, ok := v.(*pricing.TokenPrices)
	if !ok {
		return nil, false
	}
	return tp, true
}

// getOrCreateAccumulator returns the per-model accumulator, creating a fresh
// t-digest on first use. lastFlush is initialized to now so the first publish
// happens after one full flushInterval, matching the requestmetadata pattern.
func (e *RequestCostMetadataExtractor) getOrCreateAccumulator(model string, now time.Time) (*modelCostAccumulator, error) {
	if acc, ok := e.state[model]; ok {
		return acc, nil
	}
	d, err := tdigest.New(tdigest.Compression(e.compression))
	if err != nil {
		return nil, err
	}
	acc := &modelCostAccumulator{digest: d, lastFlush: now}
	e.state[model] = acc
	return acc, nil
}
