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
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
)

func TestNewSingleProfilePicker(t *testing.T) {
	picker := NewSingleProfilePicker()

	wantTypedName := plugin.TypedName{
		Type: SingleProfilePickerType,
		Name: SingleProfilePickerType,
	}
	if diff := cmp.Diff(wantTypedName, picker.TypedName()); diff != "" {
		t.Errorf("Unexpected TypedName (-want +got): %s", diff)
	}
}

func TestNewSingleProfilePickerFactory(t *testing.T) {
	instance, err := SingleProfilePickerFactory("custom-name", nil, nil)
	if err != nil {
		t.Fatalf("SingleProfilePickerFactory() returned unexpected error: %v", err)
	}

	picker, ok := instance.(*SingleProfilePicker)
	if !ok {
		t.Fatalf("Expected *SingleProfilePicker, got %T", instance)
	}

	wantTypedName := plugin.TypedName{
		Type: SingleProfilePickerType,
		Name: "custom-name",
	}
	if diff := cmp.Diff(wantTypedName, picker.TypedName()); diff != "" {
		t.Errorf("Unexpected TypedName (-want +got): %s", diff)
	}
}

func TestWithName(t *testing.T) {
	picker := NewSingleProfilePicker().WithName("renamed")

	if picker.TypedName().Name != "renamed" {
		t.Errorf("Expected Name to be %q, got %q", "renamed", picker.TypedName().Name)
	}
	if picker.TypedName().Type != SingleProfilePickerType {
		t.Errorf("Expected Type to remain %q, got %q", SingleProfilePickerType, picker.TypedName().Type)
	}
}

func TestPick(t *testing.T) {
	defaultProfile := &requesthandling.Profile{}

	tests := []struct {
		name        string
		profiles    map[string]*requesthandling.Profile
		wantProfile *requesthandling.Profile
		wantErr     bool
	}{
		{
			name:     "no profiles configured, returns error",
			profiles: map[string]*requesthandling.Profile{},
			wantErr:  true,
		},
		{
			name: "more than a single profile defined, returns error",
			profiles: map[string]*requesthandling.Profile{
				"default": defaultProfile,
				"another": defaultProfile,
			},
			wantErr: true,
		},
		{
			name: "a single profile is configured, returns the single profile",
			profiles: map[string]*requesthandling.Profile{
				"default": defaultProfile,
			},
			wantProfile: defaultProfile,
		},
	}

	picker := NewSingleProfilePicker()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := picker.Pick(context.Background(), plugin.NewCycleState(), requesthandling.NewInferenceRequest(), tt.profiles)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if diff := cmp.Diff(tt.wantProfile, got); diff != "" {
				t.Errorf("Unexpected profile (-want +got): %s", diff)
			}
		})
	}
}
