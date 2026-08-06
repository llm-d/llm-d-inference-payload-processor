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
	"slices"
	"testing"
	"time"

	ctrlbuilder "sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/datastore"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer/modelgroups"
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

// startFactory creates a datasource via useFactory, starts it, and registers a
// cleanup that stops it. Fails the test if Start returns an error.
func startFactory(t *testing.T, path string, ds datalayer.Datastore) {
	t.Helper()
	c := useFactory(t, path, ds)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); c.Stop() })
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
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

// readFile reads path and fails the test on error, for tests calling syncModels directly.
func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	return data
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

// waitForUpdatedConfig polls until the datastore reflects wantCount models
// (proving the fsnotify watcher picked up a file change and re-ran syncModels)
// or the deadline expires.
func waitForUpdatedConfig(t *testing.T, ds datalayer.Datastore, wantCount int, timeout time.Duration) []datalayer.Model {
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
	startFactory(t, path, ds)

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
	startFactory(t, path, ds)

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
	startFactory(t, path, ds)

	models := ds.GetModels(datalayer.AllModelsPredicate)
	if len(models) != 1 || models[0].GetName() != "current-model" {
		t.Errorf("expected only [current-model], got %v", models)
	}
}

// TestStart_WatcherTriggersResync verifies that writing to the config file after
// Start causes the fsnotify watcher to invoke syncModels again. It is the sole
// test of the watcher wiring itself; per-diff sync outcomes (add/remove/etc.)
// are covered directly against syncModels in the TestSyncModels_FileChange_*
// tests below, which don't need the watcher or polling.
func TestStart_WatcherTriggersResync(t *testing.T) {
	ds := datastore.NewFakeDataStore()
	path := writeTempModelsConfig(t, ModelsConfig{
		Models: []ModelConfiguration{{Name: "m1"}},
	})
	startFactory(t, path, ds)

	overwriteFile(t, path, ModelsConfig{
		Models: []ModelConfiguration{{Name: "m1"}, {Name: "m2"}},
	})

	models := waitForUpdatedConfig(t, ds, 2, 2*time.Second)
	if len(models) != 2 {
		t.Errorf("expected 2 models after file update, got %d: %v", len(models), models)
	}
}

// TestSyncModels_FileChange_RemovesModel verifies that a second syncModels call,
// after a model has been removed from the config file, deletes that model from
// the datastore. Calls syncModels directly (no Start/watcher) since this is a
// pure re-sync outcome, not a test of the watcher wiring.
func TestSyncModels_FileChange_RemovesModel(t *testing.T) {
	ds := datastore.NewFakeDataStore()
	path := writeTempModelsConfig(t, ModelsConfig{
		Models: []ModelConfiguration{{Name: "m1"}, {Name: "m2"}},
	})
	c := useFactory(t, path, ds)
	if err := c.syncModels(context.Background(), readFile(t, path)); err != nil {
		t.Fatalf("syncModels: %v", err)
	}

	overwriteFile(t, path, ModelsConfig{
		Models: []ModelConfiguration{{Name: "m1"}},
	})
	if err := c.syncModels(context.Background(), readFile(t, path)); err != nil {
		t.Fatalf("syncModels: %v", err)
	}

	models := ds.GetModels(datalayer.AllModelsPredicate)
	if len(models) != 1 || models[0].GetName() != "m1" {
		t.Errorf("expected only [m1] after resync, got %v", models)
	}
}

// TestStart_WatcherClearsModelsOnRemove verifies that removing the config file after
// Start causes the fsnotify watcher to clear every model from the datastore, treating
// the now-missing file as an empty config rather than leaving stale entries in place.
func TestStart_WatcherClearsModelsOnRemove(t *testing.T) {
	ds := datastore.NewFakeDataStore()
	path := writeTempModelsConfig(t, ModelsConfig{
		Models: []ModelConfiguration{{Name: "m1"}, {Name: "m2"}},
	})
	startFactory(t, path, ds)

	if models := ds.GetModels(datalayer.AllModelsPredicate); len(models) != 2 {
		t.Fatalf("expected 2 models before removal, got %d: %v", len(models), models)
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove config file: %v", err)
	}

	models := waitForUpdatedConfig(t, ds, 0, 2*time.Second)
	if len(models) != 0 {
		t.Errorf("expected 0 models after config file removal, got %d: %v", len(models), models)
	}
}

// TestStart_WatcherClearsModelsOnRename verifies that renaming the config file away
// (e.g. an atomic ConfigMap-style replacement that doesn't immediately re-create the
// original path) causes the fsnotify watcher to clear every model from the datastore.
func TestStart_WatcherClearsModelsOnRename(t *testing.T) {
	ds := datastore.NewFakeDataStore()
	path := writeTempModelsConfig(t, ModelsConfig{
		Models: []ModelConfiguration{{Name: "m1"}, {Name: "m2"}},
	})
	startFactory(t, path, ds)

	if models := ds.GetModels(datalayer.AllModelsPredicate); len(models) != 2 {
		t.Fatalf("expected 2 models before rename, got %d: %v", len(models), models)
	}

	if err := os.Rename(path, path+".bak"); err != nil {
		t.Fatalf("rename config file: %v", err)
	}

	models := waitForUpdatedConfig(t, ds, 0, 2*time.Second)
	if len(models) != 0 {
		t.Errorf("expected 0 models after config file rename, got %d: %v", len(models), models)
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
	startFactory(t, path, ds)

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
	startFactory(t, path, ds)

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
	startFactory(t, path, ds)

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
	startFactory(t, path, ds)

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

// readGroups fetches the modelgroups.Groups stored on modelName, if any. It looks
// the model up via GetModels rather than GetOrCreateModel so a name that was never
// registered by syncModels is reported as "not found" rather than silently created.
func readGroups(t *testing.T, ds datalayer.Datastore, modelName string) (modelgroups.Groups, bool) {
	t.Helper()
	matches := ds.GetModels(func(m datalayer.Model) bool { return m.GetName() == modelName })
	if len(matches) == 0 {
		return nil, false
	}
	v, ok := matches[0].GetAttributes().Get(modelgroups.GroupsAttributeKey)
	if !ok {
		return nil, false
	}
	g, ok := v.(modelgroups.Groups)
	if !ok {
		t.Fatalf("model %q: attribute %q is %T, want modelgroups.Groups", modelName, modelgroups.GroupsAttributeKey, v)
	}
	return g, true
}

// groupsEqual compares two Groups values order-insensitively. slices.Sort sorts
// in place, so each input is cloned first to avoid mutating the caller's slice.
func groupsEqual(a, b modelgroups.Groups) bool {
	if len(a) != len(b) {
		return false
	}
	sortedA := slices.Clone(a)
	sortedB := slices.Clone(b)
	slices.Sort(sortedA)
	slices.Sort(sortedB)
	return slices.Compare(sortedA, sortedB) == 0
}

// --- Group-configuration tests ---

// TestStart_GroupMembership verifies that the group-centric "groups" list in the
// config file ({name, models[]}) is inverted into a per-model modelgroups.Groups
// attribute on each referenced model, across a range of valid and invalid
// group configurations.
func TestStart_GroupMembership(t *testing.T) {
	tests := []struct {
		name       string
		models     []ModelConfiguration
		groups     []ModelGroupConfig
		want       map[string]modelgroups.Groups // absent/empty means "expect no groups attribute"
		wantModels []string                      // optional: asserts exactly these models are registered
	}{
		{
			name: "multiple groups partition models",
			models: []ModelConfiguration{
				{Name: "qwen3-8b"}, {Name: "gpt-oss-20b"}, {Name: "gpt-oss-120b"}, {Name: "gemma4"},
			},
			groups: []ModelGroupConfig{
				{Name: "fast", Models: []string{"qwen3-8b", "gpt-oss-20b"}},
				{Name: "planning", Models: []string{"gpt-oss-120b", "gemma4"}},
			},
			want: map[string]modelgroups.Groups{
				"qwen3-8b":     {"fast"},
				"gpt-oss-20b":  {"fast"},
				"gpt-oss-120b": {"planning"},
				"gemma4":       {"planning"},
			},
		},
		{
			name:   "model in multiple groups",
			models: []ModelConfiguration{{Name: "qwen3-32b"}},
			groups: []ModelGroupConfig{
				{Name: "qwen3models", Models: []string{"qwen3-32b"}},
				{Name: "large-models", Models: []string{"qwen3-32b"}},
			},
			want: map[string]modelgroups.Groups{
				"qwen3-32b": {"qwen3models", "large-models"},
			},
		},
		{
			name:   "group with empty models list is skipped, others still processed",
			models: []ModelConfiguration{{Name: "m1"}, {Name: "m2"}},
			groups: []ModelGroupConfig{
				{Name: "empty-models", Models: []string{}},
				{Name: "valid", Models: []string{"m2"}},
			},
			want: map[string]modelgroups.Groups{
				"m1": nil,
				"m2": {"valid"},
			},
		},
		{
			name:   "empty model name within a group is skipped, others still processed",
			models: []ModelConfiguration{{Name: "m1"}, {Name: "m2"}},
			groups: []ModelGroupConfig{
				{Name: "mixed", Models: []string{"", "m2"}},
			},
			want: map[string]modelgroups.Groups{
				"m1": nil,
				"m2": {"mixed"},
			},
		},
		{
			name:   "group with empty name is skipped",
			models: []ModelConfiguration{{Name: "m1"}, {Name: "m2"}},
			groups: []ModelGroupConfig{
				{Name: "", Models: []string{"m1"}},
				{Name: "valid", Models: []string{"m2"}},
			},
			want: map[string]modelgroups.Groups{
				"m1": nil,
				"m2": {"valid"},
			},
		},
		{
			name:   "group referencing unknown model is skipped",
			models: []ModelConfiguration{{Name: "known"}},
			groups: []ModelGroupConfig{
				{Name: "g1", Models: []string{"unknown-model"}},
			},
			want:       map[string]modelgroups.Groups{"known": nil},
			wantModels: []string{"known"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ds := datastore.NewFakeDataStore()
			path := writeTempModelsConfig(t, ModelsConfig{Models: tc.models, Groups: tc.groups})
			startFactory(t, path, ds)

			for modelName, want := range tc.want {
				got, ok := readGroups(t, ds, modelName)
				if len(want) == 0 {
					if ok {
						t.Errorf("model %q: expected no groups attribute, got %v", modelName, got)
					}
					continue
				}
				if !ok {
					t.Errorf("model %q: expected groups attribute to be set", modelName)
					continue
				}
				if !groupsEqual(got, want) {
					t.Errorf("model %q: groups = %v, want %v", modelName, got, want)
				}
			}

			if tc.wantModels != nil {
				got := ds.Models()
				if !groupsEqual(modelgroups.Groups(got), modelgroups.Groups(tc.wantModels)) {
					t.Errorf("expected registered models %v, got %v", tc.wantModels, got)
				}
			}
		})
	}
}

// TestSyncModels_FileChange_GroupMembershipRemoved verifies that when a model's
// group membership is dropped from the config file, a second syncModels call
// clears its stale modelgroups.Groups attribute rather than leaving it in place.
// Calls syncModels directly (no Start/watcher) since this is a pure re-sync
// outcome, not a test of the watcher wiring.
func TestSyncModels_FileChange_GroupMembershipRemoved(t *testing.T) {
	ds := datastore.NewFakeDataStore()
	path := writeTempModelsConfig(t, ModelsConfig{
		Models: []ModelConfiguration{{Name: "m1"}},
		Groups: []ModelGroupConfig{{Name: "g1", Models: []string{"m1"}}},
	})
	c := useFactory(t, path, ds)
	if err := c.syncModels(context.Background(), readFile(t, path)); err != nil {
		t.Fatalf("syncModels: %v", err)
	}
	if _, ok := readGroups(t, ds, "m1"); !ok {
		t.Fatal("expected m1 to have groups attribute set before file change")
	}

	overwriteFile(t, path, ModelsConfig{
		Models: []ModelConfiguration{{Name: "m1"}},
	})
	if err := c.syncModels(context.Background(), readFile(t, path)); err != nil {
		t.Fatalf("syncModels: %v", err)
	}

	if _, ok := readGroups(t, ds, "m1"); ok {
		t.Error("expected m1's groups attribute to be cleared after group removed from config")
	}
}

// TestSyncModels_EmptyData_ClearsModels verifies that calling syncModels with nil/empty
// data (as happens when the config file is missing, deleted, or renamed away) clears
// every model already in the datastore, treating the absence of content as an empty
// config rather than an error.
func TestSyncModels_EmptyData_ClearsModels(t *testing.T) {
	ds := datastore.NewFakeDataStore()
	path := writeTempModelsConfig(t, ModelsConfig{
		Models: []ModelConfiguration{{Name: "m1"}, {Name: "m2"}},
	})
	c := useFactory(t, path, ds)
	if err := c.syncModels(context.Background(), readFile(t, path)); err != nil {
		t.Fatalf("syncModels: %v", err)
	}
	if models := ds.GetModels(datalayer.AllModelsPredicate); len(models) != 2 {
		t.Fatalf("expected 2 models before clearing, got %d: %v", len(models), models)
	}

	if err := c.syncModels(context.Background(), nil); err != nil {
		t.Fatalf("syncModels with nil data: %v", err)
	}

	models := ds.GetModels(datalayer.AllModelsPredicate)
	if len(models) != 0 {
		t.Errorf("expected 0 models after syncModels with nil data, got %d: %v", len(models), models)
	}
}
