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
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"

	configapi "github.com/llm-d/llm-d-inference-payload-processor/apix/config/v1alpha1"
	config "github.com/llm-d/llm-d-inference-payload-processor/pkg/config"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer/datasource"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/modelselector"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/modelselector/picker/maxscore"
	modelselectorplugin "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/requesthandling/modelselector"
	ms "github.com/llm-d/llm-d-inference-payload-processor/pkg/modelselector"
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(configapi.Install(scheme))
}

func LoadConfiguration(configBytes []byte, handle plugin.Handle, processor datasource.DatalayerProcessor, logger logr.Logger) (*config.Config, error) {
	rawConfig, err := loadRawConfiguration(configBytes, logger)
	if err != nil {
		return nil, err
	}

	if err = instantiatePlugins(rawConfig.Plugins, handle); err != nil {
		logger.Error(err, "failed to instantiate one or more plugins")
		return nil, err
	}

	if err = applyPluginDefaults(rawConfig, handle); err != nil {
		logger.Error(err, "failed to inject default plugins")
		return nil, err
	}

	var profilePicker requesthandling.ProfilePicker
	var ok bool
	if profilePicker, ok = handle.Plugin(rawConfig.ProfilePicker.PluginRef).(requesthandling.ProfilePicker); !ok {
		err = fmt.Errorf("the profilePicker referenced in the configuration (%s) is not a requesthandling.ProfilePicker", rawConfig.ProfilePicker.PluginRef)
		logger.Error(err, "failed to load the configuration")
	}

	profiles, err := buildProfiles(rawConfig.Profiles, handle)
	if err != nil {
		logger.Error(err, "failed to load one or more profiles")
		return nil, err
	}

	preProcessors, err := buildPreProcessors(rawConfig.PreProcessing, handle)
	if err != nil {
		logger.Error(err, "failed to load one or more pre-processors")
		return nil, err
	}

	postProcessors, err := buildPostProcessors(rawConfig.PostProcessing, handle)
	if err != nil {
		logger.Error(err, "failed to load one or more post-processors")
		return nil, err
	}

	if len(postProcessors) > 0 {
		// post processors always require full body. it cannot be mixed with profile that is running on chunks.
		// the framework supports either only full body response processors or chunk response processors.
		for name, p := range profiles {
			if len(p.ResponseChunkProcessors) > 0 {
				return nil, fmt.Errorf("profile %s is using ResponseChunkProcessor plugins while post processor always require full body. the framework must use one type exclusively", name)
			}
			// force profile to require buffering even if no chunk processors are configured
			// to make sure post processors run before chunks are returns to the client
			p.NeedsResponseBuffering = true
		}
	}

	if err = buildDatalayerSources(rawConfig.Datalayer, handle, processor); err != nil {
		logger.Error(err, "failed to load one or more datalayer sources")
		return nil, err
	}

	if err = buildModelSelector(profiles, handle); err != nil {
		logger.Error(err, "failed to build model selector profiles")
		return nil, err
	}

	return &config.Config{
		ProfilePicker:  profilePicker,
		Profiles:       profiles,
		PreProcessors:  preProcessors,
		PostProcessors: postProcessors,
	}, nil
}

func buildDatalayerSources(cfg *configapi.DatalayerConfig, handle plugin.Handle, processor datasource.DatalayerProcessor) error {
	if cfg == nil {
		return nil
	}
	for _, ref := range cfg.Collectors {
		p := handle.Plugin(ref.PluginRef)
		if p == nil {
			return fmt.Errorf("there is no plugin named %s", ref.PluginRef)
		}
		c, ok := p.(datasource.Collector)
		if !ok {
			return fmt.Errorf("plugin %q is not a Collector", ref.PluginRef)
		}
		processor.RegisterCollector(c, c.CollectorFrequency())
	}
	for _, ref := range cfg.Extractors {
		p := handle.Plugin(ref.PluginRef)
		if p == nil {
			return fmt.Errorf("there is no plugin named %s", ref.PluginRef)
		}
		e, ok := p.(datasource.Extractor)
		if !ok {
			return fmt.Errorf("plugin %q is not an Extractor", ref.PluginRef)
		}
		processor.RegisterExtractor(e)
	}
	for _, ref := range cfg.Datasources {
		p := handle.Plugin(ref.PluginRef)
		if p == nil {
			return fmt.Errorf("there is no plugin named %s", ref.PluginRef)
		}
		d, ok := p.(datasource.DataSource)
		if !ok {
			return fmt.Errorf("plugin %q is not a DataSource", ref.PluginRef)
		}
		processor.RegisterDatasource(d)
	}
	return nil
}

func loadRawConfiguration(configBytes []byte, logger logr.Logger) (*configapi.PayloadProcessorConfig, error) {
	var rawConfig *configapi.PayloadProcessorConfig
	var err error
	if len(configBytes) != 0 {
		rawConfig = &configapi.PayloadProcessorConfig{}
		codecs := serializer.NewCodecFactory(scheme, serializer.EnableStrict)
		if err := runtime.DecodeInto(codecs.UniversalDecoder(), configBytes, rawConfig); err != nil {
			logger.Error(err, "failed to decode configuration JSON/YAML")
			return nil, fmt.Errorf("failed to decode configuration JSON/YAML: %w", err)
		}
		logger.Info("Loaded raw configuration", "config", rawConfig.String())
	} else {
		logger.Info("A configuration wasn't specified. A default one is being used.")
		rawConfig = loadDefaultConfig()
		logger.Info("Default raw configuration used", "config", rawConfig.String())
	}

	applyRawConfigDefaults(rawConfig)

	return rawConfig, err
}

func instantiatePlugins(configuredPlugins []configapi.PluginSpec, handle plugin.Handle) error {
	pluginNames := sets.New[string]()
	if len(configuredPlugins) == 0 {
		return errors.New("one or more plugins must be defined")
	}

	for _, spec := range configuredPlugins {
		if spec.Type == "" {
			return fmt.Errorf("plugin '%s' is missing a type", spec.Name)
		}
		if pluginNames.Has(spec.Name) {
			return fmt.Errorf("duplicate plugin name '%s'", spec.Name)
		}
		pluginNames.Insert(spec.Name)

		factory, ok := plugin.Registry[spec.Type]
		if !ok {
			return fmt.Errorf("plugin type '%s' is not registered", spec.Type)
		}
		plugin, err := factory(spec.Name, spec.Parameters, handle)
		if err != nil {
			return fmt.Errorf("failed to create plugin '%s' (type: %s): %w", spec.Name, spec.Type, err)
		}

		handle.AddPlugin(spec.Name, plugin)
	}

	return nil
}

func buildProfiles(rawProfiles []configapi.Profile, handle plugin.Handle) (map[string]*requesthandling.Profile, error) {
	if len(rawProfiles) == 0 {
		return nil, errors.New("at least one profile must be specified")
	}

	profiles := map[string]*requesthandling.Profile{}

	for _, rawProfile := range rawProfiles {
		if len(rawProfile.Name) == 0 {
			return nil, errors.New("a profile was specified without a name")
		}
		if rawProfile.Plugins == nil {
			return nil, fmt.Errorf("the profile %s must have a Plugins section", rawProfile.Name)
		}
		if len(rawProfile.Plugins.Request) == 0 && len(rawProfile.Plugins.Response) == 0 {
			return nil, fmt.Errorf("the profile %s must have one or both of the Request and Response sections", rawProfile.Name)
		}

		theProfile := requesthandling.Profile{}

		for _, pluginRef := range rawProfile.Plugins.Request {
			rawPlugin := handle.Plugin(pluginRef.PluginRef)
			if rawPlugin == nil {
				return nil, fmt.Errorf("there is no plugin named %s", pluginRef.PluginRef)
			}
			if reqPlugin, ok := rawPlugin.(requesthandling.RequestProcessor); ok {
				theProfile.RequestPlugins = append(theProfile.RequestPlugins, reqPlugin)
				continue
			}
			// Not a RequestProcessor — must be a model-selector plugin (Filter/Scorer/Picker).
			_, isFilter := rawPlugin.(modelselector.Filter)
			_, isPicker := rawPlugin.(modelselector.Picker)
			scorer, isScorer := rawPlugin.(modelselector.Scorer)
			if !isFilter && !isScorer && !isPicker {
				return nil, fmt.Errorf("plugin %q is not a RequestProcessor, Filter, Scorer, or Picker", pluginRef.PluginRef)
			}
			if isScorer {
				if pluginRef.Weight == nil {
					return nil, fmt.Errorf("scorer %q requires a weight", pluginRef.PluginRef)
				}
				// Wrap as WeightedScorer; AddPlugins will also check for Filter/Picker on the inner plugin.
				theProfile.ModelSelectorPlugins = append(theProfile.ModelSelectorPlugins, ms.NewWeightedScorer(scorer, *pluginRef.Weight))
			} else {
				theProfile.ModelSelectorPlugins = append(theProfile.ModelSelectorPlugins, rawPlugin)
			}
		}

		for _, pluginRef := range rawProfile.Plugins.Response {
			rawPlugin := handle.Plugin(pluginRef.PluginRef)
			if rawPlugin == nil {
				return nil, fmt.Errorf("there is no plugin named %s", pluginRef.PluginRef)
			}
			if bodyPlugin, ok := rawPlugin.(requesthandling.ResponseProcessor); ok {
				theProfile.ResponsePlugins = append(theProfile.ResponsePlugins, bodyPlugin)
				continue
			}
			if chunkPlugin, ok := rawPlugin.(requesthandling.ResponseChunkProcessor); ok {
				theProfile.ResponseChunkProcessors = append(theProfile.ResponseChunkProcessors, chunkPlugin)
				continue
			}
			return nil, fmt.Errorf("the plugin named %s is not a ResponseProcessor nor ResponseChunkProcessor", pluginRef.PluginRef)
		}
		if len(theProfile.ResponsePlugins) > 0 && len(theProfile.ResponseChunkProcessors) > 0 {
			return nil, fmt.Errorf("profile %s mixes ResponseProcessor and ResponseChunkProcessor plugins — a profile must use one type exclusively", rawProfile.Name)
		}
		theProfile.NeedsResponseBuffering = len(theProfile.ResponsePlugins) > 0

		profiles[rawProfile.Name] = &theProfile
	}

	return profiles, nil
}

func buildPreProcessors(rawConfig *configapi.PluginRefList, handle plugin.Handle) ([]requesthandling.RequestProcessor, error) {
	if rawConfig == nil || len(rawConfig.Plugins) == 0 {
		return []requesthandling.RequestProcessor{}, nil
	}

	preProcessors := make([]requesthandling.RequestProcessor, len(rawConfig.Plugins))

	for idx, pluginRef := range rawConfig.Plugins {
		rawPlugin := handle.Plugin(pluginRef.PluginRef)
		if rawPlugin == nil {
			return nil, fmt.Errorf("the referenced pre-processor plugin %s doesn't exist in the configuration", pluginRef.PluginRef)
		}
		if preProcessor, ok := rawPlugin.(requesthandling.RequestProcessor); ok {
			preProcessors[idx] = preProcessor
		} else {
			return nil, fmt.Errorf("the referenced plugin %s is not a RequestProcessor", pluginRef.PluginRef)
		}
	}

	return preProcessors, nil
}

func buildPostProcessors(rawConfig *configapi.PluginRefList, handle plugin.Handle) ([]requesthandling.ResponseProcessor, error) {
	if rawConfig == nil || len(rawConfig.Plugins) == 0 {
		return []requesthandling.ResponseProcessor{}, nil
	}

	postProcessors := make([]requesthandling.ResponseProcessor, len(rawConfig.Plugins))

	for idx, pluginRef := range rawConfig.Plugins {
		rawPlugin := handle.Plugin(pluginRef.PluginRef)
		if rawPlugin == nil {
			return nil, fmt.Errorf("the referenced post-processor plugin %s doesn't exist in the configuration", pluginRef.PluginRef)
		}
		if postProcessor, ok := rawPlugin.(requesthandling.ResponseProcessor); ok {
			postProcessors[idx] = postProcessor
		} else {
			return nil, fmt.Errorf("the referenced plugin %s is not a ResponseProcessor", pluginRef.PluginRef)
		}
	}

	return postProcessors, nil
}

// buildModelSelector iterates all built profiles and, for each model-selector plugin found in
// RequestPlugins, calls AddPlugins with the profile's ModelSelectorPlugins. If no Picker was
// configured, MaxScorePicker is used as the default.
func buildModelSelector(profiles map[string]*requesthandling.Profile, _ plugin.Handle) error {
	for _, profile := range profiles {
		for _, reqPlugin := range profile.RequestPlugins {
			msPlugin, ok := reqPlugin.(*modelselectorplugin.ModelSelectorPlugin)
			if !ok {
				continue
			}
			if err := msPlugin.AddPlugins(profile.ModelSelectorPlugins...); err != nil {
				return fmt.Errorf("failed to add plugins to model-selector %q: %w", msPlugin.TypedName().Name, err)
			}
			if msPlugin.Pipeline().Picker() == nil {
				msPlugin.Pipeline().WithPicker(maxscore.NewMaxScorePicker())
			}
		}
	}
	return nil
}
