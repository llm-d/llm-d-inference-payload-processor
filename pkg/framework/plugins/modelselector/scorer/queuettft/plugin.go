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
// effectiveTTFT = P10Low + inflight × (P50 − P10Low) / inflightAtP50:
// a line through (0, P10Low) and (inflightAtP50, P50), extrapolated linearly.
package medianttft

import (
	"context"
	"encoding/json"
	"math"

	"sigs.k8s.io/controller-runtime/pkg/log"

	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/modelselector"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/datalayer/ttftpercentile"
)

const PluginType = "median-ttft-scorer"

var _ modelselector.Scorer = &MedianTTFTScorer{}

type MedianTTFTScorer struct {
	typedName plugin.TypedName
}

func ScorerFactory(name string, _ json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
	return NewMedianTTFTScorer().WithName(name), nil
}

func NewMedianTTFTScorer() *MedianTTFTScorer {
	return &MedianTTFTScorer{
		typedName: plugin.TypedName{Type: PluginType, Name: PluginType},
	}
}

func (s *MedianTTFTScorer) TypedName() plugin.TypedName { return s.typedName }
func (s *MedianTTFTScorer) WithName(name string) *MedianTTFTScorer {
	s.typedName.Name = name; return s
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

	// effectiveTTFT = P10Low + inflight × (P50 − P10Low) / inflightAtP50
	// Falls back to P10Low when P50 is not yet available or equals the floor.
	eff := p10
	if m.InflightAtP50 > 0 && m.P50TTFT > p10 {
		eff = p10 + float64(m.Requests)*(m.P50TTFT-p10)/m.InflightAtP50
	}

	if dl := log.FromContext(ctx).V(logutil.DEBUG); dl.Enabled() {
		dl.Info("median-ttft effective",
			"model", model.GetName(), "inflight", m.Requests,
			"inflightAtP50", m.InflightAtP50, "P10Low_s", m.P10LowTTFT,
			"P10_s", m.P10TTFT, "P50_s", m.P50TTFT, "effectiveTTFT", eff,
		)
	}
	return eff
}
