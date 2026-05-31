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
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
	ctrlbuilder "sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
)

// Handle provides plugins a set of standard data and tools to work with
type Handle interface {
	// Context returns a context the plugins can use, if they need one
	Context() context.Context
	Client() client.Client
	ReconcilerBuilder() *ctrlbuilder.Builder
	Datastore() datalayer.Datastore
	HandlePlugins
}

// HandlePlugins defines a set of APIs to work with instantiated plugins
type HandlePlugins interface {
	// Plugin returns the named plugin instance
	Plugin(name string) Plugin

	// AddPlugin adds a plugin to the set of known plugin instances
	AddPlugin(name string, plugin Plugin)

	// GetAllPlugins returns all of the known plugins
	GetAllPlugins() []Plugin

	// GetAllPluginsWithNames returns all of the known plugins with their names
	GetAllPluginsWithNames() map[string]Plugin
}

// payloadProcessorHandle is an implementation of the Handle interface.
type payloadProcessorHandle struct {
	ctx context.Context
	mgr ctrl.Manager
	ds  datalayer.Datastore
	HandlePlugins
}

// Context returns a context the plugins can use, if they need one
func (h *payloadProcessorHandle) Context() context.Context {
	return h.ctx
}

func (h *payloadProcessorHandle) Client() client.Client {
	return h.mgr.GetClient()
}

func (h *payloadProcessorHandle) ReconcilerBuilder() *ctrlbuilder.Builder {
	return ctrl.NewControllerManagedBy(h.mgr)
}

func (h *payloadProcessorHandle) Datastore() datalayer.Datastore {
	return h.ds
}

// ippHandlePlugins implements the set of APIs to work with instantiated plugins
type ippHandlePlugins struct {
	plugins map[string]Plugin
}

// Plugin returns the named plugin instance
func (h *ippHandlePlugins) Plugin(name string) Plugin {
	return h.plugins[name]
}

// AddPlugin adds a plugin to the set of known plugin instances
func (h *ippHandlePlugins) AddPlugin(name string, plugin Plugin) {
	h.plugins[name] = plugin
}

// GetAllPlugins returns all of the known plugins
func (h *ippHandlePlugins) GetAllPlugins() []Plugin {
	result := make([]Plugin, 0, len(h.plugins))
	for _, plugin := range h.plugins {
		result = append(result, plugin)
	}
	return result
}

// GetAllPluginsWithNames returns al of the known plugins with their names
func (h *ippHandlePlugins) GetAllPluginsWithNames() map[string]Plugin {
	return h.plugins
}

func NewHandle(ctx context.Context, mgr ctrl.Manager, ds datalayer.Datastore) Handle {
	return &payloadProcessorHandle{
		ctx: ctx,
		mgr: mgr,
		ds:  ds,
		HandlePlugins: &ippHandlePlugins{
			plugins: map[string]Plugin{},
		},
	}
}
