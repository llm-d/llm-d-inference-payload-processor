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
	"context"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
)

type ProfilePicker interface {
	plugin.Plugin

	// Pick selects the Profile to run from a list of candidate profiles, while taking into consideration the request properties.
	Pick(ctx context.Context, cycleState *plugin.CycleState, request *InferenceRequest, profiles map[string]*Profile) (*Profile, error)
}

type RequestProcessor interface {
	plugin.Plugin
	// ProcessRequest runs the RequestProcessor plugin.
	// RequestProcessor can mutate the headers and/or the body of the request.
	ProcessRequest(ctx context.Context, cycleState *plugin.CycleState, request *InferenceRequest) error
}

// ResponseProcessor processes the complete buffered response body.
// If any plugin in a profile implements this interface, the framework buffers
// the entire response before calling ProcessResponse on each such plugin.
type ResponseProcessor interface {
	plugin.Plugin
	ProcessResponse(ctx context.Context, cycleState *plugin.CycleState, response *InferenceResponse) error
}

// ResponseChunkProcessor processes individual response body chunks as they
// stream through without buffering. The framework converts the raw chunk bytes
// to a string once and passes it to all chunk processors. Plugins receive the
// InferenceResponse to allow header mutation.
type ResponseChunkProcessor interface {
	plugin.Plugin
	ProcessResponseChunk(ctx context.Context, cycleState *plugin.CycleState, response *InferenceResponse, chunk string, isFinal bool) error
}
