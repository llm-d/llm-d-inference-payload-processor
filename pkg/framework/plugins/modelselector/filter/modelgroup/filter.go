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
	"errors"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/log"

	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
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

// ModelGroupFilterFactory parses the plugin parameters as a map of group name →
// model names and returns a configured ModelGroupFilter.
func ModelGroupFilterFactory(name string, params json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
	groups := map[string][]string{}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &groups); err != nil {
			return nil, err
		}
	}
	return NewModelGroupFilter(groups).WithName(name), nil
}

// NewModelGroupFilter initializes a new ModelGroupFilter with the given groups map.
// A group is skipped (not added) if its name is empty, its model list is empty,
// or any model name in the list is empty; a warning is logged for each skipped group.
func NewModelGroupFilter(groups map[string][]string) *ModelGroupFilter {
	// Build a set-per-group for O(1) membership tests.
	groupSets := make(map[string]map[string]struct{}, len(groups))
	for groupName, models := range groups {
		if err := validateGroup(groupName, models); err != nil {
			log.Log.Error(nil, "model-group filter: skipping invalid group configuration", "group", groupName, "models", models, "reason", err.Error())
			continue
		}
		set := make(map[string]struct{}, len(models))
		for _, m := range models {
			set[m] = struct{}{}
		}
		groupSets[groupName] = set
	}
	return &ModelGroupFilter{
		typedName: plugin.TypedName{Type: ModelGroupFilterType, Name: ModelGroupFilterType},
		groups:    groupSets,
	}
}

// validateGroup checks that groupName is non-empty and that models is a
// non-empty list of non-empty model names.
func validateGroup(groupName string, models []string) error {
	if groupName == "" {
		return errors.New("group name must be a non-empty string")
	}
	if len(models) == 0 {
		return errors.New("group must list at least one model")
	}
	for _, m := range models {
		if m == "" {
			return errors.New("group model names must be non-empty strings")
		}
	}
	return nil
}

// ModelGroupFilter restricts the candidate models based on the request body's
// "model" field: an exact model name, "auto"/absent/empty for all candidates,
// or "auto/<group-name>" for the models belonging to a configured group.
type ModelGroupFilter struct {
	typedName plugin.TypedName
	// groups maps group name → set of model names belonging to that group.
	groups map[string]map[string]struct{}
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

	groupSet, exists := f.groups[groupName]
	if !exists {
		logger.V(logutil.VERBOSE).Info("unknown group in auto-group selector, no candidates", "group", groupName)
		return []datalayer.Model{}
	}

	var result []datalayer.Model
	for _, m := range models {
		if _, inGroup := groupSet[m.GetName()]; inGroup {
			result = append(result, m)
		}
	}

	if len(result) == 0 {
		logger.V(logutil.VERBOSE).Info("no candidates match the auto-group", "group", groupName)
		return []datalayer.Model{}
	}

	logger.V(logutil.DEBUG).Info("model-group filter applied group match", "group", groupName, "candidates", len(result))
	return result
}
