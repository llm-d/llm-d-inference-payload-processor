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
	// unobserved is a sentinel returned by effectiveTTFT when no floor exists yet.
	// It is negative so it cannot be confused with a valid (non-negative) TTFT estimate.
	unobserved = -1.0

	defaultExplorationRate = 0.0 // off by default; set e.g. 0.1 for 10% exploration
)

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
	s.typedName.Name = name; return s
}
func (s *QueueTTFTScorer) WithExplorationRate(r float64) *QueueTTFTScorer {
	s.explorationRate = r; return s
}

// Score returns (maxTTFT − effectiveTTFT) / (maxTTFT − minTTFT) per model.
//
// Three cases per model:
//  1. Truly cold (no floor): unobserved sentinel → seeded to min(observed) after all are computed.
//  2. Trusted: RecentN ≥ MinRequests with a calibrated P50 → formula score.
//  3. Gone-quiet / under-observed: floor only (optimistic, assumes no queue).
//
// If all models are cold → all score 1.0.
//
// If explorationRate > 0, models in state 1 or 3 are suppressed to score 0 with probability
// (1 - explorationRate). This throttles probing traffic to ~explorationRate of requests,
// preventing a burst of traffic to under-observed models before their first responses return.
func (s *QueueTTFTScorer) Score(ctx context.Context, _ *plugin.CycleState, _ *requesthandling.InferenceRequest, models []datalayer.Model) map[datalayer.Model]float64 {
	effs := make(map[datalayer.Model]float64, len(models))
	needsProbe := make(map[datalayer.Model]bool, len(models))
	minEff, maxEff := math.MaxFloat64, 0.0
	allUnobserved := true

	for _, model := range models {
		v, probe := s.effectiveTTFT(ctx, model)
		effs[model] = v
		needsProbe[model] = probe
		if v != unobserved {
			allUnobserved = false
			if v > maxEff {
				maxEff = v
			}
			if v < minEff {
				minEff = v
			}
		}
	}

	// All models cold → explore equally (no suppression).
	if allUnobserved {
		scores := make(map[datalayer.Model]float64, len(models))
		for _, model := range models {
			scores[model] = 1.0
		}
		return scores
	}

	// Seed truly cold models at the best observed TTFT (optimistic: assume as-good-as-best).
	seed := minEff
	for _, model := range models {
		if effs[model] == unobserved {
			effs[model] = seed
			log.FromContext(ctx).V(logutil.DEBUG).Info("queue-ttft cold-seed",
				"model", model.GetName(), "seed_s", seed,
			)
		}
	}

	// Recompute range after seeding (seed == minEff so max is unchanged, but be explicit).
	minEff, maxEff = math.MaxFloat64, 0.0
	for _, v := range effs {
		if v > maxEff {
			maxEff = v
		}
		if v < minEff {
			minEff = v
		}
	}

	scores := make(map[datalayer.Model]float64, len(models))
	for _, model := range models {
		if maxEff == minEff {
			scores[model] = 1.0
		} else {
			scores[model] = (maxEff - effs[model]) / (maxEff - minEff)
		}
	}

	// Exploration gate: under-observed models win only explorationRate fraction of requests.
	// On the other (1-explorationRate) fraction, suppress their score to 0 so the best
	// calibrated model wins and the under-observed model does not receive a traffic burst.
	if s.explorationRate > 0 {
		for _, model := range models {
			if needsProbe[model] && rand.Float64() >= s.explorationRate {
				scores[model] = 0
				log.FromContext(ctx).V(logutil.DEBUG).Info("queue-ttft exploration suppressed",
					"model", model.GetName(), "explorationRate", s.explorationRate,
				)
			}
		}
	}

	if dl := log.FromContext(ctx).V(logutil.DEBUG); dl.Enabled() {
		for _, model := range models {
			dl.Info("queue-ttft score",
				"model", model.GetName(),
				"effectiveTTFT", effs[model],
				"score", scores[model],
				"needsProbe", needsProbe[model],
			)
		}
	}
	return scores
}

// effectiveTTFT returns the predicted TTFT for the model's next request and whether
// the model needs exploration (UNOBSERVED or SEED state — not yet calibrated).
//
//   - (unobserved, true): no floor yet — truly cold model.
//   - (floor, true): under-observed or gone-quiet — optimistic seed, assume no queue.
//   - (floor + inflight×slope, false): trusted operating point with recent calibration.
func (s *QueueTTFTScorer) effectiveTTFT(ctx context.Context, model datalayer.Model) (float64, bool) {
	val, ok := model.GetAttributes().Get(ttftpercentile.AttributeKey)
	if !ok {
		return unobserved, true
	}
	m, ok := val.(ttftpercentile.TTFTPercentileMetrics)
	if !ok {
		return unobserved, true
	}

	floor := m.P10LowTTFT
	if floor == 0 {
		floor = m.P10TTFT
	}
	if floor == 0 {
		return unobserved, true // truly cold: no hardware floor established yet
	}

	// Trusted: enough recent observations and a calibrated operating point.
	if m.RecentN >= m.MinRequests && m.InflightAtP50 > 0 && m.P50TTFT > floor {
		eff := floor + float64(m.Requests)*(m.P50TTFT-floor)/m.InflightAtP50
		if eff < floor {
			eff = floor
		}
		if dl := log.FromContext(ctx).V(logutil.DEBUG); dl.Enabled() {
			dl.Info("queue-ttft effective trusted",
				"model", model.GetName(), "inflight", m.Requests,
				"inflightAtP50", m.InflightAtP50, "P10Low_s", m.P10LowTTFT,
				"P50_s", m.P50TTFT, "recentN", m.RecentN, "effectiveTTFT", eff,
			)
		}
		return eff, false
	}

	// Optimistic seed: model is under-observed or gone-quiet.
	// Assume no queue — predict only the hardware floor.
	if dl := log.FromContext(ctx).V(logutil.DEBUG); dl.Enabled() {
		dl.Info("queue-ttft effective seed",
			"model", model.GetName(), "floor_s", floor,
			"recentN", m.RecentN, "minRequests", m.MinRequests,
		)
	}
	return floor, true
}
