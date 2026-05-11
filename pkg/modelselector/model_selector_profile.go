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
	"fmt"
	"strings"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/modelselector"
)

// NewModelSelectorProfile creates a new ModelSelectorProfile object and returns its pointer.
func NewModelSelectorProfile() *ModelSelectorProfile {
	return &ModelSelectorProfile{
		filters: []modelselector.Filter{},
		scorers: []*WeightedScorer{},
		// picker remains nil since profile doesn't support multiple pickers
	}
}

// ModelSelectorProfile provides a profile configuration for the model-selector which influence model decisions.
type ModelSelectorProfile struct {
	filters []modelselector.Filter
	scorers []*WeightedScorer
	picker  modelselector.Picker
}

func (p *ModelSelectorProfile) String() string {
	filterNames := make([]string, len(p.filters))
	for i, filter := range p.filters {
		filterNames[i] = filter.TypedName().String()
	}
	scorerNames := make([]string, len(p.scorers))
	for i, scorer := range p.scorers {
		scorerNames[i] = fmt.Sprintf("%s: %f", scorer.TypedName(), scorer.Weight())
	}

	return fmt.Sprintf(
		"{Filters: [%s], Scorers: [%s], Picker: %s}",
		strings.Join(filterNames, ", "),
		strings.Join(scorerNames, ", "),
		p.picker.TypedName(),
	)
}
