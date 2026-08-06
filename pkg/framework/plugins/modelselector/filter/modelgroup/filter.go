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

// Package modelgroup implements a modelselector filter that restricts the
// candidate models based on the request body model name field, supporting
// explicit model selection as well as "auto" and "auto/<group-name>" selection
// of a configured group of models.
//
// For detailed behavioral intent and configuration, see the package README.
package modelgroup

import (
	"context"
	"encoding/json"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/log"

	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer/modelgroups"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/modelselector"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
)

const (
	// ModelGroupFilterType is the registered name of the model-group filter plugin.
	ModelGroupFilterType = "model-group-name-filter"

	// requestModelField is the request-body field holding the requested model name.
	requestModelField = "model"

	// autoPrefix is the model name value (or prefix) that triggers "all candidates"
	// or group-based filtering.
	autoPrefix = "auto"

	// autoGroupSeparator separates "auto" from the group name in "auto/<group-name>".
	autoGroupSeparator = "/"
)

// compile-time type validation
var _ modelselector.Filter = &ModelGroupFilter{}

// ModelGroupFilterFactory returns a configured ModelGroupFilter. The filter takes
// no parameters: group membership is resolved at filter time from each candidate
// model's modelgroups.GroupsAttributeKey attribute, populated by the
// model-config-datasource plugin from the shared config file's "groups" list.
func ModelGroupFilterFactory(name string, _ json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
	return NewModelGroupFilter().WithName(name), nil
}

// NewModelGroupFilter initializes a new ModelGroupFilter.
func NewModelGroupFilter() *ModelGroupFilter {
	return &ModelGroupFilter{
		typedName: plugin.TypedName{Type: ModelGroupFilterType, Name: ModelGroupFilterType},
	}
}

// ModelGroupFilter restricts the candidate models based on the request body's
// "model" field: an exact model name, "auto"/absent/empty for all candidates,
// or "auto/<group-name>" for the candidates whose modelgroups.GroupsAttributeKey
// attribute lists that group name.
type ModelGroupFilter struct {
	typedName plugin.TypedName
}

// TypedName returns the type and name tuple of this plugin instance.
func (f *ModelGroupFilter) TypedName() plugin.TypedName {
	return f.typedName
}

// WithName sets the name of the plugin instance.
func (f *ModelGroupFilter) WithName(name string) *ModelGroupFilter {
	f.typedName.Name = name
	return f
}

// Filter returns the candidate models based on the "model" field of the request:
//   - absent, empty, or exactly "auto": all candidates pass through.
//   - "auto/<group-name>": candidates whose name appears in the named group.
//   - a plain non-"auto"-prefixed string: the single candidate matching that name.
//   - "auto/" (empty group name), unknown group, unmatched name, or non-string
//     type: no candidates (pipeline rejects with 429).
func (f *ModelGroupFilter) Filter(ctx context.Context, _ *plugin.CycleState, request *requesthandling.InferenceRequest, models []datalayer.Model) []datalayer.Model {
	logger := log.FromContext(ctx)

	raw := request.Body[requestModelField]
	requested, ok := raw.(string)
	if !ok && raw != nil {
		logger.V(logutil.VERBOSE).Info("malformed model field in request body, no available model candidates", "field", requestModelField)
		return []datalayer.Model{}
	}

	if requested == "" || requested == autoPrefix {
		logger.V(logutil.VERBOSE).Info("no model or auto in request body, all candidates kept", "field", requestModelField)
		return models
	}

	autoGroupPrefix := autoPrefix + autoGroupSeparator
	if !strings.HasPrefix(requested, autoGroupPrefix) {
		// Not an "auto/..." selector: treat it as an explicit model name request.
		for _, model := range models {
			if model.GetName() == requested {
				logger.V(logutil.DEBUG).Info("model-group filter applied explicit match", "requested", requested)
				return []datalayer.Model{model}
			}
		}
		logger.V(logutil.VERBOSE).Info("request body model is not configured", "requested", requested)
		return []datalayer.Model{}
	}

	groupName := strings.TrimPrefix(requested, autoGroupPrefix)
	if groupName == "" {
		logger.V(logutil.VERBOSE).Info("empty group name in auto-group selector, no candidates", "requested", requested)
		return []datalayer.Model{}
	}

	var result []datalayer.Model
	for _, m := range models {
		groups, ok := m.GetAttributes().Get(modelgroups.GroupsAttributeKey)
		if !ok {
			continue
		}
		if g, ok := groups.(modelgroups.Groups); ok && g.Contains(groupName) {
			result = append(result, m)
		}
	}

	if len(result) == 0 {
		logger.V(logutil.VERBOSE).Info("unknown group or no candidates match the auto-group", "group", groupName)
		return []datalayer.Model{}
	}

	logger.V(logutil.DEBUG).Info("model-group filter applied group match", "group", groupName, "candidates", len(result))
	return result
}
