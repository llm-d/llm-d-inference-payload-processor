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

// Package queuettft scores models by predicted TTFT under current load.
// effectiveTTFT = P10Low + inflight × (P50 − P10Low) / inflightAtP50:
// a line through (0, P10Low) and (inflightAtP50, P50), extrapolated linearly.
// Under-observed models receive an optimistic seed (their own floor) so they
// keep competing for traffic instead of stalling at a fixed 0.5 score.
package queuettft

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"

	"sigs.k8s.io/controller-runtime/pkg/log"

	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/modelselector"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/datalayer/ttftpercentile"
)

const (
	PluginType = "queue-ttft-scorer"

	defaultExplorationRate = 0.0 // off by default; set e.g. 0.1 for 10% exploration

	// DecisionsCycleStateKey holds the per-model scoring decisions for response plugins.
	DecisionsCycleStateKey = "queue-ttft/decisions"
)

// ScoringDecision captures the scorer's decision for one model, written to CycleState.
type ScoringDecision struct {
	EffectiveTTFT         float64
	Floor                 float64 // P10Low
	P50                   float64
	RecentN               int
	Inflight              int64
	State                 string // "trusted" | "seed" | "unobserved"
	ExplorationSuppressed bool
}

var _ modelselector.Scorer = &QueueTTFTScorer{}

// QueueTTFTScorerConfig holds optional parameters for the scorer plugin.
type QueueTTFTScorerConfig struct {
	// ExplorationRate controls the probability of routing a request to an under-observed
	// model (UNOBSERVED or SEED state) instead of the trusted best model. A value of 0.1
	// means ~10% of requests probe the under-observed model, preventing a burst of traffic
	// before the first responses return and P50 is calibrated.
	// Range [0, 1]. Default 0 (disabled — every request goes to the winner).
	ExplorationRate float64 `json:"explorationRate,omitempty"`
}

type QueueTTFTScorer struct {
	typedName       plugin.TypedName
	explorationRate float64
}

func ScorerFactory(name string, parameters json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
	cfg := QueueTTFTScorerConfig{ExplorationRate: defaultExplorationRate}
	if len(parameters) > 0 {
		if err := json.Unmarshal(parameters, &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse parameters for plugin %q: %w", name, err)
		}
	}
	if cfg.ExplorationRate < 0 || cfg.ExplorationRate > 1 {
		return nil, fmt.Errorf("explorationRate must be in [0, 1] for plugin %q", name)
	}
	return NewQueueTTFTScorer().WithName(name).WithExplorationRate(cfg.ExplorationRate), nil
}

func NewQueueTTFTScorer() *QueueTTFTScorer {
	return &QueueTTFTScorer{
		typedName:       plugin.TypedName{Type: PluginType, Name: PluginType},
		explorationRate: defaultExplorationRate,
	}
}

func (s *QueueTTFTScorer) TypedName() plugin.TypedName { return s.typedName }
func (s *QueueTTFTScorer) WithName(name string) *QueueTTFTScorer {
	s.typedName.Name = name
	return s
}
func (s *QueueTTFTScorer) WithExplorationRate(r float64) *QueueTTFTScorer {
	s.explorationRate = r
	return s
}

// modelEval is the scorer's per-model working state for one Score call.
type modelEval struct {
	metrics    ttftpercentile.TTFTPercentileMetrics
	eff        float64
	trusted    bool // calibrated operating point
	observed   bool // has a service floor (not truly cold)
	suppressed bool // exploration-suppressed this cycle
}

// Score ranks models by predicted TTFT: score = (maxTTFT − effectiveTTFT) / (maxTTFT − minTTFT).
//
// Cold models (no floor) are seeded at the best observed TTFT; if every model is cold, all
// score 1.0. With explorationRate > 0, under-observed models (not yet calibrated) are
// suppressed to 0 with probability (1 - explorationRate) to throttle probing traffic.
func (s *QueueTTFTScorer) Score(ctx context.Context, cycleState *plugin.CycleState, _ *requesthandling.InferenceRequest, models []datalayer.Model) map[datalayer.Model]float64 {
	evals := make(map[datalayer.Model]*modelEval, len(models))
	minEff := math.MaxFloat64
	anyObserved := false

	for _, model := range models {
		m := metricsFor(model)
		eff, trusted := m.Predict()
		e := &modelEval{metrics: m, eff: eff, trusted: trusted, observed: m.Floor() > 0}
		evals[model] = e
		if e.observed {
			anyObserved = true
			if eff < minEff {
				minEff = eff
			}
		}
	}

	scores := make(map[datalayer.Model]float64, len(models))

	// No model has a floor yet → nothing to rank; explore all equally.
	if !anyObserved {
		for _, model := range models {
			scores[model] = 1.0
		}
		s.writeDecisions(cycleState, models, evals)
		return scores
	}

	// Seed cold models at the best observed TTFT (optimistic). Seeds equal minEff, so the
	// range spans the observed models and maxEff is their slowest.
	maxEff := 0.0
	for _, e := range evals {
		if !e.observed {
			e.eff = minEff
		}
		if e.eff > maxEff {
			maxEff = e.eff
		}
	}

	for _, model := range models {
		e := evals[model]
		if maxEff == minEff {
			scores[model] = 1.0
		} else {
			scores[model] = (maxEff - e.eff) / (maxEff - minEff)
		}
		if s.explorationRate > 0 && !e.trusted && rand.Float64() >= s.explorationRate {
			scores[model] = 0
			e.suppressed = true
		}
	}

	if dl := log.FromContext(ctx).V(logutil.DEBUG); dl.Enabled() {
		for _, model := range models {
			e := evals[model]
			dl.Info("queue-ttft score", "model", model.GetName(),
				"effectiveTTFT", e.eff, "score", scores[model], "trusted", e.trusted)
		}
	}

	s.writeDecisions(cycleState, models, evals)
	return scores
}

// metricsFor reads the TTFT percentile metrics an extractor published for the model.
// A missing or malformed attribute yields the zero value, which reads as truly cold.
func metricsFor(model datalayer.Model) ttftpercentile.TTFTPercentileMetrics {
	if val, ok := model.GetAttributes().Get(ttftpercentile.AttributeKey); ok {
		if m, ok := val.(ttftpercentile.TTFTPercentileMetrics); ok {
			return m
		}
	}
	return ttftpercentile.TTFTPercentileMetrics{}
}

// writeDecisions stores per-model scoring decisions in CycleState for response plugins.
func (s *QueueTTFTScorer) writeDecisions(cycleState *plugin.CycleState, models []datalayer.Model, evals map[datalayer.Model]*modelEval) {
	if cycleState == nil {
		return
	}
	decisions := make(map[string]ScoringDecision, len(models))
	for _, model := range models {
		e := evals[model]
		state := "trusted"
		switch {
		case !e.observed:
			state = "unobserved"
		case !e.trusted:
			state = "seed"
		}
		decisions[model.GetName()] = ScoringDecision{
			EffectiveTTFT:         e.eff,
			Floor:                 e.metrics.Floor(),
			P50:                   e.metrics.P50TTFT,
			RecentN:               e.metrics.RecentN,
			Inflight:              e.metrics.Requests,
			State:                 state,
			ExplorationSuppressed: e.suppressed,
		}
	}
	cycleState.Write(DecisionsCycleStateKey, decisions)
}
