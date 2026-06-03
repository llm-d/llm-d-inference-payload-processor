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

package avgttft

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
	requestmetadata "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/datalayer/requestmetadata"
)

const PluginType = "avg-ttft-scorer"

// compile-time interface assertion
var _ modelselector.Scorer = &AvgTTFTScorer{}

// AvgTTFTScorer scores models based on their exponential moving average TTFT.
// The model with the lowest AvgTTFT scores 1.0; the highest scores 0.0.
// Models with no observed TTFT yet (AvgTTFT == 0) are treated as idle and score 1.0.
// If all models have the same AvgTTFT, all score 1.0.
type AvgTTFTScorer struct {
	typedName plugin.TypedName
}

func ScorerFactory(name string, _ json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
	return NewAvgTTFTScorer().WithName(name), nil
}

func NewAvgTTFTScorer() *AvgTTFTScorer {
	return &AvgTTFTScorer{
		typedName: plugin.TypedName{Type: PluginType, Name: PluginType},
	}
}

func (s *AvgTTFTScorer) TypedName() plugin.TypedName { return s.typedName }

func (s *AvgTTFTScorer) WithName(name string) *AvgTTFTScorer {
	s.typedName.Name = name
	return s
}

// Score returns a score in [0,1] for each model.
// Formula: score = (max - avgTTFT) / (max - min)
func (s *AvgTTFTScorer) Score(ctx context.Context, _ *plugin.CycleState, _ *requesthandling.InferenceRequest, models []datalayer.Model) map[datalayer.Model]float64 {
	ttfts := make(map[datalayer.Model]float64, len(models))
	minTTFT := math.MaxFloat64
	maxTTFT := 0.0

	for _, model := range models {
		v := avgTTFT(model)
		ttfts[model] = v
		if v > maxTTFT {
			maxTTFT = v
		}
		if v < minTTFT {
			minTTFT = v
		}
	}

	scores := make(map[datalayer.Model]float64, len(models))
	for _, model := range models {
		if maxTTFT == minTTFT {
			scores[model] = 1.0
		} else {
			scores[model] = (maxTTFT - ttfts[model]) / (maxTTFT - minTTFT)
		}
	}

	if debugLogger := log.FromContext(ctx).V(logutil.DEBUG); debugLogger.Enabled() {
		for _, model := range models {
			debugLogger.Info("avg-ttft score", "model", model.GetName(), "avgTTFT", ttfts[model], "score", scores[model])
		}
	}

	return scores
}

// avgTTFT returns the AvgTTFT for a model, or 0 if not yet observed.
func avgTTFT(model datalayer.Model) float64 {
	val, ok := model.GetAttributes().Get(requestmetadata.RequestMetadataAttributeKey)
	if !ok {
		return 0
	}
	rc, ok := val.(requestmetadata.ModelMetrics)
	if !ok {
		return 0
	}
	return rc.AvgTTFT
}
