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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configapi "github.com/llm-d/llm-d-inference-payload-processor/apix/config/v1alpha1"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/requesthandling/basemodelextractor"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/requesthandling/bodyfieldtoheader"
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
