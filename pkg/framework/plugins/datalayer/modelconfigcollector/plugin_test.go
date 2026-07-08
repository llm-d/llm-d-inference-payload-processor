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
