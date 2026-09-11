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

package modelselector

import (
	"context"
	"encoding/json"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
)

type ScoredModel struct {
	datalayer.Model
	Score float64
}

// MarshalJSON projects the embedded Model and Score into a flat JSON
// object. Without this, encoding/json promotes the embedded Model's
// MarshalJSON and silently drops Score from picker/pipeline log lines.
func (sm *ScoredModel) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Name  string  `json:"name"`
		Score float64 `json:"score"`
	}{Name: sm.GetName(), Score: sm.Score})
}

// PipelineRunResult captures the pipeline run result.
type PipelineRunResult struct {
	TargetModel datalayer.Model
}

type ModelSelectorPipeline interface {
	Run(ctx context.Context, request *requesthandling.InferenceRequest, cycleState *plugin.CycleState, candidateModels []datalayer.Model) (*PipelineRunResult, error)
}
