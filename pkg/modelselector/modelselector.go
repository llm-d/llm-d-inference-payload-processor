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
	"errors"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log"

	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/modelselector"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/metrics"
)

// NewModelSelector creates a new ModelSelector with the given profile.
func NewModelSelector(profile *ModelSelectorProfile) *ModelSelector {
	return &ModelSelector{
		profile: profile,
	}
}

// ModelSelector selects the best model for a request by running a single ModelSelectorProfile.
type ModelSelector struct {
	profile *ModelSelectorProfile
}

// Select runs the model selection pipeline (Filter → Score → Pick) and returns the selected model.
func (s *ModelSelector) Select(ctx context.Context, request *requesthandling.InferenceRequest, cycleState *plugin.CycleState, candidateModels []datalayer.Model) (result *modelselector.ProfileRunResult, err error) {
	logger := log.FromContext(ctx)
	logger.V(logutil.VERBOSE).Info("Starting model selection", "candidateModels", len(candidateModels))

	selectStart := time.Now()
	defer func() {
		metrics.RecordModelSelectorE2ELatency(time.Since(selectStart))
		metrics.RecordModelSelectorAttempt(err)
	}()

	if len(candidateModels) == 0 {
		err = errors.New("no candidate models provided")
		return nil, err
	}

	result, err = s.profile.Run(ctx, request, cycleState, candidateModels)
	if err != nil {
		logger.V(logutil.VERBOSE).Info("Model selection failed", "error", err.Error())
		return nil, err
	}

	if result == nil || result.TargetModel == nil {
		err = errors.New("model selection returned no result")
		return nil, err
	}

	logger.V(logutil.VERBOSE).Info("Model selection completed", "selectedModel", result.TargetModel.GetName())

	return result, nil
}
