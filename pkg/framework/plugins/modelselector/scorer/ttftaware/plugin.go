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

// Package ttftaware routes each request to the model with the lowest predicted TTFT under current
// load. The prediction is a piecewise-linear curve through the model's measured points, evaluated
// at its current inflight count. See README.md for the model, the equations and the rationale.
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

	defaultExplorationRate = 0.0 // disabled; 0.1 probes each under-observed model on ~10% of requests
	defaultMinInflightGap  = 2.0 // inflight separation the operating points need to define a slope
	defaultRoundTTFTStep   = 0.0 // disabled; e.g. 0.01 quantizes predictions to 10ms
)

var _ modelselector.Scorer = &TTFTAwareScorer{}

// TTFTAwareScorerConfig holds optional parameters for the scorer plugin. See README.md.
type TTFTAwareScorerConfig struct {
	// ExplorationRate is the per-model probability that an under-observed model is forced to the
	// top score, so it gets the traffic it needs to calibrate. Range [0, 1]. Default 0 (disabled).
	ExplorationRate float64 `json:"explorationRate,omitempty"`
	// MinInflightGap is the minimum inflight separation between the two operating points for the
	// low one to be usable as a curve anchor. Below it their slope is noise. Must be > 0.
	MinInflightGap float64 `json:"minInflightGap,omitempty"`
	// RoundTTFTStep quantizes each prediction to a multiple of this many seconds before ranking
	// (e.g. 0.01 = 10ms), so models closer together than the step tie and the picker splits them
	// instead of one winning on a difference too small to be meaningful. Must be >= 0. Default 0.
	RoundTTFTStep float64 `json:"roundTTFTStep,omitempty"`
}

type TTFTAwareScorer struct {
	typedName       plugin.TypedName
	explorationRate float64
	minInflightGap  float64
	roundTTFTStep   float64
}

func ScorerFactory(name string, parameters json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
	cfg := TTFTAwareScorerConfig{
		ExplorationRate: defaultExplorationRate,
		MinInflightGap:  defaultMinInflightGap,
		RoundTTFTStep:   defaultRoundTTFTStep,
	}
	if len(parameters) > 0 {
		if err := json.Unmarshal(parameters, &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse parameters for plugin %q: %w", name, err)
		}
	}
	if cfg.ExplorationRate < 0 || cfg.ExplorationRate > 1 {
		return nil, fmt.Errorf("explorationRate must be in [0, 1] for plugin %q", name)
	}
	if cfg.MinInflightGap <= 0 {
		return nil, fmt.Errorf("minInflightGap must be > 0 for plugin %q", name)
	}
	if cfg.RoundTTFTStep < 0 {
		return nil, fmt.Errorf("roundTTFTStep must be >= 0 for plugin %q", name)
	}
	return NewTTFTAwareScorer().
		WithName(name).
		WithExplorationRate(cfg.ExplorationRate).
		WithMinInflightGap(cfg.MinInflightGap).
		WithRoundTTFTStep(cfg.RoundTTFTStep), nil
}

func NewTTFTAwareScorer() *TTFTAwareScorer {
	return &TTFTAwareScorer{
		typedName:       plugin.TypedName{Type: PluginType, Name: PluginType},
		explorationRate: defaultExplorationRate,
		minInflightGap:  defaultMinInflightGap,
		roundTTFTStep:   defaultRoundTTFTStep,
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

func (s *TTFTAwareScorer) WithMinInflightGap(g float64) *TTFTAwareScorer {
	s.minInflightGap = g
	return s
}

func (s *TTFTAwareScorer) WithRoundTTFTStep(step float64) *TTFTAwareScorer {
	s.roundTTFTStep = step
	return s
}

// modelEval is the scorer's per-model working state for one Score call.
type modelEval struct {
	pred     float64
	trusted  bool // calibrated operating point
	observed bool // has a service floor (not truly cold)
}

// Score ranks models by predicted TTFT: score = (maxTTFT - predictedTTFT) / (maxTTFT - minTTFT).
// Cold models are seeded at the best observed TTFT; if every model is cold, all score 1.0.
func (s *TTFTAwareScorer) Score(ctx context.Context, _ *plugin.CycleState, _ *requesthandling.InferenceRequest, models []datalayer.Model) map[datalayer.Model]float64 {
	evals := make([]modelEval, len(models))
	minPred := math.MaxFloat64
	maxPred := 0.0
	anyObserved := false
	anyTrusted := false

	for i, model := range models {
		pred, trusted, observed := s.predict(metricsFor(model))
		if s.roundTTFTStep > 0 {
			// Quantize before ranking so predictions closer together than the step tie.
			pred = math.Round(pred/s.roundTTFTStep) * s.roundTTFTStep
		}
		evals[i] = modelEval{pred: pred, trusted: trusted, observed: observed}
		if observed {
			anyObserved = true
			minPred = min(minPred, pred)
			maxPred = max(maxPred, pred) // cold models seed to minPred, so they can never be the max
		}
		if trusted {
			anyTrusted = true
		}
	}

	scores := make(map[datalayer.Model]float64, len(models))

	// No model has a floor yet -> nothing to rank; explore all equally.
	if !anyObserved {
		for _, model := range models {
			scores[model] = 1.0
		}
		return scores
	}

	for i, model := range models {
		e := &evals[i]
		if !e.observed {
			e.pred = minPred // seed cold models at the best observed TTFT (optimistic)
		}
		if maxPred == minPred {
			scores[model] = 1.0
		} else {
			scores[model] = (maxPred - e.pred) / (maxPred - minPred)
		}
		// Independent coin per under-observed model: heads forces a calibration probe, tails
		// suppresses it so calibrated models take the traffic. Final score only — never feeds the
		// normalization above, so a probe cannot distort the trusted models' scores.
		if s.explorationRate > 0 && !e.trusted {
			if rand.Float64() < s.explorationRate {
				scores[model] = 1.0
			} else if anyTrusted {
				scores[model] = 0
			}
		}
	}

	if dl := log.FromContext(ctx).V(logutil.DEBUG); dl.Enabled() {
		for i, model := range models {
			dl.Info("ttft-aware score", "model", model.GetName(),
				"predictedTTFT", evals[i].pred, "score", scores[model], "trusted", evals[i].trusted)
		}
	}

	return scores
}

// predict returns the model's predicted TTFT at its current inflight, and whether it is trusted
// (calibrated) and observed (has a floor). Cold -> (0, false, false); observed but uncalibrated ->
// the floor as an optimistic seed. See README.md for the curve and the admissibility checks.
func (s *TTFTAwareScorer) predict(m ttftpercentile.TTFTPercentileMetrics) (pred float64, trusted, observed bool) {
	floor := m.Floor()
	if floor == 0 {
		return 0, false, false
	}
	if !(m.RecentN >= m.MinRequests && m.InflightAtHigh > 0 && m.HighTTFT > floor) {
		return floor, false, true
	}

	// The low point is admissible only if the two operating points are separated in load, ordered
	// in latency, and above the floor — otherwise its segment slopes on noise or downwards.
	useLow := m.InflightAtHigh-m.InflightAtLow >= s.minInflightGap &&
		m.HighTTFT > m.LowTTFT &&
		m.LowTTFT > floor &&
		m.InflightAtLow > 0

	cur := float64(m.Requests)
	switch {
	case useLow && cur < m.InflightAtLow:
		// (0, floor) -> low point. Extending the steeper high-load slope backwards instead would
		// predict a draining-but-loaded pool at its idle latency.
		pred = floor + cur*(m.LowTTFT-floor)/m.InflightAtLow
	case useLow:
		// low -> high point, extended beyond it.
		pred = m.LowTTFT + (cur-m.InflightAtLow)*(m.HighTTFT-m.LowTTFT)/(m.InflightAtHigh-m.InflightAtLow)
	default:
		// Low point inadmissible: floor chord (0, floor) -> high point, extended beyond it.
		pred = floor + cur*(m.HighTTFT-floor)/m.InflightAtHigh
	}
	return max(pred, floor), true, true
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
