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

package requesthandling

import (
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
)

func newInferenceMessage() InferenceMessage {
	return InferenceMessage{
		Headers:        map[string]string{},
		Body:           make(map[string]any),
		mutatedHeaders: make(map[string]string),
		removedHeaders: sets.New[string](),
	}
}

type InferenceMessage struct {
	// original request
	Headers map[string]string
	Body    map[string]any

	// mutations
	mutatedHeaders map[string]string
	removedHeaders sets.Set[string]
	bodyMutated    bool
}

func (r *InferenceMessage) SetHeader(key string, value string) {
	if old, ok := r.Headers[key]; !ok || old != value { // if we add or replace a header
		r.Headers[key] = value
		r.mutatedHeaders[key] = value
		r.removedHeaders.Delete(key) // no longer removed if we set it again
	}
}

func (r *InferenceMessage) RemoveHeader(key string) {
	if _, ok := r.Headers[key]; ok {
		delete(r.Headers, key)
		delete(r.mutatedHeaders, key) // avoid sending set and remove for same key
		r.removedHeaders.Insert(key)
	}
}

func (r *InferenceMessage) MutatedHeaders() map[string]string {
	return r.mutatedHeaders
}

func (r *InferenceMessage) RemovedHeaders() []string {
	return r.removedHeaders.UnsortedList()
}

func (r *InferenceMessage) SetBody(body map[string]any) {
	r.Body = body
	r.bodyMutated = true
}

func (r *InferenceMessage) SetBodyField(key string, value any) {
	r.Body[key] = value
	r.bodyMutated = true
}

func (r *InferenceMessage) RemoveBodyField(key string) {
	if _, ok := r.Body[key]; ok {
		delete(r.Body, key)
		r.bodyMutated = true
	}
}

func (r *InferenceMessage) BodyMutated() bool {
	return r.bodyMutated
}

type InferenceRequest struct {
	InferenceMessage
}

type InferenceResponse struct {
	InferenceMessage

	// CurrentChunk holds the current response body chunk during streaming.
	// Set by the framework before calling ResponseChunkProcessor plugins.
	// Plugins can read or mutate this field; the framework uses the final
	// value when building the ext_proc response.
	CurrentChunk string
	chunkMutated bool
}

// SetChunk sets the current chunk content and marks it as mutated.
func (r *InferenceResponse) SetChunk(chunk string) {
	r.CurrentChunk = chunk
	r.chunkMutated = true
}

// ChunkMutated reports whether any plugin modified the chunk via SetChunk.
func (r *InferenceResponse) ChunkMutated() bool {
	return r.chunkMutated
}

// ResetChunkState prepares the response for a new chunk processing cycle.
func (r *InferenceResponse) ResetChunkState(chunk string) {
	r.CurrentChunk = chunk
	r.chunkMutated = false
}

// NewInferenceRequest returns a new request with initialized Headers, Body, and mutatedHeaders.
func NewInferenceRequest() *InferenceRequest {
	return &InferenceRequest{
		InferenceMessage: newInferenceMessage(),
	}
}

// NewInferenceResponse returns a new response with initialized Headers, Body, and mutatedHeaders.
func NewInferenceResponse() *InferenceResponse {
	return &InferenceResponse{
		InferenceMessage: newInferenceMessage(),
	}
}

func NewProfile() *Profile {
	return &Profile{}
}

// Profile specifies a pipeline profile, a named set of request and response plugins
type Profile struct {
	// RequestPlugins are the request processing plugin instances executed by the request handler,
	// in the same order provided in the configuration file.
	RequestPlugins []RequestProcessor
	// ResponsePlugins process the complete buffered response body.
	ResponsePlugins []ResponseProcessor
	// ResponseChunkProcessors process individual response chunks without buffering.
	ResponseChunkProcessors []ResponseChunkProcessor
	// NeedsResponseBuffering is true when any ResponsePlugin is present.
	// The framework uses this to decide whether to buffer the full response body
	// or stream chunks through ResponseChunkProcessors.
	NeedsResponseBuffering bool
	// ModelSelectorPlugins are the Filter, Scorer (including WeightedScorer), and Picker plugin
	// instances to be wired into any model-selector plugin present in RequestPlugins.
	ModelSelectorPlugins []plugin.Plugin
}
