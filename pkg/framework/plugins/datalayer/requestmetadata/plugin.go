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
	"time"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	dlsrc "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer/datasource"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
)

const (
	// PluginType is the identifier used when registering this extractor.
	PluginType = "request-metadata-extractor"

	// RequestMetadataAttributeKey is the attribute key written to each model's attribute store.
	RequestMetadataAttributeKey = "request-metadata"

	// defaultWindowDuration is the interval over which TTFT/TPOT observations are averaged
	// before a single EMA update is applied.
	defaultWindowDuration = 5 * time.Second
)

// compile-time interface assertion
var _ dlsrc.Extractor = &RequestMetadataExtractor{}

// ExtractorFactory creates a RequestMetadataExtractor wired to the shared DataStore.
func ExtractorFactory(name string, _ json.RawMessage, h plugin.Handle) (plugin.Plugin, error) {
	return NewRequestMetadataExtractor(h.Datastore()).WithName(name), nil
}

// RequestMetadataCount holds per-model metadata: in-flight request count and
// EMA estimates for TTFT and TPOT.
type RequestMetadataCount struct {
	Requests int64
	AvgTTFT  float64
	AvgTPOT  float64
}

func (r RequestMetadataCount) Clone() datalayer.Cloneable { return r }

// modelState embeds the published counter and adds the internal window accumulator.
// Observations collected within the window are averaged and applied as one EMA update on flush.
type modelState struct {
	RequestMetadataCount

	windowStart time.Time
	ttftSum     float64
	ttftN       int
	tpotSum     float64
	tpotN       int
}

// flush averages the accumulated window observations into the EMA and resets the window.
func (s *modelState) flush(now time.Time) {
	if s.ttftN > 0 {
		s.AvgTTFT = ema(s.AvgTTFT, s.ttftSum/float64(s.ttftN))
	}
	if s.tpotN > 0 {
		s.AvgTPOT = ema(s.AvgTPOT, s.tpotSum/float64(s.tpotN))
	}
	s.windowStart = now
	s.ttftSum, s.ttftN = 0, 0
	s.tpotSum, s.tpotN = 0, 0
}

// RequestMetadataExtractor tracks per-model in-flight request counts and latency estimates.
// It writes RequestMetadataCount to each model's RequestMetadataAttributeKey attribute.
//
// Extract is assumed to be called from a single goroutine (the NotificationSource event loop).
// If parallel dispatch is introduced, add a sync.Mutex around state and the DataStore write.
//
// TODO: counters leak if a request fails without a corresponding ResponseEventType (e.g. connection
// drop, upstream error, context cancellation). The call site should fire a
// synthetic ResponseEventType in its error/EOF path to keep counts accurate.
type RequestMetadataExtractor struct {
	typedName      plugin.TypedName
	ds             datalayer.Datastore
	state          map[string]*modelState
	windowDuration time.Duration
}

func NewRequestMetadataExtractor(ds datalayer.Datastore) *RequestMetadataExtractor {
	return &RequestMetadataExtractor{
		typedName:      plugin.TypedName{Type: PluginType, Name: PluginType},
		ds:             ds,
		state:          make(map[string]*modelState),
		windowDuration: defaultWindowDuration,
	}
}

func (e *RequestMetadataExtractor) TypedName() plugin.TypedName { return e.typedName }

// WithName sets the instance name, used by the factory when the plugin is configured by name.
func (e *RequestMetadataExtractor) WithName(name string) *RequestMetadataExtractor {
	e.typedName.Name = name
	return e
}

// WithWindowDuration overrides the aggregation window. Pass 0 to flush after every response
func (e *RequestMetadataExtractor) WithWindowDuration(d time.Duration) *RequestMetadataExtractor {
	e.windowDuration = d
	return e
}

func (e *RequestMetadataExtractor) Extract(_ context.Context, events []dlsrc.Event) error {
	now := time.Now()
	updated := map[string]bool{}

	for _, ev := range events {
		switch ev.Type {
		case dlsrc.RequestEventType:
			p, ok := ev.Payload.(dlsrc.RequestPayload)
			if !ok {
				continue
			}
			model, _ := p.Request.Body["model"].(string)
			if model == "" {
				continue
			}
			s := e.getOrCreate(model, now)
			s.Requests++
			updated[model] = true

		case dlsrc.ResponseEventType:
			p, ok := ev.Payload.(dlsrc.ResponsePayload)
			if !ok {
				continue
			}
			model, _ := p.Request.Body["model"].(string)
			if model == "" {
				continue
			}
			s := e.getOrCreate(model, now)
			floorDecrement(&s.Requests, 1)

			// Accumulate latency observations into the current window.
			if p.TTFT > 0 {
				s.ttftSum += p.TTFT.Seconds()
				s.ttftN++
			}
			if usage, ok := p.Response.Body["usage"].(map[string]any); ok {
				if ct, ok := usage["completion_tokens"].(float64); ok && ct > 0 {
					// Decode time only: subtract TTFT so queue wait and prefill are not mixed into TPOT.
					if decodeTime := p.Duration - p.TTFT; decodeTime > 0 {
						s.tpotSum += decodeTime.Seconds() / ct
						s.tpotN++
					}
				}
			}

			// Once the window has elapsed, average all accumulated observations and apply one EMA update.
			// windowDuration=0 means flush after every response (used in unit tests).
			if now.Sub(s.windowStart) >= e.windowDuration {
				s.flush(now)
			}
			updated[model] = true
		}
	}

	for model := range updated {
		e.ds.GetOrCreateModel(model).GetAttributes().Put(RequestMetadataAttributeKey, e.state[model].RequestMetadataCount)
	}
	return nil
}

func (e *RequestMetadataExtractor) getOrCreate(model string, now time.Time) *modelState {
	if s, ok := e.state[model]; ok {
		return s
	}
	s := &modelState{windowStart: now}
	e.state[model] = s
	return s
}

// ema applies an exponential moving average update with α = 0.1.
// If current is zero (no prior observation), the new value is returned directly.
func ema(current, newValue float64) float64 {
	if current == 0 {
		return newValue
	}
	return 0.1*newValue + 0.9*current
}

// floorDecrement decrements v by delta, flooring at zero.
func floorDecrement(v *int64, delta int64) {
	*v -= delta
	if *v < 0 {
		*v = 0
	}
}
