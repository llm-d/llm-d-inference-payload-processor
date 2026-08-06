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

package datalayer

// EventType identifies the kind of runtime event.
type EventType string

const (
	RequestEventType  EventType = "request"
	ResponseEventType EventType = "response"
)

var AllModelsPredicate = func(Model) bool { return true }

// Event is the carrier for all data layer events.
type Event struct {
	Type    EventType
	Payload any
}

// EventNotifier is the narrow interface plugins use to fire datalayer events.
// Defined here (not in datasource) to avoid import cycles with the plugin package.
type EventNotifier interface {
	Notify(e Event)
}

// Datastore is the interface for reading and updating the model store.
type Datastore interface {
	GetOrCreateModel(name string) Model
	DeleteModel(name string)
	// GetModels returns all models matching predicate in a single call.
	// Pass a predicate that always returns true to retrieve all models.
	GetModels(predicate func(Model) bool) []Model
}
