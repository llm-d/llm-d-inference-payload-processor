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

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/datastore"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/datalayer"
	dlsrc "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/datalayer/datasource"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
)

const (
	// PluginType is the identifier used when registering this extractor.
	PluginType = "request-metadata-extractor"

	// RequestMetadataAttributeKey is the attribute key written to each model's attribute store.
	RequestMetadataAttributeKey = "request-metadata"
)

// compile-time interface assertion
var _ dlsrc.Extractor = &RequestMetadataExtractor{}

// ExtractorFactory creates a RequestMetadataExtractor with a nil DataStore.
// The factory path is limited: the DataStore is not available via plugin.Handle,
// so the created extractor cannot write to the store. Use NewRequestMetadataExtractor
// directly when constructing for production use.
func ExtractorFactory(name string, _ json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
	return NewRequestMetadataExtractor(nil).WithName(name), nil
}

// RequestMetadataCount holds in-flight request and token counts for one model.
type RequestMetadataCount struct {
	Requests int64
	Tokens   int64
}

func (r RequestMetadataCount) Clone() datalayer.Cloneable { return r }

// RequestMetadataExtractor tracks in-flight request counts and token sums per model.
// It writes RequestMetadataCount to each model's RequestMetadataAttributeKey attribute.
//
// Extract is assumed to be called from a single goroutine (the NotificationSource event loop).
// If parallel dispatch is introduced, add a sync.Mutex around counters and the DataStore write.
//
// TODO: counters leak if a request fails without a corresponding ResponseEventType (e.g. connection
// drop, upstream error, context cancellation). The call site should fire a
// synthetic ResponseEventType in its error/EOF path to keep counts accurate.
type RequestMetadataExtractor struct {
	typedName plugin.TypedName
	ds        datastore.Datastore
	counters  map[string]RequestMetadataCount
}

func NewRequestMetadataExtractor(ds datastore.Datastore) *RequestMetadataExtractor {
	return &RequestMetadataExtractor{
		typedName: plugin.TypedName{Type: PluginType, Name: PluginType},
		ds:        ds,
		counters:  make(map[string]RequestMetadataCount),
	}
}

func (e *RequestMetadataExtractor) TypedName() plugin.TypedName { return e.typedName }

// WithName sets the instance name, used by the factory when the plugin is configured by name.
func (e *RequestMetadataExtractor) WithName(name string) *RequestMetadataExtractor {
	e.typedName.Name = name
	return e
}

func (e *RequestMetadataExtractor) Extract(_ context.Context, events []dlsrc.Event) error {
	updated := map[string]RequestMetadataCount{}

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
			maxTokens, _ := p.Request.Body["max_tokens"].(float64)
			c := e.counters[model]
			c.Requests++
			c.Tokens += int64(maxTokens)
			e.counters[model] = c
			updated[model] = c

		case dlsrc.ResponseEventType:
			p, ok := ev.Payload.(dlsrc.ResponsePayload)
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
		e.ds.GetOrCreateModel(model).GetAttributes().Put(RequestMetadataAttributeKey, c)
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
