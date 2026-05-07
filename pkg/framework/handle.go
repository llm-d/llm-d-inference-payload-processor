/*
Copyright 2025 The Kubernetes Authors.

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

package framework

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/datastore"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlbuilder "sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Handle provides plugins a set of standard data and tools to work with
type Handle interface {
	// Context returns a context the plugins can use, if they need one
	Context() context.Context
	Client() client.Client
	ReconcilerBuilder() *ctrlbuilder.Builder
	// DatastoreSnapshot creates a snapshot of the datastore topic and stores it in CycleState
	DatastoreSnapshot(datastoreTopic string, state *CycleState) (datastore.AttributeMap, error)
}

// datastoreSnapshot holds a cached snapshot with its creation timestamp
type datastoreSnapshot struct {
	snapshot  datastore.AttributeMap
	timestamp time.Time
}

// payloadProcessorHandle is an implementation of the Handle interface.
type payloadProcessorHandle struct {
	ctx               context.Context
	mgr               ctrl.Manager
	datastores        *datastore.Datastores
	snapshotCache     sync.Map
	snapshotLifecycle time.Duration
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

func NewHandle(ctx context.Context, mgr ctrl.Manager, datastores *datastore.Datastores) Handle {
	return &payloadProcessorHandle{
		ctx:               ctx,
		mgr:               mgr,
		datastores:        datastores,
		snapshotLifecycle: 100 * time.Millisecond,
	}
}

// DatastoreSnapshot creates a snapshot of the datastore topic and stores it in CycleState.
// It uses a Handle-level cache with 100ms lifecycle to optimize performance for concurrent requests.
// Returns the snapshot stored in CycleState for the current request.
func (h *payloadProcessorHandle) DatastoreSnapshot(datastoreTopic string, state *CycleState) (datastore.AttributeMap, error) {
	if datastoreTopic == "" {
		return nil, errors.New("datastoreTopic cannot be empty")
	}
	if state == nil {
		return nil, errors.New("state cannot be nil")
	}

	// Check Handle cache first
	now := time.Now()
	if cached, ok := h.snapshotCache.Load(datastoreTopic); ok {
		if snapshot, ok := cached.(*datastoreSnapshot); ok {
			// Validate expiration (100ms lifecycle)
			if now.Sub(snapshot.timestamp) < h.snapshotLifecycle {
				// Clone from Handle cache to CycleState
				cycleSnapshot := snapshot.snapshot.Clone()
				state.Write(datastoreTopic, cycleSnapshot)
				return cycleSnapshot, nil
			}
		}
	}

	// Cache miss or expired - lookup and clone datastore topic
	topicMap, err := h.datastores.GetOrCreateStore(datastoreTopic)
	if err != nil {
		return nil, fmt.Errorf("failed to get datastore topic %q: %w", datastoreTopic, err)
	}

	handleSnapshot := topicMap.Clone()

	// Store in Handle cache with timestamp
	h.snapshotCache.Store(datastoreTopic, &datastoreSnapshot{
		snapshot:  handleSnapshot,
		timestamp: now,
	})

	// Copy to CycleState for request isolation
	cycleSnapshot := handleSnapshot.Clone()
	state.Write(datastoreTopic, cycleSnapshot)

	return cycleSnapshot, nil
}
