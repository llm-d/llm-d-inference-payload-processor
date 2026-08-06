/*
Copyright 2026 The llm-d-inference-payload-processor Authors.

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

package inflightrequests

import (
	"context"
	"encoding/json"
	"math"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/modelselector"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	requestmetadataextractor "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/datalayer/requestmetadata"
)

const PluginType = "inflight-requests-scorer"

// compile-time interface assertion
var _ modelselector.Scorer = &InflightRequestsScorer{}

// InflightRequestsScorer scores models based on their in-flight request count.
// The least loaded model scores 1.0; the most loaded scores 0.0.
// Models without an inflight-requests attribute are treated as idle (0 requests).
// If all models have the same count, all score 1.0.
type InflightRequestsScorer struct {
	typedName plugin.TypedName
}

// ScorerFactory is the factory function for InflightRequestsScorer.
func ScorerFactory(name string, _ json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
	return NewInflightRequestsScorer().WithName(name), nil
}

// NewInflightRequestsScorer initializes a new InflightRequestsScorer and returns its pointer.
func NewInflightRequestsScorer() *InflightRequestsScorer {
	return &InflightRequestsScorer{
		typedName: plugin.TypedName{Type: PluginType, Name: PluginType},
	}
}

// TypedName returns the type and name tuple of this plugin instance.
func (s *InflightRequestsScorer) TypedName() plugin.TypedName { return s.typedName }

// WithName sets the instance name.
func (s *InflightRequestsScorer) WithName(name string) *InflightRequestsScorer {
	s.typedName.Name = name
	return s
}

// Score returns a score in [0,1] for each model based on its in-flight request count.
// Formula: score = (max - count) / (max - min)
func (s *InflightRequestsScorer) Score(_ context.Context, _ *plugin.CycleState, _ *requesthandling.InferenceRequest, models []datalayer.Model) map[datalayer.Model]float64 {
	var minCount int64 = math.MaxInt64
	var maxCount int64 = math.MinInt64

	requestCounts := make(map[datalayer.Model]int64, len(models))
	for _, model := range models {
		count := inflightRequestCount(model)
		requestCounts[model] = count
		if count < minCount {
			minCount = count
		}
		if count > maxCount {
			maxCount = count
		}
	}

	scores := make(map[datalayer.Model]float64, len(models))
	for _, model := range models {
		if maxCount == minCount {
			scores[model] = 1.0
		} else {
			scores[model] = float64(maxCount-requestCounts[model]) / float64(maxCount-minCount)
		}
	}
	return scores
}

// inflightRequestCount returns the in-flight request count for a model.
// Returns 0 if the attribute is missing or has an unexpected type.
func inflightRequestCount(model datalayer.Model) int64 {
	val, ok := model.GetAttributes().Get(requestmetadataextractor.RequestMetadataAttributeKey)
	if !ok {
		return 0
	}
	rc, ok := val.(requestmetadataextractor.RequestMetadataCount)
	if !ok {
		return 0
	}
	return rc.Requests
}
