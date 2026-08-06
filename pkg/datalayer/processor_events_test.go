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
	"testing"
	"time"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer/datasource"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
)

// fakeExtractor is a minimal Extractor for testing.
type fakeExtractor struct {
	name   string
	events []datasource.Event
}

func (e *fakeExtractor) TypedName() plugin.TypedName {
	return plugin.TypedName{Type: "fake-extractor", Name: e.name}
}
func (e *fakeExtractor) Extract(_ context.Context, evts []datasource.Event) error {
	e.events = append(e.events, evts...)
	return nil
}

// TestProcessorNotify verifies that a Notify call delivers the event to registered extractors.
func TestProcessorNotify(t *testing.T) {
	proc := NewProcessor()
	ext := &fakeExtractor{name: "ext-a"}
	proc.RegisterExtractor(ext)

	if err := proc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	proc.Notify(datasource.Event{Type: datasource.RequestEventType})

	// Give the event loop time to dispatch.
	time.Sleep(20 * time.Millisecond)
	proc.Stop()

	if len(ext.events) != 1 {
		t.Errorf("expected 1 event, got %d", len(ext.events))
	}
}

// TestProcessorMultipleExtractors verifies events are delivered to all extractors.
func TestProcessorMultipleExtractors(t *testing.T) {
	proc := NewProcessor()
	ext1 := &fakeExtractor{name: "ext-a"}
	ext2 := &fakeExtractor{name: "ext-b"}
	proc.RegisterExtractor(ext1)
	proc.RegisterExtractor(ext2)

	if err := proc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	proc.Notify(datasource.Event{Type: datasource.ResponseEventType})

	time.Sleep(20 * time.Millisecond)
	proc.Stop()

	if len(ext1.events) != 1 {
		t.Errorf("ext1: expected 1 event, got %d", len(ext1.events))
	}
	if len(ext2.events) != 1 {
		t.Errorf("ext2: expected 1 event, got %d", len(ext2.events))
	}
}
