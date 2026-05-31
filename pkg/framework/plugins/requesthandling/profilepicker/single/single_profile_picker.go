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

package single

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
)

const (
	SingleProfilePickerType = "single-profile-picker"
)

// compile-time type assertion
var _ requesthandling.ProfilePicker = &SingleProfilePicker{}

// SingleProfilePickerFactory defines the factory function for SingleProfilePicker.
func SingleProfilePickerFactory(name string, _ json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
	return NewSingleProfilePicker().WithName(name), nil
}

// NewSingleProfilePicker initializes a new SingleProfilePicker and returns its pointer.
func NewSingleProfilePicker() *SingleProfilePicker {
	return &SingleProfilePicker{
		typedName: plugin.TypedName{Type: SingleProfilePickerType, Name: SingleProfilePickerType},
	}
}

// SingleProfilePicker handles a single profile which is always the picked profile.
type SingleProfilePicker struct {
	typedName plugin.TypedName
}

// TypedName returns the type and name tuple of this plugin instance.
func (p *SingleProfilePicker) TypedName() plugin.TypedName {
	return p.typedName
}

// WithName sets the name of the profile picker.
func (p *SingleProfilePicker) WithName(name string) *SingleProfilePicker {
	p.typedName.Name = name
	return p
}

// Pick selects the Profile to run from the list of candidate profiles, while taking into consideration the request properties and the
// previously executed cycles along with their results.
func (p *SingleProfilePicker) Pick(ctx context.Context, cycleState *plugin.CycleState, request *requesthandling.InferenceRequest, profiles map[string]*requesthandling.Profile) (*requesthandling.Profile, error) {
	if len(profiles) != 1 {
		return nil, fmt.Errorf("failed to select a single profile from %d profiles", len(profiles))
	}

	var result *requesthandling.Profile
	for _, profile := range profiles {
		result = profile
		break // assumes a single profile
	}

	return result, nil
}
