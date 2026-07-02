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
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/google/go-cmp/cmp"
	configapi "github.com/llm-d/llm-d-inference-payload-processor/apix/config/v1alpha1"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	datalayerpkg "github.com/llm-d/llm-d-inference-payload-processor/pkg/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer/datasource"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/modelselector"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/modelselector/picker/maxscore"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/modelselector/scorer/costaware"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/requesthandling/basemodelextractor"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/requesthandling/bodyfieldtoheader"
	modelselectorplugin "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/requesthandling/modelselector"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/requesthandling/profilepicker/single"
	ms "github.com/llm-d/llm-d-inference-payload-processor/pkg/modelselector"
)

// Define constants for test plugins.
// Constants must match those used in testdata_test.go.
const (
	testFilterType       = "test-filter"
	testPickerType       = "test-picker"
	testPluginType       = "test-plugin"
	testProfilePicker    = "test-profile-picker"
	testRequestProcType  = "test-request-processor"
	testResponseProcType = "test-response-processor"
	testScorerType       = "test-scorer"
	testExtractorType    = "test-extractor"
	testCollectorType    = "test-collector"
	testDataSourceType   = "test-datasource"
)

// TestLoadRawConfiguration verifies that YAML config text is parsed into the expected PayloadProcessorConfig structure.
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

// TestInstantiatePlugins verifies that plugins declared in config are correctly instantiated via the registry.
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
			handle := plugin.NewHandle(context.Background(), nil, nil, nil)
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

func TestBuildProfiles(t *testing.T) {
	// Not parallel because it modifies global plugin registry.
	registerTestPlugins(t)
	plugin.Register(single.SingleProfilePickerType, single.SingleProfilePickerFactory)

	tests := []struct {
		name       string
		configText string
		validate   func(*testing.T, *configapi.PayloadProcessorConfig, map[string]*requesthandling.Profile, plugin.Handle)
		wantErr    bool
	}{
		{
			name:       "successConfigWithProfile",
			configText: successConfigWithProfileText,
			validate: func(t *testing.T, rawConfig *configapi.PayloadProcessorConfig, profiles map[string]*requesthandling.Profile, handle plugin.Handle) {
				require.Equal(t, rawConfig.ProfilePicker.PluginRef, testProfilePicker, "incorrect profile picker")
				require.Equal(t, 1, len(profiles), "there should only be one profile")
				require.NotNil(t, profiles["default"], "the profile `default` wasn't created")
				require.Equal(t, 1, len(profiles["default"].RequestPlugins), "there should be one request plugin")
				require.Equal(t, 1, len(profiles["default"].ResponsePlugins), "there should be one response plugin")
			},
			wantErr: false,
		},
		{
			name:       "successConfigWithTwoProfiles",
			configText: successConfigWithTwoProfilesText,
			validate: func(t *testing.T, rawConfig *configapi.PayloadProcessorConfig, profiles map[string]*requesthandling.Profile, handle plugin.Handle) {
				require.Equal(t, rawConfig.ProfilePicker.PluginRef, testProfilePicker, "incorrect profile picker")
				require.Equal(t, 2, len(profiles), "there should be two profiles")
				require.NotNil(t, profiles["one"], "the profile `one` wasn't created")
				require.NotNil(t, profiles["two"], "the profile `two` wasn't created")
				require.Equal(t, 1, len(profiles["one"].RequestPlugins), "there should be one request plugin")
				require.Equal(t, 0, len(profiles["one"].ResponsePlugins), "there should be no response plugins")
				require.Equal(t, 0, len(profiles["two"].RequestPlugins), "there should be no request plugins")
				require.Equal(t, 1, len(profiles["two"].ResponsePlugins), "there should be one response plugin")
			},
			wantErr: false,
		},
		{
			name:       "successConfigWithNoProfilePicker",
			configText: successConfigWithNoProfilePickerText,
			validate: func(t *testing.T, rawConfig *configapi.PayloadProcessorConfig, profiles map[string]*requesthandling.Profile, handle plugin.Handle) {
				require.Equal(t, rawConfig.ProfilePicker.PluginRef, single.SingleProfilePickerType, "incorrect profile picker")
				require.Equal(t, 3, len(handle.GetAllPlugins()), "not enough plugins were instantiated")
				require.Equal(t, 1, len(profiles), "there should only be one profile")
				require.NotNil(t, profiles["default"], "the profile `default` wasn't created")
				require.Equal(t, 1, len(profiles["default"].RequestPlugins), "there should be one request plugin")
				require.Equal(t, 1, len(profiles["default"].ResponsePlugins), "there should be one response plugin")
			},
			wantErr: false,
		},
		{
			name:       "successConfigWithProfilePickerNotReferenced",
			configText: successConfigWithProfilePickerNotReferencedText,
			validate: func(t *testing.T, rawConfig *configapi.PayloadProcessorConfig, profiles map[string]*requesthandling.Profile, handle plugin.Handle) {
				require.Equal(t, rawConfig.ProfilePicker.PluginRef, testProfilePicker, "incorrect profile picker")
				require.Equal(t, 1, len(profiles), "there should only be one profile")
				require.NotNil(t, profiles["default"], "the profile `default` wasn't created")
				require.Equal(t, 1, len(profiles["default"].RequestPlugins), "there should be one request plugin")
				require.Equal(t, 1, len(profiles["default"].ResponsePlugins), "there should be one response plugin")
			},
			wantErr: false,
		},
		{
			name:       "errorConfigWithTwoProfilesNoPicker",
			configText: errorConfigWithTwoProfilesNoPickerText,
			wantErr:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logger := logging.NewTestLogger()

			rawConfig, err := loadRawConfiguration([]byte(tc.configText), logger)
			require.NoError(t, err, "setup: loadRawConfiguration failed")

			handle := plugin.NewHandle(context.Background(), nil, nil, nil)
			err = instantiatePlugins(rawConfig.Plugins, handle)
			require.NoError(t, err, "setup: instantiatePlugins failed")

			err = applyPluginDefaults(rawConfig, handle)
			if tc.wantErr && err != nil {
				return
			}

			profiles, errProf := buildProfiles(rawConfig.Profiles, handle)
			if tc.wantErr {
				if err == nil && errProf == nil {
					t.Errorf("either applyPluginDefaults or buildProfiles was suppose to fail")
				}
				return
			}
			require.NoError(t, err, "applyDefaultPlugins failed")
			require.NoError(t, errProf, "buildProfiles failed")
			if tc.validate != nil {
				tc.validate(t, rawConfig, profiles, handle)
			}
		})
	}
}

func TestBuildPreAndPostProcessors(t *testing.T) {
	// Not parallel because it modifies global plugin registry.
	registerTestPlugins(t)

	tests := []struct {
		name       string
		configText string
		preLen     int
		postLen    int
		wantErr    bool
	}{
		{
			name:       "successConfigPreAndPostProcessors",
			configText: successConfigPreAndPostProcessorsText,
			preLen:     2,
			postLen:    1,
		},
		{
			name:       "successConfigPreProcessorsOnly",
			configText: successConfigPreProcessorsOnlyText,
			preLen:     2,
			postLen:    0,
		},
		{
			name:       "successConfigPostProcessorsOnly",
			configText: successConfigPostProcessorsOnlyText,
			preLen:     0,
			postLen:    1,
		},
		{
			name:       "errorBadPreProcessors",
			configText: errorBadPreProcessorsText,
			wantErr:    true,
		},
		{
			name:       "errorBadPostProcessors",
			configText: errorBadPostProcessorsText,
			wantErr:    true,
		},
		{
			name:       "errorMissingPreProcessors",
			configText: errorMissingPreProcessorsText,
			wantErr:    true,
		},
		{
			name:       "errorMissingPostProcessors",
			configText: errorMissingPostProcessorsText,
			wantErr:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logger := logging.NewTestLogger()

			rawConfig, err := loadRawConfiguration([]byte(tc.configText), logger)
			require.NoError(t, err, "setup: loadRawConfiguration failed")

			handle := plugin.NewHandle(context.Background(), nil, nil, nil)
			err = instantiatePlugins(rawConfig.Plugins, handle)
			require.NoError(t, err, "setup: instantiatePlugins failed")

			preProcessors, preErr := buildPreProcessors(rawConfig.PreProcessing, handle)
			postProcessors, postErr := buildPostProcessors(rawConfig.PostProcessing, handle)

			if tc.wantErr {
				if preErr == nil && postErr == nil {
					t.Errorf("either buildPreProcessors or buildPostProcessors was suppose to fail")
				}
				t.Log("preErr=", preErr)
				t.Log("postErr=", postErr)
			} else {
				require.NoError(t, preErr, "buildPreProcessors wasn't suppose to fail")
				require.NoError(t, postErr, "buildPostProcessors wasn't suppose to fail")

				require.Equal(t, tc.preLen, len(preProcessors), "buildPreProcessors didn't return the correct number of pre-processors")
				require.Equal(t, tc.postLen, len(postProcessors), "buildPostProcessors didn't return the correct number of post-processors")
			}
		})
	}
}

// TestBuildDatalayerSources verifies that datalayer plugins are resolved and registered from config refs.
func TestBuildDatalayerSources(t *testing.T) {
	// Not parallel because it modifies global plugin registry.
	registerTestPlugins(t)

	tests := []struct {
		name       string
		configText string
		wantErr    bool
	}{
		{
			name:       "Success - no datalayer sources",
			configText: successConfigText,
		},
		{
			name:       "Success - valid datalayer refs for all categories",
			configText: datalayerSuccessConfigText,
		},
		{
			name:       "Error - missing plugin ref",
			configText: datalayerMissingRefConfigText,
			wantErr:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logger := logging.NewTestLogger()
			// Initialize fake processor for testing
			processor := datalayerpkg.NewFakeProcessor()

			rawConfig, err := loadRawConfiguration([]byte(tc.configText), logger)
			require.NoError(t, err, "setup: loadRawConfiguration failed")

			handle := plugin.NewHandle(context.Background(), nil, nil, nil)
			err = instantiatePlugins(rawConfig.Plugins, handle)
			require.NoError(t, err, "setup: instantiatePlugins failed")

			err = buildDatalayerSources(rawConfig.Datalayer, handle, processor)

			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

// --- Mocks ---

type mockPlugin struct {
	t plugin.TypedName
}

func (m *mockPlugin) TypedName() plugin.TypedName { return m.t }

// Mock ProfilePicker
type mockProfilePicker struct{ mockPlugin }

// compile-time type assertion
var _ requesthandling.ProfilePicker = &mockProfilePicker{}

func (m *mockProfilePicker) Pick(ctx context.Context, cycleState *plugin.CycleState, request *requesthandling.InferenceRequest,
	profiles map[string]*requesthandling.Profile) (*requesthandling.Profile, error) {
	return nil, nil
}

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

// Mock Filter
type mockFilter struct{ mockPlugin }

// compile-time type assertion
var _ modelselector.Filter = &mockFilter{}

func (m *mockFilter) Filter(_ context.Context, _ *plugin.CycleState, _ *requesthandling.InferenceRequest, models []datalayer.Model) []datalayer.Model {
	return models
}

// Mock Scorer
type mockScorer struct{ mockPlugin }

// compile-time type assertion
var _ modelselector.Scorer = &mockScorer{}

func (m *mockScorer) Score(ctx context.Context, cycleState *plugin.CycleState, request *requesthandling.InferenceRequest, models []datalayer.Model) map[datalayer.Model]float64 {
	return nil
}

// Mock Extractor
type mockExtractor struct{ mockPlugin }

var _ datasource.Extractor = &mockExtractor{}

func (m *mockExtractor) Extract(_ context.Context, _ []datasource.Event) error { return nil }

// Mock Collector
type mockCollector struct{ mockPlugin }

var _ datasource.Collector = &mockCollector{}

func (m *mockCollector) Poll(_ context.Context) (any, error) { return nil, nil }
func (m *mockCollector) CollectorFrequency() time.Duration   { return 0 }

// Mock DataSource
type mockDataSource struct{ mockPlugin }

var _ datasource.DataSource = &mockDataSource{}

func (m *mockDataSource) Start(_ context.Context) error { return nil }
func (m *mockDataSource) Stop()                         {}

// Mock Picker
type mockPicker struct{ mockPlugin }

// compile-time type assertion
var _ modelselector.Picker = &mockPicker{}

func (m *mockPicker) Pick(ctx context.Context, cycleState *plugin.CycleState, scoredModels []*modelselector.ScoredModel) *modelselector.PipelineRunResult {
	return nil
}

func registerTestPlugins(t *testing.T) {
	t.Helper()

	// Register standard test mocks.
	plugin.Register(testPluginType,
		func(name string, params json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
			return &mockPlugin{t: plugin.TypedName{Name: name, Type: testPluginType}}, nil
		})

	plugin.Register(testProfilePicker,
		func(name string, params json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
			return &mockProfilePicker{mockPlugin{t: plugin.TypedName{Name: name, Type: testProfilePicker}}}, nil
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

	plugin.Register(testFilterType,
		func(name string, _ json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
			return &mockFilter{mockPlugin{t: plugin.TypedName{Name: name, Type: testFilterType}}}, nil
		})

	plugin.Register(testExtractorType,
		func(name string, _ json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
			return &mockExtractor{mockPlugin{t: plugin.TypedName{Name: name, Type: testExtractorType}}}, nil
		})

	plugin.Register(testCollectorType,
		func(name string, _ json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
			return &mockCollector{mockPlugin{t: plugin.TypedName{Name: name, Type: testCollectorType}}}, nil
		})

	plugin.Register(testDataSourceType,
		func(name string, _ json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
			return &mockDataSource{mockPlugin{t: plugin.TypedName{Name: name, Type: testDataSourceType}}}, nil
		})
}

// registerModelSelectorPlugins registers the real model-selector plugin factories used by
// TestBuildProfilesModelSelectorPlugins.
func registerModelSelectorPlugins(t *testing.T) {
	t.Helper()
	plugin.Register(modelselectorplugin.ModelSelectorPluginType, modelselectorplugin.ModelSelectorPluginFactory)
	plugin.Register(costaware.CostScorerType, costaware.CostScorerFactory)
	plugin.Register(maxscore.MaxScorePickerType, maxscore.MaxScorePickerFactory)
	plugin.Register(testFilterType,
		func(name string, _ json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
			return &mockFilter{mockPlugin{t: plugin.TypedName{Name: name, Type: testFilterType}}}, nil
		})
}

func TestBuildProfilesModelSelectorPlugins(t *testing.T) {
	// Not parallel: modifies global plugin registry.
	registerModelSelectorPlugins(t)

	logger := logging.NewTestLogger()
	handle := plugin.NewHandle(context.Background(), nil, nil, nil)
	processor := datalayerpkg.NewFakeProcessor()

	cfg, err := LoadConfiguration([]byte(modelSelectorAllPluginTypesText), handle, processor, logger)
	require.NoError(t, err, "LoadConfiguration should succeed")

	profile, ok := cfg.Profiles["default"]
	require.True(t, ok, "profile 'default' must exist")

	// model-selector itself is a RequestProcessor and must be in RequestPlugins.
	require.Len(t, profile.RequestPlugins, 1, "expected one RequestProcessor (the model-selector plugin)")
	_, isMS := profile.RequestPlugins[0].(*modelselectorplugin.ModelSelectorPlugin)
	require.True(t, isMS, "RequestPlugins[0] must be a ModelSelectorPlugin")

	// Filter, WeightedScorer (cost-scorer @ 2.5), Picker must be in ModelSelectorPlugins.
	require.Len(t, profile.ModelSelectorPlugins, 3, "expected three ModelSelectorPlugins (filter, scorer, picker)")

	_, isFilter := profile.ModelSelectorPlugins[0].(modelselector.Filter)
	require.True(t, isFilter, "ModelSelectorPlugins[0] must implement Filter")

	ws, isWeightedScorer := profile.ModelSelectorPlugins[1].(*ms.WeightedScorer)
	require.True(t, isWeightedScorer, "ModelSelectorPlugins[1] must be a *WeightedScorer")
	require.Equal(t, 2.5, ws.Weight(), "scorer weight must be 2.5")
	require.Equal(t, costaware.CostScorerType, ws.TypedName().Type, "scorer type must be cost-scorer")

	_, isPicker := profile.ModelSelectorPlugins[2].(modelselector.Picker)
	require.True(t, isPicker, "ModelSelectorPlugins[2] must implement Picker")

	// The model-selector plugin's internal profile must have received the wired plugins.
	msPlugin := profile.RequestPlugins[0].(*modelselectorplugin.ModelSelectorPlugin)
	msPipeline := msPlugin.Pipeline()
	require.Len(t, msPipeline.Filters(), 1, "model-selector pipeline must have one filter")
	require.Len(t, msPipeline.Scorers(), 1, "model-selector pipeline must have one scorer")
	require.NotNil(t, msPipeline.Picker(), "model-selector pipeline must have a picker")
}

func TestBuildProfilesScorerMissingWeight(t *testing.T) {
	// Not parallel: modifies global plugin registry.
	registerModelSelectorPlugins(t)

	logger := logging.NewTestLogger()
	handle := plugin.NewHandle(context.Background(), nil, nil, nil)
	processor := datalayerpkg.NewFakeProcessor()

	_, err := LoadConfiguration([]byte(modelSelectorScorerMissingWeightText), handle, processor, logger)
	require.ErrorContains(t, err, "requires a weight")
}

func TestBuildProfilesUnknownPluginType(t *testing.T) {
	// Not parallel: modifies global plugin registry.
	registerTestPlugins(t)
	registerModelSelectorPlugins(t)

	logger := logging.NewTestLogger()
	handle := plugin.NewHandle(context.Background(), nil, nil, nil)
	processor := datalayerpkg.NewFakeProcessor()

	_, err := LoadConfiguration([]byte(modelSelectorUnknownPluginTypeText), handle, processor, logger)
	require.ErrorContains(t, err, "is not a RequestProcessor, Filter, Scorer, or Picker")
}
