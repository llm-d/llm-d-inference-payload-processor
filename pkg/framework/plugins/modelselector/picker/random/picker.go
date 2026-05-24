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

// Package random implements a modelselector picker that selects model uniformly at random,
// ignoring any scores calculated by scorer plugins.
//
// For detailed behavioral intent and configuration, see the package README.
package random

import (
	"context"
	"encoding/json"

	"sigs.k8s.io/controller-runtime/pkg/log"

	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/modelselector"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/modelselector/picker"
)

const (
	// RandomPickerType is the registered name of the random picker plugin.
	RandomPickerType = "random-picker"
)

// compile-time type validation
var _ modelselector.Picker = &RandomPicker{}

// RandomPickerFactory defines the factory function for RandomPicker.
func RandomPickerFactory(name string, _ json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
	return NewRandomPicker().WithName(name), nil
}

// NewRandomPicker initializes a new RandomPicker and returns its pointer.
func NewRandomPicker() *RandomPicker {
	return &RandomPicker{
		typedName: plugin.TypedName{Type: RandomPickerType, Name: RandomPickerType},
	}
}

// RandomPicker picks random model from the list of candidates.
type RandomPicker struct {
	typedName plugin.TypedName
}

// WithName sets the name of the picker.
func (p *RandomPicker) WithName(name string) *RandomPicker {
	p.typedName.Name = name
	return p
}

// TypedName returns the type and name tuple of this plugin instance.
func (p *RandomPicker) TypedName() plugin.TypedName {
	return p.typedName
}

// Pick selects random model from the list of candidates.
func (p *RandomPicker) Pick(ctx context.Context, _ *plugin.CycleState, scoredModels []*modelselector.ScoredModel) *modelselector.ProfileRunResult {
	if debugLogger := log.FromContext(ctx).V(logutil.DEBUG); debugLogger.Enabled() {
		debugLogger.Info("Selecting model from candidates randomly",
			"numOfCandidates", len(scoredModels), "scoredModels", scoredModels)
	}

	// Shuffle to ensure uniform random selection.
	picker.ShuffleScoredModels(scoredModels)

	return &modelselector.ProfileRunResult{TargetModel: scoredModels[0].Model}
}
