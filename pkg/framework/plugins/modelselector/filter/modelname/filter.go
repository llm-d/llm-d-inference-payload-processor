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

// Package modelname implements a modelselector filter that restricts the
// candidate models to the model name(s) in the request body.
//
// For detailed behavioral intent and configuration, see the package README.
package modelname

import (
	"context"
	"encoding/json"

	"sigs.k8s.io/controller-runtime/pkg/log"

	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/modelselector"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
)

const (
	// ModelNameFilterType is the registered name of the model-name filter plugin.
	ModelNameFilterType = "model-name-filter"

	// requestModelField is the request-body field holding the requested model name.
	requestModelField = "model"
)

// compile-time type validation
var _ modelselector.Filter = &ModelNameFilter{}

// ModelNameFilterFactory defines the factory function for ModelNameFilter.
func ModelNameFilterFactory(name string, _ json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
	return NewModelNameFilter().WithName(name), nil
}

// NewModelNameFilter initializes a new ModelNameFilter and returns its pointer.
func NewModelNameFilter() *ModelNameFilter {
	return &ModelNameFilter{
		typedName: plugin.TypedName{Type: ModelNameFilterType, Name: ModelNameFilterType},
	}
}

// ModelNameFilter restricts the candidate models to the model name in the request body.
type ModelNameFilter struct {
	typedName plugin.TypedName
}

// TypedName returns the type and name tuple of this plugin instance.
func (f *ModelNameFilter) TypedName() plugin.TypedName {
	return f.typedName
}

// WithName sets the name of the plugin instance.
func (f *ModelNameFilter) WithName(name string) *ModelNameFilter {
	f.typedName.Name = name
	return f
}

// Filter returns the candidate models whose name matches the request body's
// "model" field. The field must be a string; an absent field or empty string
// does not constrain the candidates; any non-string value is malformed and
// yields no candidates (the pipeline rejects the request).
func (f *ModelNameFilter) Filter(ctx context.Context, _ *plugin.CycleState, request *requesthandling.InferenceRequest, models []datalayer.Model) []datalayer.Model {
	logger := log.FromContext(ctx)

	requested, ok := request.Body[requestModelField].(string)
	if !ok && request.Body[requestModelField] != nil {
		logger.V(logutil.VERBOSE).Info("malformed model field in request body, no available model candidates", "field", requestModelField)
		return []datalayer.Model{}
	}
	if requested == "" {
		logger.V(logutil.VERBOSE).Info("no model in request body. All available models are considered as candidates", "field", requestModelField)
		return models
	}

	for _, model := range models {
		if model.GetName() == requested {
			logger.V(logutil.DEBUG).Info("model-name filter applied", "requested", requested)
			return []datalayer.Model{model}
		}
	}

	logger.V(logutil.VERBOSE).Info("request body model is not configured", "requested", requested)
	return []datalayer.Model{}
}
