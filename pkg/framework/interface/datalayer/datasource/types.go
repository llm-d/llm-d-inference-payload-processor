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

// EventType identifies the kind of runtime event.
type EventType string

const (
	RequestEventType  EventType = "request"
	ResponseEventType EventType = "response"
)

// Event is the carrier for all data layer events.
type Event struct {
	Type    EventType
	Payload any
}

// RequestPayload is the Payload for RequestEventType.
type RequestPayload struct {
	Request *requesthandling.InferenceRequest
}

// ResponsePayload is the Payload for ResponseEventType.
type ResponsePayload struct {
	Request  *requesthandling.InferenceRequest
	Response *requesthandling.InferenceResponse
	Duration time.Duration
	TTFT     time.Duration
}

// EventNotifier is the narrow interface the producer uses to fire events.
// Keeping it separate lets the server depend only on Notify, not on lifecycle
// or extractor registration.
type EventNotifier interface {
	Notify(e Event)
}

// NotificationSource manages the background pipeline.
// It implements EventNotifier and can be passed to the producer as one.
type NotificationSource interface {
	DataSource
	EventNotifier
	RegisterExtractor(e Extractor)
}

// Extractor processes a batch of Events. It does not manage its own goroutines.
type Extractor interface {
	plugin.Plugin
	Extract(ctx context.Context, events []Event) error
}

// PollingSource is a poll-based Datasource that fetches data from various sources at regular intervals.
type PollingSource interface {
	DataSource
	RegisterCollector(c Collector, frequency time.Duration)
}

// A Collector is a poll mechanism to fetch data from a configured data source.
type Collector interface {
	plugin.Plugin
	Poll(ctx context.Context) (any, error)
}
