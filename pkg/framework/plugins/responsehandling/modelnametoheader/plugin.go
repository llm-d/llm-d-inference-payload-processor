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

package modelnametoheader

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/log"

	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/requesthandling/modelselector"
)

const (
	PluginType      = "model-name-to-header"
	ModelNameHeader = "X-Gateway-Model-Name"
)

var _ requesthandling.ResponseHeadersProcessor = &ModelNameToHeaderPlugin{}

type modelNameToHeaderConfig struct {
	HeaderName string `json:"headerName"`
}

// PluginFactory creates a new ModelNameToHeaderPlugin.
func PluginFactory(name string, rawParameters json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
	var cfg modelNameToHeaderConfig
	if len(rawParameters) > 0 {
		if err := json.Unmarshal(rawParameters, &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse the parameters of the '%s' plugin - %w", PluginType, err)
		}
	}

	headerName := cfg.HeaderName
	if headerName == "" {
		headerName = ModelNameHeader
	}

	return &ModelNameToHeaderPlugin{
		typedName:  plugin.TypedName{Type: PluginType, Name: name},
		headerName: headerName,
	}, nil
}

// ModelNameToHeaderPlugin is a ResponseHeadersProcessor that echoes the model name
// selected during request processing back to the client as a response header.
// It runs during the response-headers phase so it works for both streaming and
// buffered responses.
type ModelNameToHeaderPlugin struct {
	typedName  plugin.TypedName
	headerName string
}

func (p *ModelNameToHeaderPlugin) TypedName() plugin.TypedName {
	return p.typedName
}

func (p *ModelNameToHeaderPlugin) ProcessResponseHeaders(ctx context.Context, cycleState *plugin.CycleState, response *requesthandling.InferenceResponse) error {
	selectedModel, err := plugin.ReadCycleStateKey[string](cycleState, modelselector.SelectedModelCycleStateKey)
	if err != nil {
		if errors.Is(err, plugin.ErrNotFound) {
			log.FromContext(ctx).V(logutil.VERBOSE).Info("no selected model in CycleState, skipping")
			return nil
		}
		return fmt.Errorf("failed to read selected model from CycleState: %w", err)
	}

	response.SetHeader(p.headerName, selectedModel)

	return nil
}
