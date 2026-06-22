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
// P10Low, P50, and inflightAtP50 for the median-ttft-scorer.
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
	defaultWindowAge         = 1 * time.Minute
	defaultMinObservations   = 3
	defaultIntervalDuration  = 5 * time.Second
	defaultInflightEMAAlpha  = 0.2
	defaultLowLoadWindowAge  = 1 * time.Hour
)

var _ dlsrc.Extractor = &TTFTPercentileExtractor{}

type TTFTPercentileExtractorConfig struct {
	IntervalDuration string  `json:"intervalDuration,omitempty"`
	WindowSize       int     `json:"windowSize,omitempty"`
	WindowAge        string  `json:"windowAge,omitempty"`
	MinObservations  int     `json:"minObservations,omitempty"`
	InflightEMAAlpha float64 `json:"inflightEmaAlpha,omitempty"`
	LowLoadWindowAge string  `json:"lowLoadWindowAge,omitempty"`
}

// TTFTPercentileMetrics is written to each model's attribute store every intervalDuration.
type TTFTPercentileMetrics struct {
	Requests      int64
	AvgInflight   float64
	InflightAtP50 float64 // avg inflight_at_dispatch of observations in the P40-P60 band
	P10LowTTFT    float64 // two-level P10: P10 of the bottom decile; hardware-floor estimate
	P10TTFT       float64 // P10 from all obs (short window)
	P50TTFT       float64 // P50 from all obs (short window)
}

func (m TTFTPercentileMetrics) Clone() datalayer.Cloneable { return m }

type pendingEntry struct {
	inflightAtDispatch int64
	dispatchedAt       time.Time
}

type modelPercentileState struct {
	TTFTPercentileMetrics
	intervalStart   time.Time
	tracker         percentileTracker
	pending         map[string]pendingEntry
	avgInflightInit bool
}

func (s *modelPercentileState) flush(now time.Time, windowAge, lowLoadWindowAge time.Duration, minObs int) {
	p50, inflightAtP50, ok50 := s.tracker.quantileWithInflight(0.50, now, windowAge, minObs)
	p10, ok10 := s.tracker.quantile(0.10, now, windowAge, minObs)
	if ok50 {
		s.P50TTFT, s.InflightAtP50 = p50, inflightAtP50
	}
	if ok10 {
		s.P10TTFT = p10
	}
	if p10low, ok := s.tracker.p10Low(now, lowLoadWindowAge, minObs); ok {
		s.P10LowTTFT = p10low
	}
	s.intervalStart = now
}

type TTFTPercentileExtractor struct {
	typedName        plugin.TypedName
	ds               datalayer.Datastore
	state            map[string]*modelPercentileState
	windowSize       int
	windowAge        time.Duration
	minObservations  int
	intervalDuration time.Duration
	inflightEMAAlpha float64
	lowLoadWindowAge time.Duration
}

func ExtractorFactory(name string, parameters json.RawMessage, h plugin.Handle) (plugin.Plugin, error) {
	cfg := TTFTPercentileExtractorConfig{
		IntervalDuration: defaultIntervalDuration.String(), WindowSize: defaultWindowSize,
		WindowAge: defaultWindowAge.String(), MinObservations: defaultMinObservations,
		InflightEMAAlpha: defaultInflightEMAAlpha,
		LowLoadWindowAge: defaultLowLoadWindowAge.String(),
	}
	if len(parameters) > 0 {
		if err := json.Unmarshal(parameters, &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse parameters for plugin %q: %w", name, err)
		}
	}
	if cfg.WindowSize <= 0 {
		return nil, fmt.Errorf("windowSize must be > 0 for plugin %q", name)
	}
	if cfg.MinObservations <= 0 {
		return nil, fmt.Errorf("minObservations must be > 0 for plugin %q", name)
	}
	if cfg.InflightEMAAlpha <= 0 || cfg.InflightEMAAlpha > 1 {
		return nil, fmt.Errorf("inflightEmaAlpha must be in (0,1] for plugin %q", name)
	}
	interval, err := time.ParseDuration(cfg.IntervalDuration)
	if err != nil {
		return nil, fmt.Errorf("invalid intervalDuration %q for plugin %q: %w", cfg.IntervalDuration, name, err)
	}
	windowAge, err := time.ParseDuration(cfg.WindowAge)
	if err != nil {
		return nil, fmt.Errorf("invalid windowAge %q for plugin %q: %w", cfg.WindowAge, name, err)
	}
	lowLoadAge, err := time.ParseDuration(cfg.LowLoadWindowAge)
	if err != nil {
		return nil, fmt.Errorf("invalid lowLoadWindowAge %q for plugin %q: %w", cfg.LowLoadWindowAge, name, err)
	}
	return NewTTFTPercentileExtractor(h.Datastore()).
		WithName(name).WithIntervalDuration(interval).
		WithWindow(cfg.WindowSize, windowAge, cfg.MinObservations).
		WithInflightEMAAlpha(cfg.InflightEMAAlpha).
		WithLowLoadWindowAge(lowLoadAge), nil
}

func NewTTFTPercentileExtractor(ds datalayer.Datastore) *TTFTPercentileExtractor {
	return &TTFTPercentileExtractor{
		typedName: plugin.TypedName{Type: PluginType, Name: PluginType},
		ds: ds, state: make(map[string]*modelPercentileState),
		windowSize: defaultWindowSize, windowAge: defaultWindowAge,
		minObservations: defaultMinObservations, intervalDuration: defaultIntervalDuration,
		inflightEMAAlpha: defaultInflightEMAAlpha, lowLoadWindowAge: defaultLowLoadWindowAge,
	}
}

func (e *TTFTPercentileExtractor) TypedName() plugin.TypedName { return e.typedName }
func (e *TTFTPercentileExtractor) WithName(n string) *TTFTPercentileExtractor {
	e.typedName.Name = n; return e
}
func (e *TTFTPercentileExtractor) WithIntervalDuration(d time.Duration) *TTFTPercentileExtractor {
	e.intervalDuration = d; return e
}
func (e *TTFTPercentileExtractor) WithWindow(size int, age time.Duration, minObs int) *TTFTPercentileExtractor {
	e.windowSize, e.windowAge, e.minObservations = size, age, minObs; return e
}
func (e *TTFTPercentileExtractor) WithInflightEMAAlpha(a float64) *TTFTPercentileExtractor {
	e.inflightEMAAlpha = a; return e
}
func (e *TTFTPercentileExtractor) WithLowLoadWindowAge(age time.Duration) *TTFTPercentileExtractor {
	e.lowLoadWindowAge = age; return e
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
				sample := float64(s.Requests)
				if !s.avgInflightInit {
					s.AvgInflight, s.avgInflightInit = sample, true
				} else {
					s.AvgInflight = e.inflightEMAAlpha*sample + (1-e.inflightEMAAlpha)*s.AvgInflight
				}
				s.flush(now, e.windowAge, e.lowLoadWindowAge, e.minObservations)
			}
			updated[model] = true
		}
	}

	for model := range updated {
		s := e.state[model]
		cutoff := now.Add(-e.windowAge)
		for id, entry := range s.pending {
			if entry.dispatchedAt.Before(cutoff) {
				delete(s.pending, id)
			}
		}
		m := s.TTFTPercentileMetrics
		e.ds.GetOrCreateModel(model).GetAttributes().Put(AttributeKey, m)
		debugLogger.Info("ttft-percentile wrote attribute",
			"model", model, "Requests", m.Requests, "AvgInflight", m.AvgInflight,
			"InflightAtP50", m.InflightAtP50, "P10Low_s", m.P10LowTTFT,
			"P10_s", m.P10TTFT, "P50_s", m.P50TTFT,
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
