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

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"sigs.k8s.io/controller-runtime/pkg/log"

	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/tracing"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/modelselector"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/metrics"
)

// NewModelSelector creates a new ModelSelector with the given pipeline.
func NewModelSelector(pipeline *ModelSelectorPipeline) *ModelSelector {
	return &ModelSelector{
		pipeline: pipeline,
	}
}

// ModelSelector selects the best model for a request by running a single ModelSelectorPipeline.
type ModelSelector struct {
	pipeline *ModelSelectorPipeline
}

// Pipeline returns the ModelSelectorPipeline used by this selector.
func (s *ModelSelector) Pipeline() *ModelSelectorPipeline {
	return s.pipeline
}

// Select runs the model selection pipeline (Filter → Score → Pick) and returns the selected model.
func (s *ModelSelector) Select(ctx context.Context, request *requesthandling.InferenceRequest, cycleState *plugin.CycleState, candidateModels []datalayer.Model) (result *modelselector.PipelineRunResult, err error) {
	logger := log.FromContext(ctx)
	logger.V(logutil.VERBOSE).Info("Starting model selection", "candidateModels", len(candidateModels))

	// Stage span for the whole model-selector decision. It is the single most
	// useful thing to see in a trace, so record the requested and selected model
	// (the pipeline's filter/scorer/picker spans nest under it).
	ctx, span := tracing.Tracer(modelSelectorTracerScope).Start(ctx, "model_selector", trace.WithSpanKind(trace.SpanKindInternal))
	defer span.End()
	span.SetAttributes(attribute.Int("llm_d.model_selector.candidate_count", len(candidateModels)))
	if requestedModel, ok := request.Body["model"].(string); ok && requestedModel != "" {
		span.SetAttributes(attribute.String("llm_d.model_selector.requested_model", requestedModel))
	}

	selectStart := time.Now()
	defer func() {
		metrics.RecordModelSelectorE2ELatency(time.Since(selectStart))
		metrics.RecordModelSelectorAttempt(err)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
	}()

	if len(candidateModels) == 0 {
		err = errors.New("no candidate models provided")
		return nil, err
	}

	result, err = s.pipeline.Run(ctx, request, cycleState, candidateModels)
	if err != nil {
		logger.V(logutil.VERBOSE).Info("Model selection failed", "error", err.Error())
		return nil, err
	}

	if result == nil || result.TargetModel == nil {
		err = errors.New("model selection returned no result")
		return nil, err
	}

	span.SetAttributes(attribute.String("llm_d.model_selector.selected_model", result.TargetModel.GetName()))
	logger.V(logutil.VERBOSE).Info("Model selection completed", "selectedModel", result.TargetModel.GetName())

	return result, nil
}
