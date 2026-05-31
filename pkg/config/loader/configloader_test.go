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
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/google/go-cmp/cmp"
	configapi "github.com/llm-d/llm-d-inference-payload-processor/apix/config/v1alpha1"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/modelselector"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/datalayer/notificationsource"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/requesthandling/basemodelextractor"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/requesthandling/bodyfieldtoheader"
)

// Define constants for test plugins.
// Constants must match those used in testdata_test.go.
const (
	testPickerType       = "test-picker"
	testPluginType       = "test-plugin"
	testRequestProcType  = "test-request-processor"
	testResponseProcType = "test-response-processor"
	testScorerType       = "test-scorer"
)

func TestLoadRawConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		configText string
		want       *configapi.PayloadProcessorConfig
		wantErr    bool
	}{
		{
			name:       "Success - Full Configuration",
			configText: successConfigText,
			want: &configapi.PayloadProcessorConfig{
				TypeMeta: metav1.TypeMeta{
					Kind:       "PayloadProcessorConfig",
					APIVersion: "llm-d.ai/v1alpha1",
				},
				Plugins: []configapi.PluginSpec{
					{Name: testRequestProcType, Type: testRequestProcType},
					{Name: testResponseProcType, Type: testResponseProcType},
					{Name: "test1", Type: testPluginType, Parameters: json.RawMessage(`{"threshold":10}`)},
					{Name: testScorerType, Type: testScorerType, Parameters: json.RawMessage(`{"cost":42}`)},
					{Name: "testPicker", Type: testPickerType},
				},
			},
			wantErr: false,
		},
		{
			name:       "Success - Default configuration",
			configText: "",
			want: &configapi.PayloadProcessorConfig{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "llm-d.ai/v1alpha1",
					Kind:       "PayloadProcessorConfig",
				},
				Plugins: []configapi.PluginSpec{
					{
						Name:       bodyfieldtoheader.BodyFieldToHeaderPluginType,
						Type:       bodyfieldtoheader.BodyFieldToHeaderPluginType,
						Parameters: json.RawMessage(`{"fieldName": "model", "headerName": "X-Gateway-Model-Name"}`),
					},
					{
						Name: basemodelextractor.BaseModelToHeaderPluginType,
						Type: basemodelextractor.BaseModelToHeaderPluginType,
					},
				},
				Profiles: []configapi.Profile{
					{
						Name: "default",
						Plugins: &configapi.ProfilePlugins{
							Request: []configapi.PluginRef{
								{
									PluginRef: bodyfieldtoheader.BodyFieldToHeaderPluginType,
								},
								{
									PluginRef: basemodelextractor.BaseModelToHeaderPluginType,
								},
							},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name:       "Error - Invalid YAML",
			configText: errorBadYamlText,
			wantErr:    true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			logger := logging.NewTestLogger()

			got, err := loadRawConfiguration([]byte(tc.configText), logger)

			if tc.wantErr {
				require.Error(t, err, "Expected LoadRawConfig to fail")
				return
			}
			require.NoError(t, err, "Expected LoadRawConfig to succeed")
			diff := cmp.Diff(tc.want, got)
			require.Empty(t, diff, "Config mismatch (-want +got):\n%s", diff)
		})
	}
}

func TestInstantiatePlugins(t *testing.T) {
	// Not parallel because it modifies global plugin registry.
	registerTestPlugins(t)

	tests := []struct {
		name       string
		configText string
		wantErr    bool
		validate   func(t *testing.T, handle plugin.Handle)
	}{
		// --- Success Scenarios ---

		{
			name:       "Successful load of plugins",
			configText: successConfigText,
			wantErr:    false,
			validate: func(t *testing.T, handle plugin.Handle) {
				loadedPlugins := handle.GetAllPlugins()
				require.Len(t, loadedPlugins, 5)
				require.NotNil(t, handle.Plugin("test1"), "Explicit test plugin should be instantiated")
				require.NotNil(t, handle.Plugin(testScorerType), "Explicit scorer should be instantiated")
				require.NotNil(t, handle.Plugin("testPicker"), "Explicit picker should be instantiated")
			},
		},

		// --- Instantiation Errors ---

		{
			name:       "Error (Instantiation) - Missing Type Field",
			configText: errorBadPluginReferenceText,
			wantErr:    true,
		},
		{
			name:       "Error (Instantiation) - Unknown Plugin Type",
			configText: errorBadPluginReferencePluginText,
			wantErr:    true,
		},
		{
			name:       "Error (Instantiation) - Invalid JSON Parameters",
			configText: errorBadPluginJSONText,
			wantErr:    true,
		},
		{
			name:       "Error (Instantiation) - Duplicate plugin name",
			configText: errorDuplicatePluginText,
			wantErr:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logger := logging.NewTestLogger()

			// 1. Load Raw (Assuming valid yaml/structure for Phase 2 tests)
			rawConfig, err := loadRawConfiguration([]byte(tc.configText), logger)
			if err != nil {
				// If we expected failure (and it failed early in Phase 1), success.
				if tc.wantErr {
					return
				}
				require.NoError(t, err, "Setup: LoadRawConfig failed")
			}

			// 2. Instantiate
			handle := plugin.NewHandle(context.Background(), nil, nil)
			err = instantiatePlugins(rawConfig.Plugins, handle)

			if tc.wantErr {
				require.Error(t, err, "Expected instantiatePlugins to fail")
				return
			}
			require.NoError(t, err, "Expected instantiatePlugins to succeed")

			if tc.validate != nil {
				tc.validate(t, handle)
			}
		})
	}
}

// --- Mocks ---

type mockPlugin struct {
	t plugin.TypedName
}

func (m *mockPlugin) TypedName() plugin.TypedName { return m.t }

// Mock RequestProcessor
type mockRequestProcessor struct{ mockPlugin }

// compile-time type assertion
var _ requesthandling.RequestProcessor = &mockRequestProcessor{}

func (m *mockRequestProcessor) ProcessRequest(ctx context.Context, cycleState *plugin.CycleState, request *requesthandling.InferenceRequest) error {
	return nil
}

// Mock ResponseProcessor
type mockResponseProcessor struct{ mockPlugin }

// compile-time type assertion
var _ requesthandling.ResponseProcessor = &mockResponseProcessor{}

func (m *mockResponseProcessor) ProcessResponse(ctx context.Context, cycleState *plugin.CycleState, request *requesthandling.InferenceResponse) error {
	return nil
}

// Mock Scorer
type mockScorer struct{ mockPlugin }

// compile-time type assertion
var _ modelselector.Scorer = &mockScorer{}

func (m *mockScorer) Score(ctx context.Context, cycleState *plugin.CycleState, request *requesthandling.InferenceRequest, models []datalayer.Model) map[datalayer.Model]float64 {
	return nil
}

// Mock Picker
type mockPicker struct{ mockPlugin }

// compile-time type assertion
var _ modelselector.Picker = &mockPicker{}

func (m *mockPicker) Pick(ctx context.Context, cycleState *plugin.CycleState, scoredModels []*modelselector.ScoredModel) *modelselector.ProfileRunResult {
	return nil
}

func TestBuildDatalayer(t *testing.T) {
	// Not parallel because it modifies global plugin registry.
	plugin.Register(notificationsource.PluginType, notificationsource.Factory)
	registerTestPlugins(t)

	tests := []struct {
		name       string
		configText string
		wantLen    int
		wantErr    bool
	}{
		{
			name:       "Success - no notification sources",
			configText: successConfigText,
			wantLen:    0,
		},
		{
			name:       "Success - valid notification source ref",
			configText: datalayerSuccessConfigText,
			wantLen:    1,
		},
		{
			name:       "Error - missing plugin ref",
			configText: datalayerMissingRefConfigText,
			wantErr:    true,
		},
		{
			name:       "Error - plugin is not a NotificationSource",
			configText: datalayerWrongTypeConfigText,
			wantErr:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logger := logging.NewTestLogger()

			rawConfig, err := loadRawConfiguration([]byte(tc.configText), logger)
			require.NoError(t, err, "setup: loadRawConfiguration failed")

			handle := plugin.NewHandle(context.Background(), nil, nil)
			err = instantiatePlugins(rawConfig.Plugins, handle)
			require.NoError(t, err, "setup: instantiatePlugins failed")

			sources, err := buildDatalayer(rawConfig.NotificationSources, handle)

			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Len(t, sources, tc.wantLen)
		})
	}
}

func registerTestPlugins(t *testing.T) {
	t.Helper()

	// Register standard test mocks.
	plugin.Register(testPluginType,
		func(name string, params json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
			return &mockPlugin{t: plugin.TypedName{Name: name, Type: testPluginType}}, nil
		})

	plugin.Register(testRequestProcType,
		func(name string, params json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
			return &mockRequestProcessor{mockPlugin{t: plugin.TypedName{Name: name, Type: testRequestProcType}}}, nil
		})

	plugin.Register(testResponseProcType,
		func(name string, params json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
			return &mockResponseProcessor{mockPlugin{t: plugin.TypedName{Name: name, Type: testResponseProcType}}}, nil
		})

	plugin.Register(testPickerType,
		func(name string, params json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
			return &mockPicker{mockPlugin{t: plugin.TypedName{Name: name, Type: testPickerType}}}, nil
		})

	plugin.Register(testScorerType, func(name string, params json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
		// Attempt to unmarshal to trigger errors for invalid JSON in tests.
		if len(params) > 0 {
			var p struct {
				Cost float32 `json:"cost"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, err
			}
		}
		return &mockScorer{mockPlugin{t: plugin.TypedName{Name: name, Type: testScorerType}}}, nil
	})
}
