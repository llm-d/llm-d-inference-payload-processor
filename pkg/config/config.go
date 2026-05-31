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

package config

import (
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer/datasource"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
)

// Config contains the final configuration loaded by the configuration loader
type Config struct {
	// PreProcessors are the pre-processing plugin instances executed by the request handler,
	// in the same order provided in the configuration file.
	PreProcessors []requesthandling.PreProcessor

	// ProfilePicker picks the profile to be run as the pipeline for the incoming requests
	ProfilePicker requesthandling.ProfilePicker

	// Profiles is the set of pipeline profiles loaded from the configuration file
	Profiles map[string]requesthandling.Profile

	// PostProcessors are the response processing plugin instances executed by the response handler,
	// in the same order provided in the configuration file.
	PostProcessors []requesthandling.PostProcessor

	// NotificationSources are the notification-source plugin instances to start.
	NotificationSources []datasource.NotificationSource
}
