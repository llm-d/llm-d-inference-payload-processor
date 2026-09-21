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

package plugin

import (
	"encoding/json"
)

// Factory is the definition of the factory functions that are used to instantiate plugins
// specified in a configuration. The framework provides a strict decoder
// (DisallowUnknownFields) over the plugin's raw parameters, or nil when the plugin was
// instantiated without parameters. Factories that ignore parameters can take the decoder
// as `_ *json.Decoder`.
type FactoryFunc func(name string, parameters *json.Decoder, handle Handle) (Plugin, error)

// Register is a static function that can be called to register plugin factory functions.
func Register(pluginType string, factory FactoryFunc) {
	Registry[pluginType] = factory
}

// Registry is a mapping from plugin name to Factory function
var Registry map[string]FactoryFunc = map[string]FactoryFunc{}
