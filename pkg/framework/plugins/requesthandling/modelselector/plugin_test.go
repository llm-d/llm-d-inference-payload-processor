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
	"slices"
	"testing"

	ctrlbuilder "sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/datastore"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/modelselector"
	fwkplugin "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/modelselector/picker/maxscore"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/modelselector/scorer/costaware"
	ms "github.com/llm-d/llm-d-inference-payload-processor/pkg/modelselector"
)

// fakeHandle implements plugin.Handle for unit tests.
type fakeHandle struct {
	ds      datalayer.Datastore
	plugins map[string]fwkplugin.Plugin
}

func (f *fakeHandle) Context() context.Context                { return context.Background() }
func (f *fakeHandle) Client() client.Client                   { return nil }
func (f *fakeHandle) ReconcilerBuilder() *ctrlbuilder.Builder { return nil }
func (f *fakeHandle) Datastore() datalayer.Datastore          { return f.ds }
func (f *fakeHandle) EventNotifier() datalayer.EventNotifier  { return nil }

func (f *fakeHandle) Plugin(name string) fwkplugin.Plugin { return f.plugins[name] }
func (f *fakeHandle) AddPlugin(name string, p fwkplugin.Plugin) {
	f.plugins[name] = p
}
func (f *fakeHandle) GetAllPlugins() []fwkplugin.Plugin {
	result := make([]fwkplugin.Plugin, 0, len(f.plugins))
	for _, p := range f.plugins {
		result = append(result, p)
	}
	return result
}
func (f *fakeHandle) GetAllPluginsWithNames() map[string]fwkplugin.Plugin { return f.plugins }

func newTestDatastore(modelNames ...string) datalayer.Datastore {
	ds := datastore.NewFakeDataStore()
	for _, name := range modelNames {
		ds.GetOrCreateModel(name)
	}
	return ds
}

// newFakeHandle creates a fakeHandle with a datastore pre-populated with the given model names
// and no additional plugins configured.
func newFakeHandle(modelNames ...string) *fakeHandle {
	return &fakeHandle{
		ds:      newTestDatastore(modelNames...),
		plugins: map[string]fwkplugin.Plugin{},
	}
}

// mustFactory calls ModelSelectorPluginFactory and fails the test on error.
func mustFactory(t *testing.T, handle *fakeHandle) *ModelSelectorPlugin {
	t.Helper()
	plug, err := ModelSelectorPluginFactory(ModelSelectorPluginType, json.RawMessage(`{}`), handle)
	if err != nil {
		t.Fatalf("ModelSelectorPluginFactory failed: %v", err)
	}
	return plug.(*ModelSelectorPlugin)
}

// mustAddPlugins calls AddPlugins and fails the test on error.
func mustAddPlugins(t *testing.T, p *ModelSelectorPlugin, plugins ...fwkplugin.Plugin) {
	t.Helper()
	if err := p.AddPlugins(plugins...); err != nil {
		t.Fatalf("AddPlugins failed: %v", err)
	}
}

// TestProcessRequestSelectsFromDatastoreModels checks that the selected model is one of the candidates registered in the datastore.
func TestProcessRequestSelectsFromDatastoreModels(t *testing.T) {
	candidates := []string{"llama-70b", "llama-8b", "mistral-7b"}
	p := mustFactory(t, newFakeHandle(candidates...))
	mustAddPlugins(t, p, maxscore.NewMaxScorePicker())

	request := requesthandling.NewInferenceRequest()
	request.Body["model"] = "auto"
	cycleState := fwkplugin.NewCycleState()

	if err := p.ProcessRequest(context.Background(), cycleState, request); err != nil {
		t.Fatalf("ProcessRequest failed: %v", err)
	}

	selectedModel := request.Body["model"].(string)
	if !slices.Contains(candidates, selectedModel) {
		t.Errorf("selected model %q is not in datastore models %v", selectedModel, candidates)
	}

	storedModel, err := fwkplugin.ReadCycleStateKey[string](cycleState, SelectedModelCycleStateKey)
	if err != nil {
		t.Fatalf("expected selected model in CycleState: %v", err)
	}
	if storedModel != selectedModel {
		t.Errorf("CycleState model %q != body model %q", storedModel, selectedModel)
	}
}

// TestProcessRequestFailsWithEmptyDatastore checks that ProcessRequest returns an error when no candidate models are available.
func TestProcessRequestFailsWithEmptyDatastore(t *testing.T) {
	p := mustFactory(t, newFakeHandle())
	mustAddPlugins(t, p, maxscore.NewMaxScorePicker())

	request := requesthandling.NewInferenceRequest()
	request.Body["model"] = "auto"
	cycleState := fwkplugin.NewCycleState()

	if err := p.ProcessRequest(context.Background(), cycleState, request); err == nil {
		t.Fatal("expected error with empty datastore")
	}
}

// TestTypedName checks that the plugin's TypedName type matches the registered ModelSelectorPluginType constant.
func TestTypedName(t *testing.T) {
	thePlugin := mustFactory(t, newFakeHandle("model-a"))
	if thePlugin.TypedName().Type != ModelSelectorPluginType {
		t.Errorf("expected type %q, got %q", ModelSelectorPluginType, thePlugin.TypedName().Type)
	}
}

// TestAddPluginsWiresScorer checks that a WeightedScorer added via AddPlugins appears in the pipeline.
func TestAddPluginsWiresScorer(t *testing.T) {
	p := mustFactory(t, newFakeHandle("model-a", "model-b"))
	scorer := costaware.NewCostScorer()
	mustAddPlugins(t, p, ms.NewWeightedScorer(scorer, 2.0))

	scorers := p.Pipeline().Scorers()
	if len(scorers) != 1 || scorers[0].TypedName().Type != costaware.CostScorerType {
		t.Errorf("expected one scorer of type %q, got %v", costaware.CostScorerType, scorers)
	}
	if scorers[0].Weight() != 2.0 {
		t.Errorf("expected scorer weight 2.0, got %v", scorers[0].Weight())
	}
}

// TestAddPluginsWiresPicker checks that a Picker added via AddPlugins is registered in the pipeline.
func TestAddPluginsWiresPicker(t *testing.T) {
	p := mustFactory(t, newFakeHandle("model-a"))
	mustAddPlugins(t, p, maxscore.NewMaxScorePicker())

	got := p.Pipeline().Picker()
	if got == nil || got.TypedName().Type != maxscore.MaxScorePickerType {
		t.Errorf("expected picker type %q, got %v", maxscore.MaxScorePickerType, got)
	}
}

// TestAddPluginsRejectsMultiplePickers checks that adding a second Picker returns an error.
func TestAddPluginsRejectsMultiplePickers(t *testing.T) {
	p1 := maxscore.NewMaxScorePicker().WithName("picker-1")
	p2 := maxscore.NewMaxScorePicker().WithName("picker-2")
	p := mustFactory(t, newFakeHandle("model-a"))
	mustAddPlugins(t, p, p1)

	if err := p.AddPlugins(p2); err == nil {
		t.Fatal("expected error when adding a second picker")
	}
}

// TestAddPluginsRejectsScorerWithoutWeight checks that passing a raw Scorer (not wrapped in WeightedScorer) returns an error.
func TestAddPluginsRejectsScorerWithoutWeight(t *testing.T) {
	scorer := costaware.NewCostScorer()
	p := mustFactory(t, newFakeHandle("model-a"))

	if err := p.AddPlugins(scorer); err == nil {
		t.Fatal("expected error when scorer has no weight")
	}
}

// fakeScorerFilter implements both modelselector.Scorer and modelselector.Filter.
type fakeScorerFilter struct{ typedName fwkplugin.TypedName }

func (f *fakeScorerFilter) TypedName() fwkplugin.TypedName { return f.typedName }
func (f *fakeScorerFilter) Score(_ context.Context, _ *fwkplugin.CycleState, _ *requesthandling.InferenceRequest, models []datalayer.Model) map[datalayer.Model]float64 {
	out := make(map[datalayer.Model]float64, len(models))
	for _, m := range models {
		out[m] = 1.0
	}
	return out
}
func (f *fakeScorerFilter) Filter(_ context.Context, _ *fwkplugin.CycleState, _ *requesthandling.InferenceRequest, models []datalayer.Model) []datalayer.Model {
	return models
}

var _ modelselector.Scorer = &fakeScorerFilter{}
var _ modelselector.Filter = &fakeScorerFilter{}

// TestAddPluginsPluginImplementingBothScorerAndFilter checks that a plugin implementing both Scorer and Filter is registered in both roles.
func TestAddPluginsPluginImplementingBothScorerAndFilter(t *testing.T) {
	dual := &fakeScorerFilter{typedName: fwkplugin.TypedName{Type: "dual", Name: "dual"}}
	p := mustFactory(t, newFakeHandle("model-a"))
	mustAddPlugins(t, p, ms.NewWeightedScorer(dual, 1.0))

	pipeline := p.Pipeline()
	if len(pipeline.Filters()) != 1 || pipeline.Filters()[0].TypedName().Name != "dual" {
		t.Errorf("expected dual in filters, got %v", pipeline.Filters())
	}
	if len(pipeline.Scorers()) != 1 || pipeline.Scorers()[0].TypedName().Name != "dual" {
		t.Errorf("expected dual in scorers, got %v", pipeline.Scorers())
	}
}
