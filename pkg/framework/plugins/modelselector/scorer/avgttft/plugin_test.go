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
	"testing"

	fwdatalayer "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	requestmetadata "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/datalayer/requestmetadata"
)

func modelWithAvgTTFT(name string, avgTTFT float64) fwdatalayer.Model {
	model := fwdatalayer.NewModel(name)
	model.GetAttributes().Put(requestmetadata.RequestMetadataAttributeKey, requestmetadata.ModelMetrics{
		AvgTTFT: avgTTFT,
	})
	return model
}

func modelWithNoAttribute(name string) fwdatalayer.Model {
	return fwdatalayer.NewModel(name)
}

func TestAvgTTFTScorer(t *testing.T) {
	scorer := NewAvgTTFTScorer()

	tests := []struct {
		name           string
		models         []fwdatalayer.Model
		expectedScores []float64
	}{
		{
			name: "lower TTFT gets higher score",
			models: []fwdatalayer.Model{
				modelWithAvgTTFT("fast", 0.2),
				modelWithAvgTTFT("slow", 1.0),
			},
			expectedScores: []float64{1.0, 0.0},
		},
		{
			name: "equal TTFT — all score 1.0",
			models: []fwdatalayer.Model{
				modelWithAvgTTFT("m1", 0.5),
				modelWithAvgTTFT("m2", 0.5),
			},
			expectedScores: []float64{1.0, 1.0},
		},
		{
			name: "no attribute scores optimistically (treated as 0)",
			models: []fwdatalayer.Model{
				modelWithAvgTTFT("observed", 0.5),
				modelWithNoAttribute("unobserved"),
			},
			expectedScores: []float64{0.0, 1.0},
		},
		{
			name: "three models — intermediate score is normalised",
			// min=0.2, max=1.0; middle=0.6 → (1.0-0.6)/(1.0-0.2) = 0.5
			models: []fwdatalayer.Model{
				modelWithAvgTTFT("fast", 0.2),
				modelWithAvgTTFT("mid", 0.6),
				modelWithAvgTTFT("slow", 1.0),
			},
			expectedScores: []float64{1.0, 0.5, 0.0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scores := scorer.Score(context.Background(), nil, nil, tt.models)
			for i, model := range tt.models {
				got := scores[model]
				want := tt.expectedScores[i]
				if got != want {
					t.Errorf("model[%d] %q: expected score %f, got %f", i, model.GetName(), want, got)
				}
			}
		})
	}
}
