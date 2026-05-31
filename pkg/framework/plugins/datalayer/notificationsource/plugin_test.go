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

package notificationsource

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	dlsrc "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer/datasource"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	ctrlbuilder "sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// fakeHandle implements plugin.Handle for unit tests.
type fakeHandle struct {
	plugins map[string]plugin.Plugin
}

func newFakeHandle(plugins map[string]plugin.Plugin) *fakeHandle {
	if plugins == nil {
		plugins = map[string]plugin.Plugin{}
	}
	return &fakeHandle{plugins: plugins}
}

func (f *fakeHandle) Context() context.Context                         { return context.Background() }
func (f *fakeHandle) Client() client.Client                            { return nil }
func (f *fakeHandle) ReconcilerBuilder() *ctrlbuilder.Builder          { return nil }
func (f *fakeHandle) Datastore() datalayer.Datastore                   { return nil }
func (f *fakeHandle) Plugin(name string) plugin.Plugin                 { return f.plugins[name] }
func (f *fakeHandle) AddPlugin(name string, p plugin.Plugin)           { f.plugins[name] = p }
func (f *fakeHandle) GetAllPlugins() []plugin.Plugin                   { return nil }
func (f *fakeHandle) GetAllPluginsWithNames() map[string]plugin.Plugin { return f.plugins }

// fakeExtractor is a minimal dlsrc.Extractor for testing.
type fakeExtractor struct{ name string }

func (e *fakeExtractor) TypedName() plugin.TypedName {
	return plugin.TypedName{Type: "fake-extractor", Name: e.name}
}
func (e *fakeExtractor) Extract(_ context.Context, _ []dlsrc.Event) error { return nil }

// notAnExtractor satisfies plugin.Plugin but not dlsrc.Extractor.
type notAnExtractor struct{}

func (n *notAnExtractor) TypedName() plugin.TypedName {
	return plugin.TypedName{Type: "not-an-extractor", Name: "not-an-extractor"}
}

// Factory with nil parameters returns a notificationSource with no extractors.
func TestFactoryNoParameters(t *testing.T) {
	h := newFakeHandle(nil)

	p, err := Factory("my-source", nil, h)
	if err != nil {
		t.Fatalf("Factory returned unexpected error: %v", err)
	}
	src, ok := p.(*notificationSource)
	if !ok {
		t.Fatalf("expected *notificationSource, got %T", p)
	}
	if src.TypedName().Name != "my-source" {
		t.Errorf("expected name %q, got %q", "my-source", src.TypedName().Name)
	}
	if len(src.extractors) != 0 {
		t.Errorf("expected 0 extractors, got %d", len(src.extractors))
	}
}

// Factory with an empty JSON object returns a notificationSource with no extractors.
func TestFactoryEmptyParameters(t *testing.T) {
	h := newFakeHandle(nil)

	p, err := Factory("src", json.RawMessage(`{}`), h)
	if err != nil {
		t.Fatalf("Factory returned unexpected error: %v", err)
	}
	src := p.(*notificationSource)
	if len(src.extractors) != 0 {
		t.Errorf("expected 0 extractors, got %d", len(src.extractors))
	}
}

// Factory resolves pluginRefs to Extractor instances and registers them in order.
func TestFactoryWithExtractors(t *testing.T) {
	ext1 := &fakeExtractor{name: "ext-a"}
	ext2 := &fakeExtractor{name: "ext-b"}
	h := newFakeHandle(map[string]plugin.Plugin{
		"ext-a": ext1,
		"ext-b": ext2,
	})

	params := json.RawMessage(`{"extractors":[{"pluginRef":"ext-a"},{"pluginRef":"ext-b"}]}`)
	p, err := Factory("src", params, h)
	if err != nil {
		t.Fatalf("Factory returned unexpected error: %v", err)
	}
	src := p.(*notificationSource)
	if len(src.extractors) != 2 {
		t.Fatalf("expected 2 extractors, got %d", len(src.extractors))
	}
	if src.extractors[0] != ext1 {
		t.Errorf("expected extractors[0] to be ext1")
	}
	if src.extractors[1] != ext2 {
		t.Errorf("expected extractors[1] to be ext2")
	}
}

// Factory returns an error when the parameters JSON is malformed.
func TestFactoryInvalidJSON(t *testing.T) {
	h := newFakeHandle(nil)

	_, err := Factory("src", json.RawMessage(`{invalid`), h)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

// Factory returns an error when a pluginRef names a plugin that is not registered.
func TestFactoryUnknownExtractorRef(t *testing.T) {
	h := newFakeHandle(nil)

	params := json.RawMessage(`{"extractors":[{"pluginRef":"does-not-exist"}]}`)
	_, err := Factory("src", params, h)
	if err == nil {
		t.Fatal("expected error for unknown extractor ref, got nil")
	}
}

// Factory returns an error when a referenced plugin does not implement the Extractor interface.
func TestFactoryPluginNotExtractor(t *testing.T) {
	h := newFakeHandle(map[string]plugin.Plugin{
		"wrong-type": &notAnExtractor{},
	})

	params := json.RawMessage(`{"extractors":[{"pluginRef":"wrong-type"}]}`)
	_, err := Factory("src", params, h)
	if err == nil {
		t.Fatal("expected error when plugin does not implement Extractor, got nil")
	}
}
