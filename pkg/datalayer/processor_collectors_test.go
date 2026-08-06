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
	"sync/atomic"
	"testing"
	"time"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
)

// fakeCollector counts how many times Poll has been called.
type fakeCollector struct {
	name      string
	freq      time.Duration
	pollCount atomic.Int64
}

func (f *fakeCollector) TypedName() plugin.TypedName {
	return plugin.TypedName{Type: "fake-collector", Name: f.name}
}

func (f *fakeCollector) Poll(_ context.Context) (any, error) {
	f.pollCount.Add(1)
	return nil, nil
}

func (f *fakeCollector) CollectorFrequency() time.Duration { return f.freq }

// TestStart_AlreadyStarted verifies that calling Start on an already-running Processor returns an error.
func TestStart_AlreadyStarted(t *testing.T) {
	proc := NewProcessor()
	ctx := context.Background()
	if err := proc.Start(ctx); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	defer proc.Stop()

	if err := proc.Start(ctx); err == nil {
		t.Error("expected error on second Start, got nil")
	}
}

// TestStart_AfterStop verifies that calling Start after Stop returns an error.
func TestStart_AfterStop(t *testing.T) {
	proc := NewProcessor()
	proc.Stop()
	if err := proc.Start(context.Background()); err == nil {
		t.Error("expected error when calling Start after Stop, got nil")
	}
}

// TestRegisterCollector_InvalidFrequency verifies that a non-positive frequency is rejected and the collector is not registered.
func TestRegisterCollector_InvalidFrequency(t *testing.T) {
	proc := NewProcessor()
	c := &fakeCollector{name: "c", freq: 0}
	proc.RegisterCollector(c, 0)
	proc.RegisterCollector(c, -1*time.Second)

	if len(proc.collectors) != 0 {
		t.Errorf("expected 0 collectors after invalid registrations, got %d", len(proc.collectors))
	}
}

// TestRegisterCollectorAfterStart verifies that a collector registered after Start is still polled.
func TestRegisterCollectorAfterStart(t *testing.T) {
	proc := NewProcessor()
	pre := &fakeCollector{name: "pre", freq: 10 * time.Millisecond}
	proc.RegisterCollector(pre, 10*time.Millisecond)

	if err := proc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	post := &fakeCollector{name: "post", freq: 10 * time.Millisecond}
	proc.RegisterCollector(post, 10*time.Millisecond)

	time.Sleep(100 * time.Millisecond)
	proc.Stop()

	if got := post.pollCount.Load(); got < 1 {
		t.Errorf("expected post-start collector to be polled at least once, got %d", got)
	}
}
