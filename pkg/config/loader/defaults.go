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

package loader

import (
	"encoding/json"
	"errors"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configapi "github.com/llm-d/llm-d-inference-payload-processor/apix/config/v1alpha1"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/requesthandling/basemodelextractor"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/requesthandling/bodyfieldtoheader"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/requesthandling/profilepicker/single"
)

func loadDefaultConfig() *configapi.PayloadProcessorConfig {
	return &configapi.PayloadProcessorConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "llm-d.ai/v1alpha1",
			Kind:       "PayloadProcessorConfig",
		},
		Plugins: []configapi.PluginSpec{
			{
				Type:       bodyfieldtoheader.BodyFieldToHeaderPluginType,
				Parameters: json.RawMessage(`{"fieldName": "model", "headerName": "X-Gateway-Model-Name"}`),
			},
			{
				Type: basemodelextractor.BaseModelToHeaderPluginType,
			},
		},
		Profiles: []configapi.Profile{
			{
				Name: "default",
				Plugins: &configapi.ProfilePlugins{
					Request: []configapi.PluginRef{
						{PluginRef: bodyfieldtoheader.BodyFieldToHeaderPluginType},
						{PluginRef: basemodelextractor.BaseModelToHeaderPluginType},
					},
				},
			},
		},
	}
}

// applyRawConfigDefaults applies defaults that can be applied at the level of the raw configuration text
func applyRawConfigDefaults(rawConfig *configapi.PayloadProcessorConfig) {
	for idx, pluginConfig := range rawConfig.Plugins {
		if pluginConfig.Name == "" {
			rawConfig.Plugins[idx].Name = pluginConfig.Type
		}
	}
}

// applyPluginDefaults injects default plugins into the configuration
func applyPluginDefaults(rawConfig *configapi.PayloadProcessorConfig, handle plugin.Handle) error {
	if rawConfig.ProfilePicker == nil || rawConfig.ProfilePicker.PluginRef == "" {
		var profilePicker requesthandling.ProfilePicker
		for _, rawPlugin := range handle.GetAllPlugins() {
			if aProfilePicker, ok := rawPlugin.(requesthandling.ProfilePicker); ok {
				if profilePicker != nil {
					return errors.New("multiple profile pickers have been defined in the configuration")
				}
				profilePicker = aProfilePicker
			}
		}

		if profilePicker == nil {
			if len(rawConfig.Profiles) == 1 {
				if err := registerDefaultPlugin(rawConfig, handle, single.SingleProfilePickerType); err != nil {
					return err
				}
				profilePicker = handle.Plugin(single.SingleProfilePickerType).(requesthandling.ProfilePicker)
			} else {
				return errors.New("multiple profiles in the configuration require a profile picker")
			}
		}

		if rawConfig.ProfilePicker == nil {
			rawConfig.ProfilePicker = &configapi.PluginRef{}
		}
		rawConfig.ProfilePicker.PluginRef = profilePicker.TypedName().Name
	}

	return nil
}

// registerDefaultPlugin instantiates a plugin with empty configuration (defaults) and adds it to both the handle and
// the config spec.
func registerDefaultPlugin(
	rawConfig *configapi.PayloadProcessorConfig,
	handle plugin.Handle,
	pluginType string,
) error {
	factory, ok := plugin.Registry[pluginType]
	if !ok {
		return fmt.Errorf("plugin type '%s' not found in registry", pluginType)
	}

	plugin, err := factory(pluginType, nil, handle) // default plugins have no parameters
	if err != nil {
		return fmt.Errorf("failed to instantiate default plugin '%s': %w", pluginType, err)
	}

	handle.AddPlugin(pluginType, plugin)
	rawConfig.Plugins = append(rawConfig.Plugins, configapi.PluginSpec{
		Name: pluginType,
		Type: pluginType,
	})

	return nil
}
