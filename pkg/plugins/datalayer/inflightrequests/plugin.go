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

package inflightrequests

import (
	"context"
	"encoding/json"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/datalayer"
)

const (
	// PluginType is the identifier used when registering this extractor.
	PluginType = "inflight-requests-extractor"

	// InflightRequestsAttributeKey is the attribute key written to each model's attribute store.
	InflightRequestsAttributeKey = "inflight-requests"
)

// compile-time interface assertion
var _ datalayer.Extractor = &InflightRequestsExtractor{}

// ExtractorFactory creates a InflightRequestsExtractor with a nil DataStore.
// The factory path is limited: the DataStore is not available via framework.Handle,
// so the created extractor cannot write to the store. Use NewInflightRequestsExtractor
// directly when constructing for production use.
func ExtractorFactory(name string, _ json.RawMessage, _ framework.Handle) (framework.Plugin, error) {
	return NewInflightRequestsExtractor(nil).WithName(name), nil
}

// InflightRequestsCount holds in-flight request and token counts for one model.
type InflightRequestsCount struct {
	Requests int64
	Tokens   int64
}

func (r InflightRequestsCount) Clone() datalayer.Cloneable { return r }

// InflightRequestsExtractor tracks in-flight request counts and token sums per model.
// It writes InflightRequestsCount to each model's InflightRequestsAttributeKey attribute.
//
// Extract is assumed to be called from a single goroutine (the NotificationSource event loop).
// If parallel dispatch is introduced, add a sync.Mutex around counters and the DataStore write.
//
// TODO: counters leak if a request fails without a corresponding ResponseEventType (e.g. connection
// drop, upstream error, context cancellation). The call site should fire a
// synthetic ResponseEventType in its error/EOF path to keep counts accurate.
type InflightRequestsExtractor struct {
	typedName framework.TypedName
	dataStore datalayer.DataStore
	counters  map[string]InflightRequestsCount
}

func NewInflightRequestsExtractor(ds datalayer.DataStore) *InflightRequestsExtractor {
	return &InflightRequestsExtractor{
		typedName: framework.TypedName{Type: PluginType, Name: PluginType},
		dataStore: ds,
		counters:  make(map[string]InflightRequestsCount),
	}
}

func (e *InflightRequestsExtractor) TypedName() framework.TypedName { return e.typedName }

// WithName sets the instance name, used by the factory when the plugin is configured by name.
func (e *InflightRequestsExtractor) WithName(name string) *InflightRequestsExtractor {
	e.typedName.Name = name
	return e
}

func (e *InflightRequestsExtractor) Extract(_ context.Context, events []datalayer.Event) error {
	updated := map[string]InflightRequestsCount{}

	for _, ev := range events {
		switch ev.Type {
		case datalayer.RequestEventType:
			p, ok := ev.Payload.(datalayer.RequestPayload)
			if !ok {
				continue
			}
			model, _ := p.Request.Body["model"].(string)
			if model == "" {
				continue
			}
			maxTokens, _ := p.Request.Body["max_tokens"].(float64)
			c := e.counters[model]
			c.Requests++
			c.Tokens += int64(maxTokens)
			e.counters[model] = c
			updated[model] = c

		case datalayer.ResponseEventType:
			p, ok := ev.Payload.(datalayer.ResponsePayload)
			if !ok {
				continue
			}
			model, _ := p.Request.Body["model"].(string)
			if model == "" {
				continue
			}
			maxTokens, _ := p.Request.Body["max_tokens"].(float64)
			c := e.counters[model]
			floorDecrement(&c.Requests, 1)
			floorDecrement(&c.Tokens, int64(maxTokens))
			e.counters[model] = c
			updated[model] = c
		}
	}

	for model, c := range updated {
		e.dataStore.GetOrCreateModel(model).GetAttributes().Put(InflightRequestsAttributeKey, c)
	}
	return nil
}

// floorDecrement decrements v by delta, flooring at zero.
func floorDecrement(v *int64, delta int64) {
	*v -= delta
	if *v < 0 {
		*v = 0
	}
}
