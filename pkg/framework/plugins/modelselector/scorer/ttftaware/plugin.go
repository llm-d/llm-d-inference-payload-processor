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

// Package ttftaware routes each request to the model with the lowest predicted TTFT under
// current load. Prediction is a line through (inflightAtP50, P50) and a low anchor blended
// between the in-cloud point (inflightAtP25, P25) and the floor (0, P10Low). See README.md
// for the equations and rationale.
package ttftaware

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
	PluginType = "ttft-aware-scorer"

	defaultExplorationRate = 0.0 // off by default; set e.g. 0.1 for 10% exploration
	defaultAnchorGapScale  = 2.0 // inflight separation at which the blend fully trusts the secant
)

var _ modelselector.Scorer = &TTFTAwareScorer{}

// TTFTAwareScorerConfig holds optional parameters for the scorer plugin.
type TTFTAwareScorerConfig struct {
	// ExplorationRate controls the probability of routing a request to an under-observed
	// model (UNOBSERVED or SEED state) instead of the trusted best model. A value of 0.1
	// means ~10% of requests probe the under-observed model, preventing a burst of traffic
	// before the first responses return and P50 is calibrated.
	// Range [0, 1]. Default 0 (disabled — every request goes to the winner).
	ExplorationRate float64 `json:"explorationRate,omitempty"`
	// AnchorGapScale is the inflight separation at which the prediction fully trusts the in-cloud
	// secant; below it the low anchor blends toward the floor chord. Must be > 0. Default 2.
	AnchorGapScale float64 `json:"anchorGapScale,omitempty"`
}

type TTFTAwareScorer struct {
	typedName       plugin.TypedName
	explorationRate float64
	anchorGapScale  float64
}

func ScorerFactory(name string, parameters json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
	cfg := TTFTAwareScorerConfig{
		ExplorationRate: defaultExplorationRate,
		AnchorGapScale:  defaultAnchorGapScale,
	}
	if len(parameters) > 0 {
		if err := json.Unmarshal(parameters, &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse parameters for plugin %q: %w", name, err)
		}
	}
	if cfg.ExplorationRate < 0 || cfg.ExplorationRate > 1 {
		return nil, fmt.Errorf("explorationRate must be in [0, 1] for plugin %q", name)
	}
	if cfg.AnchorGapScale <= 0 {
		return nil, fmt.Errorf("anchorGapScale must be > 0 for plugin %q", name)
	}
	return NewTTFTAwareScorer().
		WithName(name).
		WithExplorationRate(cfg.ExplorationRate).
		WithAnchorGapScale(cfg.AnchorGapScale), nil
}

func NewTTFTAwareScorer() *TTFTAwareScorer {
	return &TTFTAwareScorer{
		typedName:       plugin.TypedName{Type: PluginType, Name: PluginType},
		explorationRate: defaultExplorationRate,
		anchorGapScale:  defaultAnchorGapScale,
	}
}

func (s *TTFTAwareScorer) TypedName() plugin.TypedName { return s.typedName }

func (s *TTFTAwareScorer) WithName(name string) *TTFTAwareScorer {
	s.typedName.Name = name
	return s
}

func (s *TTFTAwareScorer) WithExplorationRate(r float64) *TTFTAwareScorer {
	s.explorationRate = r
	return s
}

func (s *TTFTAwareScorer) WithAnchorGapScale(g float64) *TTFTAwareScorer {
	s.anchorGapScale = g
	return s
}

// modelEval is the scorer's per-model working state for one Score call.
type modelEval struct {
	metrics  ttftpercentile.TTFTPercentileMetrics
	eff      float64
	trusted  bool // calibrated operating point
	observed bool // has a service floor (not truly cold)
}

// Score ranks models by predicted TTFT: score = (maxTTFT − effectiveTTFT) / (maxTTFT − minTTFT).
// Cold models (no floor) are seeded at the best observed TTFT; if every model is cold, all score
// 1.0. With explorationRate > 0 each under-observed model is independently explored (forced to the
// top) or suppressed — see the scoring loop below and README.md.
func (s *TTFTAwareScorer) Score(ctx context.Context, cycleState *plugin.CycleState, _ *requesthandling.InferenceRequest, models []datalayer.Model) map[datalayer.Model]float64 {
	evals := make(map[datalayer.Model]*modelEval, len(models))
	minEff := math.MaxFloat64
	maxEff := 0.0
	anyObserved := false
	anyTrusted := false

	for _, model := range models {
		m := metricsFor(model)
		eff, trusted, observed := s.predict(m)
		evals[model] = &modelEval{metrics: m, eff: eff, trusted: trusted, observed: observed}
		if observed {
			anyObserved = true
			minEff = min(minEff, eff)
			maxEff = max(maxEff, eff) // cold models seed to minEff, so they can never be the max
		}
		if trusted {
			anyTrusted = true
		}
	}

	scores := make(map[datalayer.Model]float64, len(models))

	// No model has a floor yet → nothing to rank; explore all equally.
	if !anyObserved {
		for _, model := range models {
			scores[model] = 1.0
		}
		return scores
	}

	// Independent coin per under-observed model (explorationRate == 0 disables exploration). Heads
	// forces that model to the top so the picker sends it a calibration probe; tails suppresses it to
	// 0 so only calibrated models compete, but only when a calibrated model exists to take over. Each
	// under-observed model is flipped independently, so the fraction of requests that explore grows
	// with the number of under-observed models (~1-(1-rate)^k) — a larger budget in exchange for
	// guaranteed per-model probe coverage. The override sets the final score only — eff and the
	// min/max normalization above are untouched.
	for _, model := range models {
		e := evals[model]
		if !e.observed {
			e.eff = minEff // seed cold models at the best observed TTFT (optimistic)
		}
		if maxEff == minEff {
			scores[model] = 1.0
		} else {
			scores[model] = (maxEff - e.eff) / (maxEff - minEff)
		}
		if s.explorationRate > 0 && !e.trusted {
			if rand.Float64() < s.explorationRate {
				scores[model] = 1.0 // explore: probe this under-observed model
			} else if anyTrusted {
				scores[model] = 0 // exploit: a calibrated model takes the traffic
			}
		}
	}

	if dl := log.FromContext(ctx).V(logutil.DEBUG); dl.Enabled() {
		for _, model := range models {
			e := evals[model]
			dl.Info("ttft-aware score", "model", model.GetName(),
				"effectiveTTFT", e.eff, "score", scores[model], "trusted", e.trusted)
		}
	}

	return scores
}

// predict returns the model's effective TTFT and whether its operating point is trusted
// (calibrated) and observed (has a floor). Cold models return (0, false, false); observed but
// uncalibrated models seed at the floor. See README.md for the blended-secant equation.
func (s *TTFTAwareScorer) predict(m ttftpercentile.TTFTPercentileMetrics) (eff float64, trusted, observed bool) {
	floor := m.Floor()
	if floor == 0 {
		return 0, false, false
	}
	if !(m.RecentN >= m.MinRequests && m.InflightAtP50 > 0 && m.P50TTFT > floor) {
		return floor, false, true // optimistic seed at the floor
	}

	// Blend the low anchor between (iP25, P25) and (0, floor) by the anchor separation; w→1
	// under load gives the in-cloud secant, w→0 at low load gives the floor chord.
	w := max(0, min(1, (m.InflightAtP50-m.InflightAtP25)/s.anchorGapScale))
	lowInflight := w * m.InflightAtP25
	lowTTFT := w*m.P25TTFT + (1-w)*floor

	cur := float64(m.Requests)
	eff = lowTTFT + (cur-lowInflight)*(m.P50TTFT-lowTTFT)/(m.InflightAtP50-lowInflight)
	if eff < floor {
		eff = floor
	}
	return eff, true, true
}

// metricsFor reads the TTFT percentile metrics an extractor published for the model.
// A missing or malformed attribute yields the zero value, which reads as truly cold.
func metricsFor(model datalayer.Model) ttftpercentile.TTFTPercentileMetrics {
	if val, err := datalayer.ReadAttributeKey[ttftpercentile.TTFTPercentileMetrics](
		model.GetAttributes(), ttftpercentile.AttributeKey,
	); err == nil {
		return val
	}
	return ttftpercentile.TTFTPercentileMetrics{}
}
