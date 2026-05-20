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

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework"
	dlsrc "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/datalayer/datasource"
)

const (
	PluginType = "notification-source"

	defaultBufferSize = 10000
)

// compile-time interface assertion
var _ dlsrc.NotificationSource = &notificationSource{}

type notificationSource struct {
	name       framework.TypedName
	ch         chan dlsrc.Event
	extractors []dlsrc.Extractor

	started atomic.Bool
	cancel  context.CancelFunc
	done    chan struct{}
}

// Factory is the factory function for NotificationSource.
func Factory(name string, _ json.RawMessage, _ framework.Handle) (framework.Plugin, error) {
	src, err := New(name)
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
		name:       framework.TypedName{Type: PluginType, Name: name},
		ch:         make(chan dlsrc.Event, defaultBufferSize),
		extractors: extractors,
		done:       make(chan struct{}),
	}, nil
}

func (n *notificationSource) TypedName() framework.TypedName { return n.name }

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
