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

// Package byfieldattribute implements a modelselector filter that matches
// candidates by name or attribute. See the package README for details.
package byfieldattribute

import (
	"context"
	"encoding/json"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/log"

	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/modelselector"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
)

type StringAttribute = datalayer.StringAttribute

const (
	// ModelNameFilterType is the registered plugin type for model-name-filter.
	ModelNameFilterType = "model-name-filter"
	// ByFieldFilterType is the registered plugin type for by-field-filter.
	ByFieldFilterType = "by-field-filter"

	requestModelField   = "model"
	defaultByFieldValue = ""
)

var _ modelselector.Filter = &ByFieldFilter{}
var _ datalayer.Cloneable = StringAttribute("")

// ByFieldFilterConfig is the JSON parameter shape for ByFieldFilter.
type ByFieldFilterConfig struct {
	FieldName string `json:"fieldName"`
	ByField   string `json:"byField,omitempty"`
}

// ModelNameFilterFactory ignores its parameters: fieldName is always "model"
// and byField always defaults to Model.GetName().
func ModelNameFilterFactory(name string, _ json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
	return newByFieldFilter(ModelNameFilterType, requestModelField, defaultByFieldValue).WithName(name), nil
}

// ByFieldFilterFactory parses fieldName (required) and byField (optional)
// from params and returns a configured ByFieldFilter.
func ByFieldFilterFactory(name string, params json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
	var cfg ByFieldFilterConfig
	if len(params) > 0 {
		if err := json.Unmarshal(params, &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse parameters of '%s': %w", ByFieldFilterType, err)
		}
	}
	if cfg.FieldName == "" {
		return nil, fmt.Errorf("'%s' requires a non-empty fieldName parameter", ByFieldFilterType)
	}

	return newByFieldFilter(ByFieldFilterType, cfg.FieldName, cfg.ByField).WithName(name), nil
}

// NewModelNameFilter initializes a ByFieldFilter with model-name-filter's
// fixed settings (fieldName "model", default byField).
func NewModelNameFilter() *ByFieldFilter {
	return newByFieldFilter(ModelNameFilterType, requestModelField, defaultByFieldValue)
}

// NewByFieldFilter initializes a by-field-filter with the given fieldName
// and byField (empty byField means Model.GetName()).
func NewByFieldFilter(fieldName, byField string) *ByFieldFilter {
	return newByFieldFilter(ByFieldFilterType, fieldName, byField)
}

func newByFieldFilter(typ, fieldName, byField string) *ByFieldFilter {
	if typ == ModelNameFilterType {
		log.Log.Info("'" + ModelNameFilterType + "' is deprecated and will be removed in ipp v0.3.0; " +
			"use '" + ByFieldFilterType + "' with fieldName: model instead")
	}
	return &ByFieldFilter{
		typedName: plugin.TypedName{Type: typ, Name: typ},
		fieldName: fieldName,
		byField:   byField,
	}
}

// ByFieldFilter restricts candidates to those whose comparison value (the
// name, or a configured attribute) matches a configurable request body field.
type ByFieldFilter struct {
	typedName plugin.TypedName
	fieldName string
	byField   string
}

func (f *ByFieldFilter) TypedName() plugin.TypedName {
	return f.typedName
}

func (f *ByFieldFilter) WithName(name string) *ByFieldFilter {
	f.typedName.Name = name
	return f
}

// Filter keeps candidates whose comparison value matches the request field.
// An absent or empty field keeps all candidates; a non-string field value is
// malformed and yields none (the pipeline rejects the request).
func (f *ByFieldFilter) Filter(ctx context.Context, _ *plugin.CycleState, request *requesthandling.InferenceRequest, models []datalayer.Model) []datalayer.Model {
	logger := log.FromContext(ctx)

	raw := request.Body[f.fieldName]
	requested, ok := raw.(string)
	if !ok && raw != nil {
		logger.V(logutil.VERBOSE).Info("malformed request field, no available model candidates", "field", f.fieldName)
		return []datalayer.Model{}
	}
	if requested == "" {
		logger.V(logutil.VERBOSE).Info("no value in request field. All available models are considered as candidates", "field", f.fieldName)
		return models
	}

	result := make([]datalayer.Model, 0, len(models))
	for _, model := range models {
		value, found := f.candidateValue(model)
		if !found {
			continue
		}
		if value == requested {
			result = append(result, model)
		}
	}

	if len(result) == 0 {
		logger.V(logutil.VERBOSE).Info("request field value is not configured", "requested", requested)
		return []datalayer.Model{}
	}

	logger.V(logutil.DEBUG).Info("by-field filter applied", "requested", requested, "candidates", len(result))
	return result
}

// candidateValue returns the model's name by default, or its byField
// attribute when configured; false if that attribute is missing.
func (f *ByFieldFilter) candidateValue(model datalayer.Model) (string, bool) {
	if f.byField == defaultByFieldValue {
		return model.GetName(), true
	}
	value, err := datalayer.ReadAttributeKey[StringAttribute](model.GetAttributes(), f.byField)
	if err != nil {
		return "", false
	}
	return string(value), true
}
