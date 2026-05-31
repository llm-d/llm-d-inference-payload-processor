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
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(configapi.Install(scheme))
}

func LoadConfiguration(configBytes []byte, handle plugin.Handle, logger logr.Logger) (*config.Config, error) {
	rawConfig, err := loadRawConfiguration(configBytes, logger)
	if err != nil {
		return nil, err
	}

	if err = instantiatePlugins(rawConfig.Plugins, handle); err != nil {
		logger.Error(err, "failed to instantiate one or more plugins")
		return nil, err
	}

	profiles, err := buildProfiles(rawConfig.Profiles, handle)
	if err != nil {
		logger.Error(err, "failed to load one or more profiles")
		return nil, err
	}

	notificationSources, err := buildDatalayer(rawConfig.NotificationSources, handle)
	if err != nil {
		logger.Error(err, "failed to load one or more notification sources")
		return nil, err
	}

	return &config.Config{
		Profiles:            profiles,
		NotificationSources: notificationSources,
	}, nil
}

func buildDatalayer(refs []configapi.PluginRef, handle plugin.Handle) ([]datasource.NotificationSource, error) {
	sources := make([]datasource.NotificationSource, 0, len(refs))
	for _, ref := range refs {
		p := handle.Plugin(ref.PluginRef)
		if p == nil {
			return nil, fmt.Errorf("there is no plugin named %s", ref.PluginRef)
		}
		src, ok := p.(datasource.NotificationSource)
		if !ok {
			return nil, fmt.Errorf("the plugin named %s is not a NotificationSource", ref.PluginRef)
		}
		sources = append(sources, src)
	}
	return sources, nil
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

func buildProfiles(rawProfiles []configapi.Profile, handle plugin.Handle) (map[string]requesthandling.Profile, error) {
	if len(rawProfiles) == 0 {
		return nil, errors.New("at least one profile must be specified")
	}

	profiles := map[string]requesthandling.Profile{}

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

		theProfile := requesthandling.Profile{
			RequestPlugins:  make([]requesthandling.RequestProcessor, len(rawProfile.Plugins.Request)),
			ResponsePlugins: make([]requesthandling.ResponseProcessor, len(rawProfile.Plugins.Response)),
		}

		for idx, pluginRef := range rawProfile.Plugins.Request {
			rawPlugin := handle.Plugin(pluginRef.PluginRef)
			if rawPlugin == nil {
				return nil, fmt.Errorf("there is no plugin named %s", pluginRef.PluginRef)
			}
			thePlugin, ok := rawPlugin.(requesthandling.RequestProcessor)
			if !ok {
				return nil, fmt.Errorf("the plugin named %s is not a RequestProcessor", pluginRef.PluginRef)
			}
			theProfile.RequestPlugins[idx] = thePlugin
		}

		for idx, pluginRef := range rawProfile.Plugins.Response {
			rawPlugin := handle.Plugin(pluginRef.PluginRef)
			if rawPlugin == nil {
				return nil, fmt.Errorf("there is no plugin named %s", pluginRef.PluginRef)
			}
			thePlugin, ok := rawPlugin.(requesthandling.ResponseProcessor)
			if !ok {
				return nil, fmt.Errorf("the plugin named %s is not a ResponseProcessor", pluginRef.PluginRef)
			}
			theProfile.ResponsePlugins[idx] = thePlugin
		}

		profiles[rawProfile.Name] = theProfile
	}

	return profiles, nil
}
