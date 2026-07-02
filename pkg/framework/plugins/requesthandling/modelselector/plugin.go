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
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/log"

	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	ms "github.com/llm-d/llm-d-inference-payload-processor/pkg/modelselector"
)

const (
	ModelSelectorPluginType = "model-selector"
)

var _ requesthandling.RequestProcessor = &ModelSelectorPlugin{}

// ModelSelectorPluginFactory is the factory function for the ModelSelector RequestProcessor plugin.
// It creates a plugin with an empty pipeline; plugins are wired in by the configuration loader.
func ModelSelectorPluginFactory(name string, _ json.RawMessage, handle plugin.Handle) (plugin.Plugin, error) {
	return NewModelSelectorPlugin(ms.NewModelSelectorPipeline(), handle.Datastore()).WithName(name), nil
}

// NewModelSelectorPlugin creates a ModelSelector RequestProcessor plugin.
// Candidate models are read from the Datastore on each request.
// Plugins are added to the pipeline via AddPlugins after construction.
func NewModelSelectorPlugin(pipeline *ms.ModelSelectorPipeline, datastore datalayer.Datastore) *ModelSelectorPlugin {
	return &ModelSelectorPlugin{
		typedName: plugin.TypedName{Type: ModelSelectorPluginType, Name: ModelSelectorPluginType},
		selector:  ms.NewModelSelector(pipeline),
		datastore: datastore,
	}
}

// ModelSelectorPlugin is a RequestProcessor that runs the ModelSelector
// pipeline (Filter → Score → Pick) to select a model for the request.
// Candidate models are read from the Datastore on each request.
type ModelSelectorPlugin struct {
	typedName plugin.TypedName
	selector  *ms.ModelSelector
	datastore datalayer.Datastore
}

func (p *ModelSelectorPlugin) TypedName() plugin.TypedName {
	return p.typedName
}

// WithName sets the plugin name and returns the plugin for method chaining.
func (p *ModelSelectorPlugin) WithName(name string) *ModelSelectorPlugin {
	p.typedName.Name = name
	return p
}

// ProcessRequest reads candidate models from the Datastore, runs model
// selection, and writes the selected model into the request body and CycleState.
func (p *ModelSelectorPlugin) ProcessRequest(ctx context.Context, cycleState *plugin.CycleState, request *requesthandling.InferenceRequest) error {
	logger := log.FromContext(ctx)

	candidateModels := p.loadCandidateModels()
	if len(candidateModels) == 0 {
		return errors.New("no candidate models available in datastore")
	}

	result, err := p.selector.Select(ctx, request, cycleState, candidateModels)
	if err != nil {
		return fmt.Errorf("model selection failed: %w", err)
	}

	selectedName := result.TargetModel.GetName()
	logger.V(logutil.VERBOSE).Info("Model selected", "model", selectedName)

	request.SetBodyField("model", selectedName)

	return nil
}

// Pipeline returns the ModelSelectorPipeline used by this plugin.
func (p *ModelSelectorPlugin) Pipeline() *ms.ModelSelectorPipeline {
	return p.selector.Pipeline()
}

// AddPlugins adds the given plugins to the model selector pipeline.
func (p *ModelSelectorPlugin) AddPlugins(plugins ...plugin.Plugin) error {
	return p.selector.Pipeline().AddPlugins(plugins...)
}

// loadCandidateModels reads all known models from the Datastore.
func (p *ModelSelectorPlugin) loadCandidateModels() []datalayer.Model {
	modelNames := p.datastore.Models()
	candidates := make([]datalayer.Model, len(modelNames))
	for i, name := range modelNames {
		candidates[i] = p.datastore.GetOrCreateModel(name)
	}
	return candidates
}
