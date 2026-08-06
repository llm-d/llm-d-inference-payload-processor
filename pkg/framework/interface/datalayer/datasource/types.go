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

package datasource

import (
	"context"
	"time"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
)

// DataSource is the base interface for background data layer components.
type DataSource interface {
	plugin.Plugin
	Start(ctx context.Context) error
	// Stop signals the component to shut down and blocks until it has fully stopped.
	Stop()
}

// EventType, Event, and EventNotifier are defined in the parent datalayer package
// to avoid import cycles. Alias them here for convenience.
type EventType = datalayer.EventType
type Event = datalayer.Event
type EventNotifier = datalayer.EventNotifier

const (
	RequestEventType  = datalayer.RequestEventType
	ResponseEventType = datalayer.ResponseEventType
)

// RequestPayload is the Payload for RequestEventType.
type RequestPayload struct {
	Request    *requesthandling.InferenceRequest
	CycleState *plugin.CycleState
}

// ResponsePayload is the Payload for ResponseEventType.
type ResponsePayload struct {
	Request    *requesthandling.InferenceRequest
	Response   *requesthandling.InferenceResponse
	CycleState *plugin.CycleState
	Duration   time.Duration
	TTFT       time.Duration
}

type DatalayerProcessor interface {
	EventNotifier
	RegisterExtractor(e Extractor)
	RegisterCollector(c Collector, frequency time.Duration)
	RegisterDatasource(d DataSource)
	Start(ctx context.Context) error
	Stop()
}

// Extractor processes a batch of Events. It does not manage its own goroutines.
type Extractor interface {
	plugin.Plugin
	Extract(ctx context.Context, events []Event) error
}

// A Collector is a poll mechanism to fetch data from a configured data source.
type Collector interface {
	plugin.Plugin
	Poll(ctx context.Context) (any, error)
	CollectorFrequency() time.Duration
}
