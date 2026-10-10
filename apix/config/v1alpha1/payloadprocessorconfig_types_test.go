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

package v1alpha1

import (
	"testing"
)

func TestProfileString(t *testing.T) {
	tests := []struct {
		name     string
		profile  Profile
		expected string
	}{
		{
			name:     "nil plugins does not panic and marks plugins as missing",
			profile:  Profile{Name: "my-profile"},
			expected: "{Name: my-profile, Plugins: <missing>}",
		},
		{
			name:     "zero-value profile does not panic",
			profile:  Profile{},
			expected: "{Name: , Plugins: <missing>}",
		},
		{
			name: "empty name still renders the name field",
			profile: Profile{
				Plugins: &ProfilePlugins{
					Request: []PluginRef{{PluginRef: "req-plugin"}},
				},
			},
			expected: "{Name: , Plugins: {Request: [{PluginRef: req-plugin}]}}",
		},
		{
			name: "request plugins only",
			profile: Profile{
				Name: "my-profile",
				Plugins: &ProfilePlugins{
					Request: []PluginRef{{PluginRef: "req-plugin"}},
				},
			},
			expected: "{Name: my-profile, Plugins: {Request: [{PluginRef: req-plugin}]}}",
		},
		{
			name: "response plugins only",
			profile: Profile{
				Name: "my-profile",
				Plugins: &ProfilePlugins{
					Response: []PluginRef{{PluginRef: "resp-plugin"}},
				},
			},
			expected: "{Name: my-profile, Plugins: {Response: [{PluginRef: resp-plugin}]}}",
		},
		{
			name: "request and response plugins",
			profile: Profile{
				Name: "my-profile",
				Plugins: &ProfilePlugins{
					Request:  []PluginRef{{PluginRef: "req-plugin"}},
					Response: []PluginRef{{PluginRef: "resp-plugin"}},
				},
			},
			expected: "{Name: my-profile, Plugins: {Request: [{PluginRef: req-plugin}], " +
				"Response: [{PluginRef: resp-plugin}]}}",
		},
		{
			name: "non-nil but empty plugins omits the plugins part",
			profile: Profile{
				Name:    "my-profile",
				Plugins: &ProfilePlugins{},
			},
			expected: "{Name: my-profile}",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.profile.String(); got != test.expected {
				t.Errorf("Profile.String() = %q, want %q", got, test.expected)
			}
		})
	}
}
