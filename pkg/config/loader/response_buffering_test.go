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

package loader

import (
	"context"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/log"

	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
)

type fakeResponsePlugin struct {
	name string
	mode *requesthandling.BodyProcessingMode
}

func (p *fakeResponsePlugin) TypedName() plugin.TypedName {
	return plugin.TypedName{Type: "fake", Name: p.name}
}

func (p *fakeResponsePlugin) ProcessResponse(_ context.Context, _ *plugin.CycleState, _ *requesthandling.InferenceResponse) error {
	return nil
}

func (p *fakeResponsePlugin) BodyProcessingMode() requesthandling.BodyProcessingMode {
	if p.mode != nil {
		return *p.mode
	}
	return requesthandling.Full
}

var (
	_ requesthandling.ResponseProcessor      = &fakeResponsePlugin{}
	_ requesthandling.ResponseBodyRequirement = &fakeResponsePlugin{}
)

// fakeChunkPlugin declares Chunks and implements ChunkProcessor.
type fakeChunkPlugin struct {
	fakeResponsePlugin
}

func (p *fakeChunkPlugin) ProcessResponseChunk(_ context.Context, _ *plugin.CycleState, chunk []byte, _ bool) ([]byte, error) {
	return chunk, nil
}

var _ requesthandling.ChunkProcessor = &fakeChunkPlugin{}

// badChunkedPlugin declares Chunks but does NOT implement ChunkProcessor.
type badChunkedPlugin struct {
	fakeResponsePlugin
}

type legacyResponsePlugin struct {
	name string
}

func (p *legacyResponsePlugin) TypedName() plugin.TypedName {
	return plugin.TypedName{Type: "legacy", Name: p.name}
}

func (p *legacyResponsePlugin) ProcessResponse(_ context.Context, _ *plugin.CycleState, _ *requesthandling.InferenceResponse) error {
	return nil
}

var _ requesthandling.ResponseProcessor = &legacyResponsePlugin{}

func modePtr(m requesthandling.BodyProcessingMode) *requesthandling.BodyProcessingMode { return &m }

func TestComputeResponseBuffering(t *testing.T) {
	logger := log.FromContext(logutil.NewTestLoggerIntoContext(context.Background()))

	chunkedMode := modePtr(requesthandling.Chunks)

	tests := []struct {
		name              string
		plugins           []requesthandling.ResponseProcessor
		wantBuffering     bool
		wantChunkCount    int
	}{
		{
			name:          "no response plugins",
			plugins:       nil,
			wantBuffering: false,
		},
		{
			name: "all Skip",
			plugins: []requesthandling.ResponseProcessor{
				&fakeResponsePlugin{name: "a", mode: modePtr(requesthandling.Skip)},
				&fakeResponsePlugin{name: "b", mode: modePtr(requesthandling.Skip)},
			},
			wantBuffering: false,
		},
		{
			name: "Chunks with ChunkProcessor",
			plugins: []requesthandling.ResponseProcessor{
				&fakeChunkPlugin{fakeResponsePlugin{name: "chunker", mode: chunkedMode}},
			},
			wantBuffering:  false,
			wantChunkCount: 1,
		},
		{
			name: "one Full forces buffering",
			plugins: []requesthandling.ResponseProcessor{
				&fakeResponsePlugin{name: "a", mode: modePtr(requesthandling.Skip)},
				&fakeResponsePlugin{name: "b", mode: modePtr(requesthandling.Full)},
			},
			wantBuffering: true,
		},
		{
			name: "legacy plugin without ResponseBodyRequirement defaults to Full",
			plugins: []requesthandling.ResponseProcessor{
				&legacyResponsePlugin{name: "old-plugin"},
			},
			wantBuffering: true,
		},
		{
			name: "mixed: Chunks + legacy forces buffering",
			plugins: []requesthandling.ResponseProcessor{
				&fakeChunkPlugin{fakeResponsePlugin{name: "a", mode: chunkedMode}},
				&legacyResponsePlugin{name: "legacy"},
			},
			wantBuffering:  true,
			wantChunkCount: 1,
		},
		{
			name: "mixed: Skip + Chunks — no buffering",
			plugins: []requesthandling.ResponseProcessor{
				&fakeResponsePlugin{name: "a", mode: modePtr(requesthandling.Skip)},
				&fakeChunkPlugin{fakeResponsePlugin{name: "b", mode: chunkedMode}},
			},
			wantBuffering:  false,
			wantChunkCount: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			profiles := map[string]*requesthandling.Profile{
				"test": {
					ResponsePlugins: tc.plugins,
				},
			}
			if err := computeResponseBuffering(profiles, logger); err != nil {
				t.Fatalf("computeResponseBuffering() unexpected error: %v", err)
			}
			if profiles["test"].NeedsResponseBuffering != tc.wantBuffering {
				t.Errorf("NeedsResponseBuffering = %v, want %v", profiles["test"].NeedsResponseBuffering, tc.wantBuffering)
			}
			if got := len(profiles["test"].ChunkProcessors); got != tc.wantChunkCount {
				t.Errorf("ChunkProcessors count = %d, want %d", got, tc.wantChunkCount)
			}
		})
	}
}

func TestComputeResponseBuffering_ChunksWithoutChunkProcessor(t *testing.T) {
	logger := log.FromContext(logutil.NewTestLoggerIntoContext(context.Background()))

	profiles := map[string]*requesthandling.Profile{
		"test": {
			ResponsePlugins: []requesthandling.ResponseProcessor{
				&badChunkedPlugin{fakeResponsePlugin{name: "bad", mode: modePtr(requesthandling.Chunks)}},
			},
		},
	}

	err := computeResponseBuffering(profiles, logger)
	if err == nil {
		t.Fatal("expected error for Chunks plugin without ChunkProcessor")
	}
	if !strings.Contains(err.Error(), "does not implement ChunkProcessor") {
		t.Errorf("error message should mention ChunkProcessor, got: %v", err)
	}
}

func TestComputeResponseBuffering_MultipleProfiles(t *testing.T) {
	logger := log.FromContext(logutil.NewTestLoggerIntoContext(context.Background()))

	chunkedMode := modePtr(requesthandling.Chunks)

	profiles := map[string]*requesthandling.Profile{
		"streaming": {
			ResponsePlugins: []requesthandling.ResponseProcessor{
				&fakeResponsePlugin{name: "headers-only", mode: modePtr(requesthandling.Skip)},
			},
		},
		"chunked": {
			ResponsePlugins: []requesthandling.ResponseProcessor{
				&fakeChunkPlugin{fakeResponsePlugin{name: "meter", mode: chunkedMode}},
			},
		},
		"full-body": {
			ResponsePlugins: []requesthandling.ResponseProcessor{
				&fakeResponsePlugin{name: "translator", mode: modePtr(requesthandling.Full)},
			},
		},
	}

	if err := computeResponseBuffering(profiles, logger); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if profiles["streaming"].NeedsResponseBuffering {
		t.Error("streaming profile should not need buffering")
	}
	if profiles["chunked"].NeedsResponseBuffering {
		t.Error("chunked profile should not need buffering")
	}
	if len(profiles["chunked"].ChunkProcessors) != 1 {
		t.Errorf("chunked profile should have 1 ChunkProcessor, got %d", len(profiles["chunked"].ChunkProcessors))
	}
	if !profiles["full-body"].NeedsResponseBuffering {
		t.Error("full-body profile should need buffering")
	}
}
