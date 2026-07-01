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

// Package ttftpercentile tracks per-model TTFT distributions and publishes
// P10Low, P50, and inflightAtP50 for the queue-ttft-scorer.
package ttftpercentile

import (
	"context"
	"encoding/json"
	"fmt"
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

	defaultWindowSize        = 5000
	defaultMaxObservationAge = 3 * time.Minute // observations older than this are never used
	defaultIntervalDuration  = 5 * time.Second
	defaultLowLoadWindowAge  = 1 * time.Hour
	defaultMaxRequests       = 100 // cap the short window to the most recent N observations
	defaultMinRequests       = 10  // below this count the scorer falls back to the optimistic seed
)

var _ dlsrc.Extractor = &TTFTPercentileExtractor{}

type TTFTPercentileExtractorConfig struct {
	IntervalDuration string  `json:"intervalDuration,omitempty"`
	WindowSize       int     `json:"windowSize,omitempty"`
	// MaxObservationAge caps how far back the short window looks.
	// Observations older than this are never used for P50 or short-window P10.
	MaxObservationAge string `json:"maxObservationAge,omitempty"`
	LowLoadWindowAge  string `json:"lowLoadWindowAge,omitempty"`
	// MaxRequests caps the short window to the most recent N observations regardless of age.
	MaxRequests int `json:"maxRequests,omitempty"`
	// MinRequests is the minimum capped-window count for the scorer to use the trusted
	// operating point. Below this the scorer falls back to the optimistic seed (floor only).
	MinRequests int `json:"minRequests,omitempty"`
}

// TTFTPercentileMetrics is written to each model's attribute store every intervalDuration.
type TTFTPercentileMetrics struct {
	Requests       int64
	InflightAtP50  float64 // avg inflight_at_dispatch of observations in the P40-P60 band
	P10LowTTFT     float64 // two-level P10: P10 of the bottom decile; hardware-floor estimate
	P10TTFT        float64 // P10 from capped short window
	P50TTFT        float64 // P50 from capped short window
	LastObservedAt int64
	RecentN        int // count of observations in the capped short window
	MinRequests    int // scorer threshold — copied from config so the scorer needs no separate param
}

func (m TTFTPercentileMetrics) Clone() datalayer.Cloneable { return m }

// Floor is the load-invariant service floor: P10Low, or P10 before the long window fills.
// Zero means the model is truly cold.
func (m TTFTPercentileMetrics) Floor() float64 {
	if m.P10LowTTFT > 0 {
		return m.P10LowTTFT
	}
	return m.P10TTFT
}

// Predict returns the effective TTFT at the current inflight and whether it is trusted
// (calibrated). Uncalibrated but observed → floor as an optimistic seed; cold → (0, false).
func (m TTFTPercentileMetrics) Predict() (effectiveTTFT float64, trusted bool) {
	floor := m.Floor()
	if floor == 0 {
		return 0, false
	}
	if m.RecentN >= m.MinRequests && m.InflightAtP50 > 0 && m.P50TTFT > floor {
		eff := floor + float64(m.Requests)*(m.P50TTFT-floor)/m.InflightAtP50
		if eff < floor {
			eff = floor
		}
		return eff, true
	}
	return floor, false
}

type pendingEntry struct {
	inflightAtDispatch int64
	dispatchedAt       time.Time
}

type modelPercentileState struct {
	TTFTPercentileMetrics
	intervalStart time.Time
	tracker       percentileTracker
	pending       map[string]pendingEntry
}

func (s *modelPercentileState) flush(now time.Time, maxObservationAge, lowLoadWindowAge time.Duration, maxRequests int) {
	p50, inflightAtP50, ok50 := s.tracker.quantileWithInflight(0.50, now, maxObservationAge, maxRequests)
	p10, ok10 := s.tracker.quantile(0.10, now, maxObservationAge, maxRequests)
	if ok50 {
		s.P50TTFT, s.InflightAtP50, s.LastObservedAt = p50, inflightAtP50, now.UnixNano()
	}
	if ok10 {
		s.P10TTFT = p10
	}
	if p10low, ok := s.tracker.p10Low(now, lowLoadWindowAge); ok {
		s.P10LowTTFT = p10low
	}
	s.RecentN = s.tracker.countCapped(now, maxObservationAge, maxRequests)
	s.intervalStart = now
}

type TTFTPercentileExtractor struct {
	typedName         plugin.TypedName
	ds                datalayer.Datastore
	state             map[string]*modelPercentileState
	windowSize        int
	maxObservationAge time.Duration
	intervalDuration  time.Duration
	lowLoadWindowAge  time.Duration
	maxRequests       int
	minRequests       int
}

func ExtractorFactory(name string, parameters json.RawMessage, h plugin.Handle) (plugin.Plugin, error) {
	cfg := TTFTPercentileExtractorConfig{
		IntervalDuration:  defaultIntervalDuration.String(),
		WindowSize:        defaultWindowSize,
		MaxObservationAge: defaultMaxObservationAge.String(),
		LowLoadWindowAge:  defaultLowLoadWindowAge.String(),
		MaxRequests:       defaultMaxRequests,
		MinRequests:       defaultMinRequests,
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
	interval, err := time.ParseDuration(cfg.IntervalDuration)
	if err != nil {
		return nil, fmt.Errorf("invalid intervalDuration %q for plugin %q: %w", cfg.IntervalDuration, name, err)
	}
	maxObsAge, err := time.ParseDuration(cfg.MaxObservationAge)
	if err != nil {
		return nil, fmt.Errorf("invalid maxObservationAge %q for plugin %q: %w", cfg.MaxObservationAge, name, err)
	}
	lowLoadAge, err := time.ParseDuration(cfg.LowLoadWindowAge)
	if err != nil {
		return nil, fmt.Errorf("invalid lowLoadWindowAge %q for plugin %q: %w", cfg.LowLoadWindowAge, name, err)
	}
	return NewTTFTPercentileExtractor(h.Datastore()).
		WithName(name).
		WithIntervalDuration(interval).
		WithWindow(cfg.WindowSize, maxObsAge).
		WithLowLoadWindowAge(lowLoadAge).
		WithRequestBounds(cfg.MaxRequests, cfg.MinRequests), nil
}

func NewTTFTPercentileExtractor(ds datalayer.Datastore) *TTFTPercentileExtractor {
	return &TTFTPercentileExtractor{
		typedName:         plugin.TypedName{Type: PluginType, Name: PluginType},
		ds:                ds,
		state:             make(map[string]*modelPercentileState),
		windowSize:        defaultWindowSize,
		maxObservationAge: defaultMaxObservationAge,
		intervalDuration:  defaultIntervalDuration,
		lowLoadWindowAge:  defaultLowLoadWindowAge,
		maxRequests:       defaultMaxRequests,
		minRequests:       defaultMinRequests,
	}
}

func (e *TTFTPercentileExtractor) TypedName() plugin.TypedName { return e.typedName }
func (e *TTFTPercentileExtractor) WithName(n string) *TTFTPercentileExtractor {
	e.typedName.Name = n; return e
}
func (e *TTFTPercentileExtractor) WithIntervalDuration(d time.Duration) *TTFTPercentileExtractor {
	e.intervalDuration = d; return e
}
func (e *TTFTPercentileExtractor) WithWindow(size int, maxObsAge time.Duration) *TTFTPercentileExtractor {
	e.windowSize, e.maxObservationAge = size, maxObsAge; return e
}
func (e *TTFTPercentileExtractor) WithLowLoadWindowAge(age time.Duration) *TTFTPercentileExtractor {
	e.lowLoadWindowAge = age; return e
}
func (e *TTFTPercentileExtractor) WithRequestBounds(maxN, minN int) *TTFTPercentileExtractor {
	e.maxRequests, e.minRequests = maxN, minN; return e
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
			if reqID := p.Request.Headers["x-request-id"]; reqID != "" {
				s.pending[reqID] = pendingEntry{
					inflightAtDispatch: inflight,
					dispatchedAt:       now,
				}
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
			reqID := p.Request.Headers["x-request-id"]
			if p.TTFT > 0 {
				ttft := p.TTFT.Seconds()
				var inflightAtDispatch int64
				if entry, found := s.pending[reqID]; found && reqID != "" {
					inflightAtDispatch = entry.inflightAtDispatch
				}
				s.tracker.add(ttft, inflightAtDispatch, now)
				debugLogger.Info("ttft-observation",
					"model", model, "ttft_s", ttft, "inflightAtDispatch", inflightAtDispatch,
				)
			}
			if reqID != "" {
				delete(s.pending, reqID)
			}
			if now.Sub(s.intervalStart) >= e.intervalDuration {
				s.flush(now, e.maxObservationAge, e.lowLoadWindowAge, e.maxRequests)
			}
			updated[model] = true
		}
	}

	for model := range updated {
		s := e.state[model]
		cutoff := now.Add(-e.maxObservationAge)
		for id, entry := range s.pending {
			if entry.dispatchedAt.Before(cutoff) {
				delete(s.pending, id)
			}
		}
		m := s.TTFTPercentileMetrics
		m.MinRequests = e.minRequests
		e.ds.GetOrCreateModel(model).GetAttributes().Put(AttributeKey, m)

		eff, _ := m.Predict()
		debugLogger.Info("ttft-percentile wrote attribute",
			"model", model, "Requests", m.Requests,
			"InflightAtP50", m.InflightAtP50, "P10Low_s", m.P10LowTTFT,
			"P10_s", m.P10TTFT, "P50_s", m.P50TTFT, "EffectiveTTFT_s", eff,
			"RecentN", m.RecentN, "MinRequests", m.MinRequests,
		)
	}
	return nil
}

func (e *TTFTPercentileExtractor) getOrCreate(model string) *modelPercentileState {
	if s, ok := e.state[model]; ok {
		return s
	}
	s := &modelPercentileState{
		tracker: newSlidingWindowTracker(e.windowSize),
		pending: make(map[string]pendingEntry),
	}
	e.state[model] = s
	return s
}
