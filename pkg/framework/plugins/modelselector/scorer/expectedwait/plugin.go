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

package expectedwait

import (
	"context"
	"encoding/json"
	"math"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/modelselector"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	requestmetadata "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/datalayer/requestmetadata"
)

// PluginType is the identifier used when registering this scorer.
const PluginType = "expected-wait-scorer"

// compile-time interface assertion
var _ modelselector.Scorer = &ExpectedWaitScorer{}

// ExpectedWaitScorer scores candidate models by estimated total response time:
//
//	total_time = (inflight_tokens + max_tokens) / tokens_per_sec
//
// Both inflight_tokens and tokens_per_sec are read from the single "request-metadata"
// attribute written by RequestMetadataExtractor.
// Models with no throughput data yet score 1.0 (optimistic — untried models are preferred).
// Lower expected wait → higher score via min-max normalisation.
type ExpectedWaitScorer struct {
	typedName plugin.TypedName
}

// ScorerFactory is the factory function for ExpectedWaitScorer.
func ScorerFactory(name string, _ json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
	return NewExpectedWaitScorer().WithName(name), nil
}

// NewExpectedWaitScorer creates a new ExpectedWaitScorer.
func NewExpectedWaitScorer() *ExpectedWaitScorer {
	return &ExpectedWaitScorer{
		typedName: plugin.TypedName{Type: PluginType, Name: PluginType},
	}
}

func (s *ExpectedWaitScorer) TypedName() plugin.TypedName { return s.typedName }

func (s *ExpectedWaitScorer) WithName(name string) *ExpectedWaitScorer {
	s.typedName.Name = name
	return s
}

// Score returns a score in [0,1] for each model based on estimated total response time.
// Formula: score = (maxWait - wait) / (maxWait - minWait); all 1.0 if maxWait == minWait.
//
// max_tokens is a pessimistic proxy for the current request's processing time.
// TODO: replace max_tokens with per-model avg_completion_tokens once tracked by RequestMetadataExtractor.
func (s *ExpectedWaitScorer) Score(_ context.Context, _ *plugin.CycleState, request *requesthandling.InferenceRequest, models []datalayer.Model) map[datalayer.Model]float64 {
	var maxTokens int64
	if request != nil {
		if v, ok := request.Body["max_tokens"].(float64); ok {
			maxTokens = int64(v)
		}
	}

	waits := make(map[datalayer.Model]float64, len(models))
	minWait := math.MaxFloat64
	maxWait := -math.MaxFloat64

	for _, model := range models {
		var inflightTokens int64
		var tokensPerSec float64
		if rc, err := datalayer.ReadAttributeKey[requestmetadata.RequestMetadataCount](
			model.GetAttributes(), requestmetadata.RequestMetadataAttributeKey); err == nil {
			inflightTokens = rc.Tokens
			tokensPerSec = rc.TokensPerSec
		}

		var expectedWait float64
		if tokensPerSec > 0 {
			expectedWait = float64(inflightTokens+maxTokens) / tokensPerSec
		}
		// tokensPerSec == 0: optimistic — untried models score best

		waits[model] = expectedWait
		if expectedWait < minWait {
			minWait = expectedWait
		}
		if expectedWait > maxWait {
			maxWait = expectedWait
		}
	}

	scores := make(map[datalayer.Model]float64, len(models))
	for _, model := range models {
		if maxWait == minWait {
			scores[model] = 1.0
		} else {
			scores[model] = (maxWait - waits[model]) / (maxWait - minWait)
		}
	}
	return scores
}
