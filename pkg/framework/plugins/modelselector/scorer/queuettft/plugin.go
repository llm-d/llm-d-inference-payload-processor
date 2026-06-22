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

// Package medianttft scores models by predicted TTFT under current load.
// Model P10Low × (1 + inflight/capacity) is used to predict queue wait,
// where capacity is estimated from paired (P50, inflightAtP50) observations.
package medianttft

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"sigs.k8s.io/controller-runtime/pkg/log"

	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/modelselector"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/datalayer/ttftpercentile"
)

const (
	PluginType                    = "median-ttft-scorer"
	defaultInflightPenaltyWeight  = 1.0
)

var _ modelselector.Scorer = &MedianTTFTScorer{}

type MedianTTFTScorerConfig struct {
	// InflightPenaltyWeight scales the overload penalty:
	//   effectiveTTFT = P10Low × (1 + weight × (inflight/capacity − 1))
	// 0 disables the penalty; 1.0 (default) equals the queue model prediction.
	InflightPenaltyWeight *float64 `json:"inflightPenaltyWeight,omitempty"`
}

type MedianTTFTScorer struct {
	typedName             plugin.TypedName
	inflightPenaltyWeight float64
}

func ScorerFactory(name string, parameters json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
	cfg := MedianTTFTScorerConfig{}
	if len(parameters) > 0 {
		if err := json.Unmarshal(parameters, &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse parameters for plugin %q: %w", name, err)
		}
	}
	w := defaultInflightPenaltyWeight
	if cfg.InflightPenaltyWeight != nil {
		if *cfg.InflightPenaltyWeight < 0 {
			return nil, fmt.Errorf("inflightPenaltyWeight must be >= 0 for plugin %q", name)
		}
		w = *cfg.InflightPenaltyWeight
	}
	return NewMedianTTFTScorer().WithName(name).WithInflightPenaltyWeight(w), nil
}

func NewMedianTTFTScorer() *MedianTTFTScorer {
	return &MedianTTFTScorer{
		typedName:             plugin.TypedName{Type: PluginType, Name: PluginType},
		inflightPenaltyWeight: defaultInflightPenaltyWeight,
	}
}

func (s *MedianTTFTScorer) TypedName() plugin.TypedName { return s.typedName }
func (s *MedianTTFTScorer) WithName(name string) *MedianTTFTScorer {
	s.typedName.Name = name; return s
}
func (s *MedianTTFTScorer) WithInflightPenaltyWeight(w float64) *MedianTTFTScorer {
	s.inflightPenaltyWeight = w; return s
}

// Score returns (maxTTFT − effectiveTTFT) / (maxTTFT − minTTFT) per model.
// Unobserved models score 1.0 (all unobserved) or 0.5 (some peers observed).
func (s *MedianTTFTScorer) Score(ctx context.Context, _ *plugin.CycleState, _ *requesthandling.InferenceRequest, models []datalayer.Model) map[datalayer.Model]float64 {
	ttfts := make(map[datalayer.Model]float64, len(models))
	minTTFT, maxTTFT := math.MaxFloat64, 0.0
	allUnobserved := true

	for _, model := range models {
		v := s.effectiveTTFT(ctx, model)
		ttfts[model] = v
		if v > 0 {
			allUnobserved = false
			if v > maxTTFT {
				maxTTFT = v
			}
			if v < minTTFT {
				minTTFT = v
			}
		}
	}

	scores := make(map[datalayer.Model]float64, len(models))
	for _, model := range models {
		v := ttfts[model]
		switch {
		case v == 0 && allUnobserved:
			scores[model] = 1.0
		case v == 0:
			scores[model] = 0.5
		case maxTTFT == minTTFT:
			scores[model] = 1.0
		default:
			scores[model] = (maxTTFT - v) / (maxTTFT - minTTFT)
		}
	}

	if dl := log.FromContext(ctx).V(logutil.DEBUG); dl.Enabled() {
		for _, model := range models {
			dl.Info("median-ttft score", "model", model.GetName(), "effectiveTTFT", ttfts[model], "score", scores[model])
		}
	}
	return scores
}

func (s *MedianTTFTScorer) effectiveTTFT(ctx context.Context, model datalayer.Model) float64 {
	val, ok := model.GetAttributes().Get(ttftpercentile.AttributeKey)
	if !ok {
		return 0
	}
	m, ok := val.(ttftpercentile.TTFTPercentileMetrics)
	if !ok {
		return 0
	}
	p10 := m.P10LowTTFT
	if p10 == 0 {
		p10 = m.P10TTFT
	}
	if p10 == 0 {
		return 0
	}

	eff := p10
	var loadRatio float64
	if m.Capacity >= 1 {
		loadRatio = float64(m.Requests) / m.Capacity
		if loadRatio > 1 {
			eff = p10 * (1 + s.inflightPenaltyWeight*(loadRatio-1))
		}
	}

	if dl := log.FromContext(ctx).V(logutil.DEBUG); dl.Enabled() {
		dl.Info("median-ttft effective",
			"model", model.GetName(), "inflight", m.Requests, "capacity", m.Capacity,
			"loadRatio", loadRatio, "P10Low_s", m.P10LowTTFT, "P10_s", m.P10TTFT,
			"P50_s", m.P50TTFT, "effectiveTTFT", eff,
		)
	}
	return eff
}
