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

package datalayer

import (
	"context"
	"time"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer/datasource"
)

// FakeProcessor is a test implementation of datasource.DatalayerProcessor
// that tracks registered components without actually running them.
type FakeProcessor struct {
	extractors  []datasource.Extractor
	collectors  []datasource.Collector
	datasources []datasource.DataSource
	events      []datalayer.Event
}

// NewFakeProcessor creates a new FakeProcessor for testing.
func NewFakeProcessor() *FakeProcessor {
	return &FakeProcessor{
		extractors:  make([]datasource.Extractor, 0),
		collectors:  make([]datasource.Collector, 0),
		datasources: make([]datasource.DataSource, 0),
		events:      make([]datalayer.Event, 0),
	}
}

// Notify records an event.
func (f *FakeProcessor) Notify(e datalayer.Event) {
	f.events = append(f.events, e)
}

// RegisterExtractor records an extractor.
func (f *FakeProcessor) RegisterExtractor(e datasource.Extractor) {
	f.extractors = append(f.extractors, e)
}

// RegisterCollector records a collector.
func (f *FakeProcessor) RegisterCollector(c datasource.Collector, frequency time.Duration) {
	f.collectors = append(f.collectors, c)
}

// RegisterDatasource records a datasource.
func (f *FakeProcessor) RegisterDatasource(d datasource.DataSource) {
	f.datasources = append(f.datasources, d)
}

// Start is a no-op for the fake processor.
func (f *FakeProcessor) Start(ctx context.Context) error {
	return nil
}

// Stop is a no-op for the fake processor.
func (f *FakeProcessor) Stop() {
}

// GetExtractors returns the registered extractors for test assertions.
func (f *FakeProcessor) GetExtractors() []datasource.Extractor {
	return f.extractors
}

// GetCollectors returns the registered collectors for test assertions.
func (f *FakeProcessor) GetCollectors() []datasource.Collector {
	return f.collectors
}

// GetDatasources returns the registered datasources for test assertions.
func (f *FakeProcessor) GetDatasources() []datasource.DataSource {
	return f.datasources
}

// GetEvents returns the recorded events for test assertions.
func (f *FakeProcessor) GetEvents() []datalayer.Event {
	return f.events
}
