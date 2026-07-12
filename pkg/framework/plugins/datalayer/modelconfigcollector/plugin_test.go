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

package modelconfigcollector

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	ctrlbuilder "sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/datastore"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer/pricing"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
)

// fakeHandle implements plugin.Handle for unit tests.
type fakeHandle struct{ ds datalayer.Datastore }

func (f *fakeHandle) Context() context.Context                         { return context.Background() }
func (f *fakeHandle) Client() client.Client                            { return nil }
func (f *fakeHandle) ReconcilerBuilder() *ctrlbuilder.Builder          { return nil }
func (f *fakeHandle) Datastore() datalayer.Datastore                   { return f.ds }
func (f *fakeHandle) EventNotifier() datalayer.EventNotifier           { return nil }
func (f *fakeHandle) Plugin(string) plugin.Plugin                      { return nil }
func (f *fakeHandle) AddPlugin(string, plugin.Plugin)                  {}
func (f *fakeHandle) GetAllPlugins() []plugin.Plugin                   { return nil }
func (f *fakeHandle) GetAllPluginsWithNames() map[string]plugin.Plugin { return nil }

// useFactory creates a datasource via DatasourceFactory or fails the test.
func useFactory(t *testing.T, path string, ds datalayer.Datastore) *ModelConfigDataSource {
	t.Helper()
	rawCfg, _ := json.Marshal(PluginConfig{ModelsPath: path})
	p, err := DatasourceFactory("test", rawCfg, &fakeHandle{ds: ds})
	if err != nil {
		t.Fatalf("DatasourceFactory: %v", err)
	}
	return p.(*ModelConfigDataSource)
}

func writeTempModelsConfig(t *testing.T, cfg ModelsConfig) string {
	t.Helper()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return writeTempRaw(t, string(data))
}

func writeTempRaw(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "models-*.json")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	f.Close()
	return f.Name()
}

func overwriteFile(t *testing.T, path string, cfg ModelsConfig) {
	t.Helper()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("overwrite file: %v", err)
	}
}

func waitForModels(t *testing.T, ds datalayer.Datastore, wantCount int, timeout time.Duration) []datalayer.Model {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if models := ds.GetModels(datalayer.AllModelsPredicate); len(models) == wantCount {
			return models
		}
		time.Sleep(20 * time.Millisecond)
	}
	return ds.GetModels(datalayer.AllModelsPredicate)
}

// --- Factory-level tests ---

// TestDatasourceFactory_InvalidJSON ensures the factory rejects a plugin config
// that is not valid JSON at all.
func TestDatasourceFactory_InvalidJSON(t *testing.T) {
	ds := datastore.NewFakeDataStore()
	_, err := DatasourceFactory("x", json.RawMessage(`not-json`), &fakeHandle{ds: ds})
	if err == nil {
		t.Error("expected error for invalid JSON plugin config, got nil")
	}
}

// TestDatasourceFactory_EmptyInput ensures the factory rejects an empty config payload.
func TestDatasourceFactory_EmptyInput(t *testing.T) {
	ds := datastore.NewFakeDataStore()
	_, err := DatasourceFactory("x", json.RawMessage(``), &fakeHandle{ds: ds})
	if err == nil {
		t.Error("expected error for empty plugin config input, got nil")
	}
}

// TestDatasourceFactory_MissingModelsPath ensures the factory rejects a config where
// modelsPath is absent (empty string), which would make the datasource inoperable.
func TestDatasourceFactory_MissingModelsPath(t *testing.T) {
	ds := datastore.NewFakeDataStore()
	rawCfg, _ := json.Marshal(PluginConfig{}) // modelsPath omitted → empty string
	_, err := DatasourceFactory("x", rawCfg, &fakeHandle{ds: ds})
	if err == nil {
		t.Error("expected error for missing modelsPath, got nil")
	}
}

// TestDatasourceFactory_FileNotExist ensures the factory rejects a config that points
// to a file that does not exist on disk, catching misconfiguration at startup.
func TestDatasourceFactory_FileNotExist(t *testing.T) {
	ds := datastore.NewFakeDataStore()
	rawCfg, _ := json.Marshal(PluginConfig{ModelsPath: "/no/such/file.json"})
	_, err := DatasourceFactory("x", rawCfg, &fakeHandle{ds: ds})
	if err == nil {
		t.Error("expected error for non-existent file, got nil")
	}
}

// TestDatasourceFactory_DirectoryNotFile ensures the factory rejects a config that points
// to a directory instead of a file, preventing runtime errors.
func TestDatasourceFactory_DirectoryNotFile(t *testing.T) {
	ds := datastore.NewFakeDataStore()
	dir := t.TempDir()
	rawCfg, _ := json.Marshal(PluginConfig{ModelsPath: dir})
	_, err := DatasourceFactory("x", rawCfg, &fakeHandle{ds: ds})
	if err == nil {
		t.Error("expected error for directory path, got nil")
	}
}

// --- Start-level tests ---

// TestStart_LoadsModels verifies that all models listed in the config file are present
// in the datastore immediately after Start returns, before any file-change event fires.
func TestStart_LoadsModels(t *testing.T) {
	ds := datastore.NewFakeDataStore()
	path := writeTempModelsConfig(t, ModelsConfig{
		Models: []ModelConfiguration{{Name: "m1"}, {Name: "m2"}},
	})
	c := useFactory(t, path, ds)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); c.Stop() })

	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	models := ds.GetModels(datalayer.AllModelsPredicate)
	if len(models) != 2 {
		t.Errorf("expected 2 models after Start, got %d: %v", len(models), models)
	}
}

// TestStart_InvalidFileContent verifies that Start returns an error when the models
// file exists but contains content that cannot be parsed as valid JSON.
func TestStart_InvalidFileContent(t *testing.T) {
	ds := datastore.NewFakeDataStore()
	path := writeTempRaw(t, `this is not valid json {{{`)
	rawCfg, _ := json.Marshal(PluginConfig{ModelsPath: path})
	p, err := DatasourceFactory("x", rawCfg, &fakeHandle{ds: ds})
	if err != nil {
		t.Fatalf("DatasourceFactory: %v", err)
	}
	c := p.(*ModelConfigDataSource)

	if err := c.Start(context.Background()); err == nil {
		t.Error("expected Start to return error for invalid JSON file content, got nil")
	}
}

// TestStart_SkipsEmptyName verifies that model entries with an empty name field are
// silently ignored and do not create a blank entry in the datastore.
func TestStart_SkipsEmptyName(t *testing.T) {
	ds := datastore.NewFakeDataStore()
	path := writeTempModelsConfig(t, ModelsConfig{
		Models: []ModelConfiguration{{Name: ""}, {Name: "valid-model"}},
	})
	c := useFactory(t, path, ds)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); c.Stop() })

	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	models := ds.GetModels(datalayer.AllModelsPredicate)
	if len(models) != 1 || models[0].GetName() != "valid-model" {
		t.Errorf("expected only [valid-model], got %v", models)
	}
}

// TestStart_RemovesStaleModels verifies that models already in the datastore but absent
// from the config file are removed during the initial sync performed by Start.
func TestStart_RemovesStaleModels(t *testing.T) {
	ds := datastore.NewFakeDataStore()
	ds.GetOrCreateModel("stale-model")

	path := writeTempModelsConfig(t, ModelsConfig{
		Models: []ModelConfiguration{{Name: "current-model"}},
	})
	c := useFactory(t, path, ds)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); c.Stop() })

	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	models := ds.GetModels(datalayer.AllModelsPredicate)
	if len(models) != 1 || models[0].GetName() != "current-model" {
		t.Errorf("expected only [current-model], got %v", models)
	}
}

// TestStart_FileChange_AddsModel verifies that writing a new model into the config file
// after Start causes the watcher to pick up the change and register the new model.
func TestStart_FileChange_AddsModel(t *testing.T) {
	ds := datastore.NewFakeDataStore()
	path := writeTempModelsConfig(t, ModelsConfig{
		Models: []ModelConfiguration{{Name: "m1"}},
	})
	c := useFactory(t, path, ds)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); c.Stop() })

	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	overwriteFile(t, path, ModelsConfig{
		Models: []ModelConfiguration{{Name: "m1"}, {Name: "m2"}},
	})

	models := waitForModels(t, ds, 2, 2*time.Second)
	if len(models) != 2 {
		t.Errorf("expected 2 models after file update, got %d: %v", len(models), models)
	}
}

// TestStart_FileChange_RemovesModel verifies that removing a model from the config file
// after Start causes the watcher to pick up the change and delete the model from the datastore.
func TestStart_FileChange_RemovesModel(t *testing.T) {
	ds := datastore.NewFakeDataStore()
	path := writeTempModelsConfig(t, ModelsConfig{
		Models: []ModelConfiguration{{Name: "m1"}, {Name: "m2"}},
	})
	c := useFactory(t, path, ds)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); c.Stop() })

	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	overwriteFile(t, path, ModelsConfig{
		Models: []ModelConfiguration{{Name: "m1"}},
	})

	models := waitForModels(t, ds, 1, 2*time.Second)
	if len(models) != 1 || models[0].GetName() != "m1" {
		t.Errorf("expected only [m1] after file update, got %v", models)
	}
}

// TestStop_CleanShutdown verifies that Stop returns within a reasonable timeout and
// does not leak the watcher goroutine.
func TestStop_CleanShutdown(t *testing.T) {
	ds := datastore.NewFakeDataStore()
	path := writeTempModelsConfig(t, ModelsConfig{Models: []ModelConfiguration{{Name: "m1"}}})
	c := useFactory(t, path, ds)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	done := make(chan struct{})
	go func() {
		c.Stop()
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Error("Stop did not return within timeout")
	}
}

// priceTestModelName is the model name shared by the price-related tests below.
const priceTestModelName = "m1"

// priceFloatEpsilon is the tolerance used when comparing per-token prices.
// 1e-15 is well below the precision of an IEEE-754 double for the small values
// produced by dividing single-digit per-million prices by 1e6.
const priceFloatEpsilon = 1e-15

// readTokenPrices fetches the *pricing.TokenPrices stored on priceTestModelName.
// It fails the test if the attribute is missing or of the wrong type.
func readTokenPrices(t *testing.T, ds datalayer.Datastore) *pricing.TokenPrices {
	t.Helper()
	v, ok := ds.GetOrCreateModel(priceTestModelName).GetAttributes().Get(pricing.TokenPricesAttributeKey)
	if !ok {
		t.Fatalf("model %q: attribute %q not present", priceTestModelName, pricing.TokenPricesAttributeKey)
	}
	tp, ok := v.(*pricing.TokenPrices)
	if !ok {
		t.Fatalf("model %q: attribute %q is %T, want *pricing.TokenPrices", priceTestModelName, pricing.TokenPricesAttributeKey, v)
	}
	return tp
}

// waitForTokenPrices polls until the *pricing.TokenPrices on priceTestModelName matches
// want (both fields within priceFloatEpsilon) or the deadline expires. Returns the last
// observed value (zero-valued *TokenPrices if nothing was ever observed).
func waitForTokenPrices(t *testing.T, ds datalayer.Datastore, want *pricing.TokenPrices, timeout time.Duration) *pricing.TokenPrices {
	t.Helper()
	deadline := time.Now().Add(timeout)
	got := &pricing.TokenPrices{}
	for time.Now().Before(deadline) {
		v, ok := ds.GetOrCreateModel(priceTestModelName).GetAttributes().Get(pricing.TokenPricesAttributeKey)
		if ok {
			if tp, ok := v.(*pricing.TokenPrices); ok {
				got = tp
				if floatCloseEnough(got.InputTokenPrice, want.InputTokenPrice) &&
					floatCloseEnough(got.OutputTokenPrice, want.OutputTokenPrice) {
					return got
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return got
}

// TestStart_PopulatesPrices verifies that the per-million-tokens prices in the
// config's nested "pricing" block are stored on the registered Model as per-token
// prices (each field divided by 1e6) inside a single *pricing.TokenPrices attribute.
func TestStart_PopulatesPrices(t *testing.T) {
	ds := datastore.NewFakeDataStore()
	path := writeTempModelsConfig(t, ModelsConfig{
		Models: []ModelConfiguration{{
			Name:    priceTestModelName,
			Pricing: pricing.ModelPriceShape{InputPerMillion: 2.0, OutputPerMillion: 8.0},
		}},
	})
	c := useFactory(t, path, ds)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); c.Stop() })

	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	tp := readTokenPrices(t, ds)
	if !floatCloseEnough(tp.InputTokenPrice, 2.0/1e6) {
		t.Errorf("InputTokenPrice = %v, want %v", tp.InputTokenPrice, 2.0/1e6)
	}
	if !floatCloseEnough(tp.OutputTokenPrice, 8.0/1e6) {
		t.Errorf("OutputTokenPrice = %v, want %v", tp.OutputTokenPrice, 8.0/1e6)
	}
}

// TestStart_OmittedPricingDefaultsToFreeModel verifies that a model entry whose
// "pricing" block is entirely omitted from the JSON config is still registered
// in the datastore AND has the TokenPrices attribute populated with 0/0, i.e.
// it is treated as a free model. This guarantees that downstream consumers can
// always read TokenPricesAttributeKey unconditionally without first checking
// whether the operator supplied pricing for that model.
func TestStart_OmittedPricingDefaultsToFreeModel(t *testing.T) {
	ds := datastore.NewFakeDataStore()
	path := writeTempRaw(t, `{"models":[{"name":"m1"}]}`)
	rawCfg, _ := json.Marshal(PluginConfig{ModelsPath: path})
	p, err := DatasourceFactory("x", rawCfg, &fakeHandle{ds: ds})
	if err != nil {
		t.Fatalf("DatasourceFactory: %v", err)
	}
	c := p.(*ModelConfigDataSource)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); c.Stop() })

	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if models := ds.Models(); len(models) != 1 || models[0] != priceTestModelName {
		t.Fatalf("expected exactly [%q] registered, got %v", priceTestModelName, models)
	}
	tp := readTokenPrices(t, ds)
	if tp.InputTokenPrice != 0 || tp.OutputTokenPrice != 0 {
		t.Errorf("TokenPrices = %+v, want {0, 0} (free model)", tp)
	}
}

// TestStart_PricingPresentButEmpty verifies the same free-model invariant when
// the pricing block is present but contains no fields ("pricing":{}). Locks down
// the "absent means empty" semantics: the operator can write either form and get
// the same result.
func TestStart_PricingPresentButEmpty(t *testing.T) {
	ds := datastore.NewFakeDataStore()
	path := writeTempRaw(t, `{"models":[{"name":"m1","pricing":{}}]}`)
	rawCfg, _ := json.Marshal(PluginConfig{ModelsPath: path})
	p, err := DatasourceFactory("x", rawCfg, &fakeHandle{ds: ds})
	if err != nil {
		t.Fatalf("DatasourceFactory: %v", err)
	}
	c := p.(*ModelConfigDataSource)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); c.Stop() })

	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	tp := readTokenPrices(t, ds)
	if tp.InputTokenPrice != 0 || tp.OutputTokenPrice != 0 {
		t.Errorf("TokenPrices = %+v, want {0, 0} (free model)", tp)
	}
}

// TestStart_SkipsNegativePrice verifies that a model entry with a negative input
// or output price is skipped (not registered) and does not block other valid entries.
func TestStart_SkipsNegativePrice(t *testing.T) {
	ds := datastore.NewFakeDataStore()
	path := writeTempModelsConfig(t, ModelsConfig{
		Models: []ModelConfiguration{
			{Name: "bad-input", Pricing: pricing.ModelPriceShape{InputPerMillion: -1.0}},
			{Name: "bad-output", Pricing: pricing.ModelPriceShape{OutputPerMillion: -1.0}},
			{Name: "ok", Pricing: pricing.ModelPriceShape{InputPerMillion: 1.0, OutputPerMillion: 2.0}},
		},
	})
	c := useFactory(t, path, ds)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); c.Stop() })

	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	models := ds.Models()
	if len(models) != 1 || models[0] != "ok" {
		t.Errorf("expected only [ok] after Start, got %v", models)
	}
}

// floatCloseEnough returns true when a and b agree within priceFloatEpsilon.
func floatCloseEnough(a, b float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < priceFloatEpsilon
}
