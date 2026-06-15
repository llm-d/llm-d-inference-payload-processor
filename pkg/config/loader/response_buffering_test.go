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
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/log"

	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
)

type fakeResponsePlugin struct {
	name string
	mode *requesthandling.ResponseBodyMode
}

func (p *fakeResponsePlugin) TypedName() plugin.TypedName {
	return plugin.TypedName{Type: "fake", Name: p.name}
}

func (p *fakeResponsePlugin) ProcessResponse(_ context.Context, _ *plugin.CycleState, _ *requesthandling.InferenceResponse) error {
	return nil
}

func (p *fakeResponsePlugin) ResponseBodyMode() requesthandling.ResponseBodyMode {
	if p.mode != nil {
		return *p.mode
	}
	return requesthandling.BodyFull
}

var (
	_ requesthandling.ResponseProcessor      = &fakeResponsePlugin{}
	_ requesthandling.ResponseBodyRequirement = &fakeResponsePlugin{}
)

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

func modePtr(m requesthandling.ResponseBodyMode) *requesthandling.ResponseBodyMode { return &m }

func TestComputeResponseBuffering(t *testing.T) {
	logger := log.FromContext(logutil.NewTestLoggerIntoContext(context.Background()))

	tests := []struct {
		name           string
		plugins        []requesthandling.ResponseProcessor
		wantBuffering  bool
	}{
		{
			name:          "no response plugins",
			plugins:       nil,
			wantBuffering: false,
		},
		{
			name: "all BodyNotNeeded",
			plugins: []requesthandling.ResponseProcessor{
				&fakeResponsePlugin{name: "a", mode: modePtr(requesthandling.BodyNotNeeded)},
				&fakeResponsePlugin{name: "b", mode: modePtr(requesthandling.BodyNotNeeded)},
			},
			wantBuffering: false,
		},
		{
			name: "all BodyChunked",
			plugins: []requesthandling.ResponseProcessor{
				&fakeResponsePlugin{name: "a", mode: modePtr(requesthandling.BodyChunked)},
			},
			wantBuffering: false,
		},
		{
			name: "one BodyFull forces buffering",
			plugins: []requesthandling.ResponseProcessor{
				&fakeResponsePlugin{name: "a", mode: modePtr(requesthandling.BodyNotNeeded)},
				&fakeResponsePlugin{name: "b", mode: modePtr(requesthandling.BodyFull)},
			},
			wantBuffering: true,
		},
		{
			name: "legacy plugin without ResponseBodyRequirement defaults to BodyFull",
			plugins: []requesthandling.ResponseProcessor{
				&legacyResponsePlugin{name: "old-plugin"},
			},
			wantBuffering: true,
		},
		{
			name: "mixed: BodyChunked + legacy forces buffering",
			plugins: []requesthandling.ResponseProcessor{
				&fakeResponsePlugin{name: "a", mode: modePtr(requesthandling.BodyChunked)},
				&legacyResponsePlugin{name: "legacy"},
			},
			wantBuffering: true,
		},
		{
			name: "mixed: BodyNotNeeded + BodyChunked — no buffering",
			plugins: []requesthandling.ResponseProcessor{
				&fakeResponsePlugin{name: "a", mode: modePtr(requesthandling.BodyNotNeeded)},
				&fakeResponsePlugin{name: "b", mode: modePtr(requesthandling.BodyChunked)},
			},
			wantBuffering: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			profiles := map[string]*requesthandling.Profile{
				"test": {
					ResponsePlugins: tc.plugins,
				},
			}
			computeResponseBuffering(profiles, logger)
			if profiles["test"].NeedsResponseBuffering != tc.wantBuffering {
				t.Errorf("NeedsResponseBuffering = %v, want %v", profiles["test"].NeedsResponseBuffering, tc.wantBuffering)
			}
		})
	}
}

func TestComputeResponseBuffering_MultipleProfiles(t *testing.T) {
	logger := log.FromContext(logutil.NewTestLoggerIntoContext(context.Background()))

	profiles := map[string]*requesthandling.Profile{
		"streaming": {
			ResponsePlugins: []requesthandling.ResponseProcessor{
				&fakeResponsePlugin{name: "headers-only", mode: modePtr(requesthandling.BodyNotNeeded)},
			},
		},
		"full-body": {
			ResponsePlugins: []requesthandling.ResponseProcessor{
				&fakeResponsePlugin{name: "translator", mode: modePtr(requesthandling.BodyFull)},
			},
		},
	}

	computeResponseBuffering(profiles, logger)

	if profiles["streaming"].NeedsResponseBuffering {
		t.Error("streaming profile should not need buffering")
	}
	if !profiles["full-body"].NeedsResponseBuffering {
		t.Error("full-body profile should need buffering")
	}
}
