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
	"errors"
	"math/rand/v2"
	"sync"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer/datasource"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
)

const processorName = "datalayer-processor"

const defaultBufferSize = 10000

// compile-time interface assertions
var _ datasource.DatalayerProcessor = &Processor{}
var _ datasource.EventNotifier = &Processor{}

type collectorEntry struct {
	collector datasource.Collector
	frequency time.Duration
}

// Processor is the unified datalayer component. It drives one polling goroutine
// per registered Collector and runs a single event loop that dispatches events
// to all registered Extractors.
// RegisterExtractor must be called before Start.
// RegisterCollector may be called before or after Start.
// Start and Stop must each be called at most once.
type Processor struct {
	name plugin.TypedName

	// event notification
	ch         chan datasource.Event
	extractors []datasource.Extractor
	notifyDone chan struct{}

	// polling
	collectors  []collectorEntry
	datasources []datasource.DataSource
	mu          sync.Mutex

	// lifecycle
	started bool
	stopped bool
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// NewProcessor creates a Processor with no collectors or extractors registered.
func NewProcessor() *Processor {
	return &Processor{
		name:       plugin.TypedName{Type: processorName, Name: processorName},
		ch:         make(chan datasource.Event, defaultBufferSize),
		notifyDone: make(chan struct{}),
	}
}

func (p *Processor) TypedName() plugin.TypedName { return p.name }

// RegisterExtractor adds an Extractor to receive events. Must be called before Start.
func (p *Processor) RegisterExtractor(e datasource.Extractor) {
	if p.started {
		log.Log.Error(nil, "processor: RegisterExtractor called after Start; ignoring",
			"extractor", e.TypedName())
		return
	}
	p.extractors = append(p.extractors, e)
}

// RegisterCollector adds a Collector to be polled at the given frequency.
// Safe to call before or after Start. No-op for the goroutine if called after Stop.
// Logs an error and skips registration if frequency is not positive.
func (p *Processor) RegisterCollector(c datasource.Collector, frequency time.Duration) {
	if frequency <= 0 {
		log.Log.Error(nil, "processor: skipping collector with non-positive frequency",
			"collector", c.TypedName(), "frequency", frequency)
		return
	}
	p.mu.Lock()
	p.collectors = append(p.collectors, collectorEntry{collector: c, frequency: frequency})
	if p.started && !p.stopped {
		p.wg.Add(1)
		go p.runCollector(p.ctx, c, frequency, frequency)
	}
	p.mu.Unlock()
}

// RegisterDatasource adds a DataSource. If the Processor is already running,
// the DataSource is started immediately and stopped when the Processor stops.
// If called before Start, it will be started as part of Start.
func (p *Processor) RegisterDatasource(d datasource.DataSource) {
	p.mu.Lock()
	p.datasources = append(p.datasources, d)
	started := p.started && !p.stopped
	ctx := p.ctx
	p.mu.Unlock()

	if started {
		p.wg.Add(1)
		go p.runDatasource(ctx, d)
	}
}

// Notify fires an event non-blocking; drops silently if the buffer is full.
func (p *Processor) Notify(e datasource.Event) {
	select {
	case p.ch <- e:
	default:
	}
}

// Start launches the event loop and one polling goroutine per registered Collector.
// Returns an error if called more than once or after Stop.
func (p *Processor) Start(ctx context.Context) error {
	p.mu.Lock()
	if p.started {
		p.mu.Unlock()
		return errors.New("Processor already started")
	}
	if p.stopped {
		p.mu.Unlock()
		return errors.New("Processor already stopped")
	}
	ctx, p.cancel = context.WithCancel(ctx)
	p.ctx = ctx
	p.started = true
	snapshot := make([]collectorEntry, len(p.collectors))
	copy(snapshot, p.collectors)
	dsSnapshot := make([]datasource.DataSource, len(p.datasources))
	copy(dsSnapshot, p.datasources)
	p.mu.Unlock()

	// Start event loop goroutine.
	ready := make(chan struct{})
	go p.eventLoop(ctx, ready)
	<-ready

	// Spread collector startups: allow at most 5 per second on average,
	// capped at each collector's own frequency so no collector waits longer
	// than one full interval before its first poll.
	maxJitter := time.Duration(len(snapshot)) * 200 * time.Millisecond
	for _, entry := range snapshot {
		jitterCap := maxJitter
		if entry.frequency < jitterCap {
			jitterCap = entry.frequency
		}
		p.wg.Add(1)
		go p.runCollector(ctx, entry.collector, entry.frequency, jitterCap)
	}

	for _, d := range dsSnapshot {
		p.wg.Add(1)
		go p.runDatasource(ctx, d)
	}
	return nil
}

// Stop cancels all goroutines and waits for them to exit.
func (p *Processor) Stop() {
	p.mu.Lock()
	p.stopped = true
	cancel := p.cancel
	p.mu.Unlock()

	if cancel != nil {
		cancel()
		p.wg.Wait()
		<-p.notifyDone
	}
}

func (p *Processor) eventLoop(ctx context.Context, ready chan struct{}) {
	close(ready)

	logger := log.FromContext(ctx).WithName("processor")

	for {
		select {
		case <-ctx.Done():
			close(p.notifyDone)
			return
		case e := <-p.ch:
			for _, ext := range p.extractors {
				if err := ext.Extract(ctx, []datasource.Event{e}); err != nil {
					logger.Error(err, "extractor error", "extractor", ext.TypedName())
				}
			}
		}
	}
}

func (p *Processor) runDatasource(ctx context.Context, d datasource.DataSource) {
	defer p.wg.Done()
	logger := log.FromContext(ctx).WithName("processor").WithValues("datasource", d.TypedName())
	if err := d.Start(ctx); err != nil {
		logger.Error(err, "datasource start failed")
		return
	}
	<-ctx.Done()
	d.Stop()
}

func (p *Processor) runCollector(ctx context.Context, c datasource.Collector, freq, jitterCap time.Duration) {
	defer p.wg.Done()
	logger := log.FromContext(ctx).WithName("processor").
		WithValues("collector", c.TypedName(), "frequency", freq)

	poll := func() {
		if _, err := c.Poll(ctx); err != nil {
			logger.Error(err, "collector poll failed")
		}
	}

	// Spread initial polls across [0, jitterCap) to avoid thundering herd.
	if jitterCap > 0 {
		jitter := time.Duration(rand.Int64N(int64(jitterCap)))
		select {
		case <-ctx.Done():
			return
		case <-time.After(jitter):
		}
	}

	poll()

	ticker := time.NewTicker(freq)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			poll()
		}
	}
}
