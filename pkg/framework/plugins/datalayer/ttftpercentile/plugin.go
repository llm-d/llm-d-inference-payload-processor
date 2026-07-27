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

// Package ttftpercentile observes per-model TTFTs and publishes a TTFTPercentileMetrics snapshot
// (the service floor P10Low, the P25/P50 operating points and their inflight anchors) to each
// model's attribute store for the ttft-aware scorer to consume.
package ttftpercentile

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log"

	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	dlsrc "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer/datasource"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
)

const (
	PluginType   = "ttft-percentile-extractor"
	AttributeKey = "ttft-percentile"

	// inflightAtDispatchKey carries a request's inflight-at-dispatch through CycleState
	// from the request event to the matching response event.
	inflightAtDispatchKey = "ttft-percentile/inflight-at-dispatch"

	// requestIDHeaderKey is the request-id request header, logged with each ttft-observation so
	// an offline analysis can pair the actual TTFT with the scorer's predicted effectiveTTFT
	// (which logs the same id via the request-context logger). Debug-only.
	requestIDHeaderKey = "x-request-id"

	// how many past observations to keep per pool - the size of the notebook
	defaultWindowSize = 5000
	// how far back "recent" reaches for the operating point - what counts as "now"
	defaultMaxObservationAge = 3 * time.Minute
	// how often the summary percentiles are recomputed - observations stream in continuously,
	// they are only refreshed on this cadence
	defaultIntervalDuration = 1 * time.Second
	// even inside the time window, use only the newest N observations - a cap on top of a cap
	defaultMaxRequests = 100
	// how much evidence before we trust a pool's operating point - the "do I trust this yet?" threshold
	defaultMinRequests = 10

	// Floor (P10Low) is the P10 of a bounded history of per-bucket P10s; when the history is
	// full, the smallest and largest entries are evicted (by value, not by age).
	//
	// how much time makes up one floor sample - the floor takes one P10 per bucket
	defaultBucketDuration = 1 * time.Minute
	// how many per-bucket P10s to remember for the floor
	defaultBucketHistorySize = 1000
)

var _ dlsrc.Extractor = &TTFTPercentileExtractor{}

type TTFTPercentileExtractorConfig struct {
	IntervalDuration string `json:"intervalDuration,omitempty"`
	WindowSize       int    `json:"windowSize,omitempty"`
	// MaxObservationAge caps how far back the short window looks.
	// Observations older than this are never used for P50 or short-window P10.
	MaxObservationAge string `json:"maxObservationAge,omitempty"`
	// MaxRequests caps the short window to the most recent N observations regardless of age.
	MaxRequests int `json:"maxRequests,omitempty"`
	// MinRequests is the minimum capped-window count for the scorer to use the trusted
	// operating point. Below this the scorer falls back to the optimistic seed (floor only).
	MinRequests int `json:"minRequests,omitempty"`
	// BucketDuration is the window over which each floor-history entry's P10 is computed.
	// Defaults to 1m. Keep it <= maxObservationAge so the tracker retains a full bucket.
	BucketDuration string `json:"bucketDuration,omitempty"`
	// BucketHistorySize caps the number of per-bucket P10s kept for the floor. Entries are evicted
	// by value (smallest and largest) when full, not by age, so this bounds memory and smooths the
	// floor rather than setting a fixed time horizon; a permanent shift works in gradually. Default 1000.
	BucketHistorySize int `json:"bucketHistorySize,omitempty"`
}

// TTFTPercentileMetrics is written to each model's attribute store every intervalDuration.
type TTFTPercentileMetrics struct {
	Requests       int64
	InflightAtP25  float64 // avg inflight_at_dispatch of observations in the P15-P35 band
	InflightAtP50  float64 // avg inflight_at_dispatch of observations in the P40-P60 band
	P10LowTTFT     float64 // P10 of the per-bucket P10 history; load-invariant floor estimate
	P10TTFT        float64 // P10 from capped short window
	P25TTFT        float64 // P25 from capped short window
	P50TTFT        float64 // P50 from capped short window
	LastObservedAt int64
	RecentN        int   // count of observations in the capped short window
	Observations   int64 // cumulative observations feeding the floor; gates Floor until >= MinRequests
	MinRequests    int   // scorer threshold — copied from config so the scorer needs no separate param
}

func (m TTFTPercentileMetrics) Clone() datalayer.Cloneable { return m }

// Floor is the load-invariant service floor: P10Low, or P10 before the history fills.
// Zero means the model is cold. A model observed fewer than MinRequests times is also
// treated as cold: its P10 is percentile-noise from a handful of cold-start requests, so
// returning it would let a barely-observed pool compete on a poisoned value.
func (m TTFTPercentileMetrics) Floor() float64 {
	if m.Observations < int64(m.MinRequests) {
		return 0
	}
	if m.P10LowTTFT > 0 {
		return m.P10LowTTFT
	}
	return m.P10TTFT
}

type modelPercentileState struct {
	TTFTPercentileMetrics
	intervalStart time.Time
	bucketStart   time.Time
	lastTouched   time.Time // last event seen for this model; drives stale-state eviction
	bucketP10s    []float64 // bounded history of per-bucket P10s, kept value-sorted
	tracker       *slidingWindowTracker
}

func (s *modelPercentileState) flush(now time.Time, maxObservationAge, bucketDuration time.Duration, maxRequests, bucketHistorySize int) {
	// Short window (value-sorted, capped): one snapshot feeds P10, P50 and inflightAtP50.
	// Recomputed every interval to keep the operating point fresh.
	short := s.tracker.window(now, maxObservationAge, maxRequests)
	s.RecentN = len(short)
	if len(short) > 0 {
		s.P10TTFT = percentileOf(short, 0.10)
		s.P25TTFT = percentileOf(short, 0.25)
		s.P50TTFT = percentileOf(short, 0.50)
		s.InflightAtP25 = bandInflight(short, 0.25)
		s.InflightAtP50 = bandInflight(short, 0.50)
		s.LastObservedAt = now.UnixNano()
	}
	s.intervalStart = now

	// Floor: once per bucket, add that bucket's P10 to the history and set the floor to the
	// P10 of the history. The history is sorted here (needed for the percentile anyway), so
	// when it is full its smallest and largest entries are the ends and get evicted — dropping
	// anomalous buckets rather than the oldest.
	if s.bucketStart.IsZero() {
		s.bucketStart = now
	}
	if now.Sub(s.bucketStart) >= bucketDuration {
		if bucket := s.tracker.window(now, bucketDuration, 0); len(bucket) > 0 {
			s.bucketP10s = append(s.bucketP10s, percentileOf(bucket, 0.10))
			sort.Float64s(s.bucketP10s)
			if len(s.bucketP10s) > bucketHistorySize {
				s.bucketP10s = dropMinMax(s.bucketP10s)
			}
			s.P10LowTTFT = percentileFloat64(s.bucketP10s, 0.10)
		}
		s.bucketStart = now
	}
}

type TTFTPercentileExtractor struct {
	typedName         plugin.TypedName
	ds                datalayer.Datastore
	state             map[string]*modelPercentileState
	windowSize        int
	maxObservationAge time.Duration
	intervalDuration  time.Duration
	maxRequests       int
	minRequests       int
	bucketDuration    time.Duration
	bucketHistorySize int
	lastSweep         time.Time // last stale-state eviction pass; throttles the sweep
}

func ExtractorFactory(name string, parameters json.RawMessage, h plugin.Handle) (plugin.Plugin, error) {
	cfg := TTFTPercentileExtractorConfig{
		IntervalDuration:  defaultIntervalDuration.String(),
		WindowSize:        defaultWindowSize,
		MaxObservationAge: defaultMaxObservationAge.String(),
		MaxRequests:       defaultMaxRequests,
		MinRequests:       defaultMinRequests,
		BucketDuration:    defaultBucketDuration.String(),
		BucketHistorySize: defaultBucketHistorySize,
	}
	if len(parameters) > 0 {
		if err := json.Unmarshal(parameters, &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse parameters for plugin %q: %w", name, err)
		}
	}
	if cfg.WindowSize <= 0 {
		return nil, fmt.Errorf("windowSize must be > 0 for plugin %q", name)
	}
	if cfg.MaxRequests <= 0 {
		return nil, fmt.Errorf("maxRequests must be > 0 for plugin %q", name)
	}
	if cfg.MinRequests <= 0 {
		return nil, fmt.Errorf("minRequests must be > 0 for plugin %q", name)
	}
	if cfg.BucketHistorySize < 2 {
		return nil, fmt.Errorf("bucketHistorySize must be >= 2 for plugin %q", name)
	}
	interval, err := time.ParseDuration(cfg.IntervalDuration)
	if err != nil {
		return nil, fmt.Errorf("invalid intervalDuration %q for plugin %q: %w", cfg.IntervalDuration, name, err)
	}
	if interval <= 0 {
		return nil, fmt.Errorf("intervalDuration must be > 0 for plugin %q", name)
	}
	maxObsAge, err := time.ParseDuration(cfg.MaxObservationAge)
	if err != nil {
		return nil, fmt.Errorf("invalid maxObservationAge %q for plugin %q: %w", cfg.MaxObservationAge, name, err)
	}
	if maxObsAge <= 0 {
		return nil, fmt.Errorf("maxObservationAge must be > 0 for plugin %q", name)
	}
	bucketDur, err := time.ParseDuration(cfg.BucketDuration)
	if err != nil {
		return nil, fmt.Errorf("invalid bucketDuration %q for plugin %q: %w", cfg.BucketDuration, name, err)
	}
	if bucketDur <= 0 {
		return nil, fmt.Errorf("bucketDuration must be > 0 for plugin %q", name)
	}
	return NewTTFTPercentileExtractor(h.Datastore()).
		WithName(name).
		WithIntervalDuration(interval).
		WithWindow(cfg.WindowSize, maxObsAge).
		WithRequestBounds(cfg.MaxRequests, cfg.MinRequests).
		WithFloorBuckets(bucketDur, cfg.BucketHistorySize), nil
}

func NewTTFTPercentileExtractor(ds datalayer.Datastore) *TTFTPercentileExtractor {
	return &TTFTPercentileExtractor{
		typedName:         plugin.TypedName{Type: PluginType, Name: PluginType},
		ds:                ds,
		state:             make(map[string]*modelPercentileState),
		windowSize:        defaultWindowSize,
		maxObservationAge: defaultMaxObservationAge,
		intervalDuration:  defaultIntervalDuration,
		maxRequests:       defaultMaxRequests,
		minRequests:       defaultMinRequests,
		bucketDuration:    defaultBucketDuration,
		bucketHistorySize: defaultBucketHistorySize,
	}
}

func (e *TTFTPercentileExtractor) TypedName() plugin.TypedName { return e.typedName }
func (e *TTFTPercentileExtractor) WithName(n string) *TTFTPercentileExtractor {
	e.typedName.Name = n
	return e
}
func (e *TTFTPercentileExtractor) WithIntervalDuration(d time.Duration) *TTFTPercentileExtractor {
	e.intervalDuration = d
	return e
}
func (e *TTFTPercentileExtractor) WithWindow(size int, maxObsAge time.Duration) *TTFTPercentileExtractor {
	e.windowSize, e.maxObservationAge = size, maxObsAge
	return e
}
func (e *TTFTPercentileExtractor) WithRequestBounds(maxN, minN int) *TTFTPercentileExtractor {
	e.maxRequests, e.minRequests = maxN, minN
	return e
}
func (e *TTFTPercentileExtractor) WithFloorBuckets(dur time.Duration, historySize int) *TTFTPercentileExtractor {
	e.bucketDuration, e.bucketHistorySize = dur, historySize
	return e
}

func (e *TTFTPercentileExtractor) Extract(ctx context.Context, events []dlsrc.Event) error {
	debugLogger := log.FromContext(ctx).V(logutil.DEBUG)
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
			s := e.getOrCreate(model)
			inflight := s.Requests
			s.Requests++
			// Stash inflight-at-dispatch in CycleState; the matching response event reads it back.
			if p.CycleState != nil {
				p.CycleState.Write(inflightAtDispatchKey, inflight)
			}
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
			s := e.getOrCreate(model)
			if s.Requests--; s.Requests < 0 {
				s.Requests = 0
			}
			if p.TTFT > 0 && p.CycleState != nil {
				// Record the observation only when its inflight-at-dispatch resolves. A missing
				// CycleState or key would default to 0, dragging inflightAtP25/P50 downward and
				// making the model look less loaded than it was — so skip it rather than record a 0.
				if inflightAtDispatch, err := plugin.ReadCycleStateKey[int64](p.CycleState, inflightAtDispatchKey); err == nil {
					ttft := p.TTFT.Seconds()
					s.tracker.add(ttft, inflightAtDispatch, now)
					s.Observations++
					if debugLogger.Enabled() {
						debugLogger.Info("ttft-observation",
							"x-request-id", p.Request.Headers[requestIDHeaderKey],
							"model", model, "ttft_s", ttft, "inflightAtDispatch", inflightAtDispatch,
						)
					}
				}
			}
			if now.Sub(s.intervalStart) >= e.intervalDuration {
				s.flush(now, e.maxObservationAge, e.bucketDuration, e.maxRequests, e.bucketHistorySize)
			}
			updated[model] = true
		}
	}

	for model := range updated {
		s := e.state[model]
		s.lastTouched = now
		m := s.TTFTPercentileMetrics
		m.MinRequests = e.minRequests
		e.ds.GetOrCreateModel(model).GetAttributes().Put(AttributeKey, m)

		if debugLogger.Enabled() {
			// Log the raw published anchors only. The prediction (effectiveTTFT) is the scorer's
			// job and is logged there; the extractor must not carry a second, divergent formula.
			debugLogger.Info("ttft-percentile wrote attribute",
				"model", model, "Requests", m.Requests,
				"InflightAtP25", m.InflightAtP25, "InflightAtP50", m.InflightAtP50,
				"P10Low_s", m.P10LowTTFT, "P10_s", m.P10TTFT, "P25_s", m.P25TTFT,
				"P50_s", m.P50TTFT,
				"RecentN", m.RecentN, "Observations", m.Observations, "MinRequests", m.MinRequests,
			)
		}
	}
	e.evictStale(now)
	return nil
}

// evictStale reclaims state for models not seen within the idle window (bucketDuration *
// bucketHistorySize), so churned or decommissioned model names do not leak. It is throttled to
// once per interval. Eviction keys on lastTouched (any request or response event), NOT the inflight
// counter: a request whose response never arrives (client disconnect, upstream error, dropped
// Notify) leaves Requests > 0 forever, which would otherwise pin the model permanently. A model
// that later reappears is re-created cold and re-calibrates.
func (e *TTFTPercentileExtractor) evictStale(now time.Time) {
	if now.Sub(e.lastSweep) < e.intervalDuration {
		return
	}
	e.lastSweep = now
	idleWindow := e.bucketDuration * time.Duration(e.bucketHistorySize)
	for model, s := range e.state {
		if !s.lastTouched.IsZero() && now.Sub(s.lastTouched) > idleWindow {
			delete(e.state, model)
		}
	}
}

func (e *TTFTPercentileExtractor) getOrCreate(model string) *modelPercentileState {
	if s, ok := e.state[model]; ok {
		return s
	}
	s := &modelPercentileState{
		tracker: newSlidingWindowTracker(e.windowSize),
	}
	e.state[model] = s
	return s
}
