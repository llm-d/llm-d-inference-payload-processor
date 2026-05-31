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
	"errors"
	"fmt"
	"sync/atomic"

	"sigs.k8s.io/controller-runtime/pkg/log"

	v1alpha1 "github.com/llm-d/llm-d-inference-payload-processor/apix/config/v1alpha1"
	dlsrc "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer/datasource"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
)

const (
	PluginType = "notification-source"

	defaultBufferSize = 10000
)

// compile-time interface assertion
var _ dlsrc.NotificationSource = &notificationSource{}

type notificationSource struct {
	name       plugin.TypedName
	ch         chan dlsrc.Event
	extractors []dlsrc.Extractor

	started atomic.Bool
	cancel  context.CancelFunc
	done    chan struct{}
}

// NotificationSourceConfig is the JSON configuration structure for the NotificationSource plugin.
type NotificationSourceConfig struct {
	// Extractors is an ordered list of extractor plugins to register on this source,
	// each referenced by the name of a plugin entry in the configuration's Plugins section.
	Extractors []v1alpha1.PluginRef `json:"extractors"`
}

// Factory is the factory function for NotificationSource.
func Factory(name string, rawParameters json.RawMessage, handle plugin.Handle) (plugin.Plugin, error) {
	var config NotificationSourceConfig
	if len(rawParameters) > 0 {
		if err := json.Unmarshal(rawParameters, &config); err != nil {
			return nil, fmt.Errorf("failed to parse the parameters of the '%s' plugin - %w", PluginType, err)
		}
	}

	extractors := make([]dlsrc.Extractor, 0, len(config.Extractors))
	for _, ref := range config.Extractors {
		p := handle.Plugin(ref.PluginRef)
		if p == nil {
			return nil, fmt.Errorf("'%s' plugin: extractor plugin %q not found", PluginType, ref.PluginRef)
		}
		ext, ok := p.(dlsrc.Extractor)
		if !ok {
			return nil, fmt.Errorf("'%s' plugin: plugin %q does not implement Extractor", PluginType, ref.PluginRef)
		}
		extractors = append(extractors, ext)
	}

	src, err := New(name, extractors...)
	if err != nil {
		return nil, fmt.Errorf("failed to create '%s' plugin - %w", PluginType, err)
	}
	return src, nil
}

// New creates a NotificationSource that delivers each event to the given extractors as it arrives.
func New(name string, extractors ...dlsrc.Extractor) (dlsrc.NotificationSource, error) {
	if name == "" {
		return nil, fmt.Errorf("name is required for plugin '%s'", PluginType)
	}
	return &notificationSource{
		name:       plugin.TypedName{Type: PluginType, Name: name},
		ch:         make(chan dlsrc.Event, defaultBufferSize),
		extractors: extractors,
		done:       make(chan struct{}),
	}, nil
}

func (n *notificationSource) TypedName() plugin.TypedName { return n.name }

func (n *notificationSource) RegisterExtractor(e dlsrc.Extractor) {
	n.extractors = append(n.extractors, e)
}

// Notify fires an event non-blocking; drops silently if the buffer is full.
func (n *notificationSource) Notify(e dlsrc.Event) {
	select {
	case n.ch <- e:
	default:
	}
}

// Start launches the event loop. Returns an error if called more than once.
func (n *notificationSource) Start(ctx context.Context) error {
	if !n.started.CompareAndSwap(false, true) {
		return errors.New("NotificationSource already started")
	}
	ctx, n.cancel = context.WithCancel(ctx)
	ready := make(chan struct{})
	go n.eventLoop(ctx, ready)
	<-ready
	return nil
}

// Stop cancels the event loop and waits for it to exit.
func (n *notificationSource) Stop() {
	if n.cancel != nil {
		n.cancel()
		<-n.done
	}
}

func (n *notificationSource) eventLoop(ctx context.Context, ready chan struct{}) {
	close(ready)

	logger := log.FromContext(ctx).WithName("notification-source")

	for {
		select {
		case <-ctx.Done():
			close(n.done)
			return
		case e := <-n.ch:
			// Extractors are called sequentially; current extractors are in-memory only.
			// Switch to a WaitGroup if any extractor performs I/O.
			for _, ext := range n.extractors {
				if err := ext.Extract(ctx, []dlsrc.Event{e}); err != nil {
					logger.Error(err, "extractor error", "extractor", ext.TypedName())
				}
			}
		}
	}
}
