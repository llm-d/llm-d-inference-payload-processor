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

// PLEASE NOTE:
//
// The Kubernetes machinery is used to read this struct from YAML text.
// HOWEVER, the CRD JSON Schema validation is NOT used as we don't have
// a real CRD. The Kubernetes validation tags are just for documentation.

package v1alpha1

import (
	"encoding/json"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true

// PayloadProcessorConfig is the Schema for the payloadprocessorconfig API
type PayloadProcessorConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +required
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// Plugins is the list of plugins that will be instantiated.
	Plugins []PluginSpec `json:"plugins"`

	// +optional
	// Preprocessing is an optional ordered list of references to plugins
	// that will pre-process incoming requests
	PreProcessing *PluginRefList `json:"preProcessing"`

	// +optional
	// ProfilePicker is the plugin that chooses the profile to be used
	ProfilePicker *PluginRef `json:"profilePicker"`

	// +required
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// Profiles is the list of named Profiles
	// that will be created.
	Profiles []Profile `json:"profiles"`

	// +optional
	// Postprocessing is an optional ordered list of references to plugins
	// that will post-process incoming requests
	PostProcessing *PluginRefList `json:"postProcessing"`

	// +optional
	// Datalayer is an optional configuration for Collector, Extractor, and DataSource plugins
	// registered with the built-in datalayer Processor.
	Datalayer *DatalayerConfig `json:"datalayer,omitempty"`
}

// DatalayerConfig holds the plugin references for the three datalayer plugin categories.
type DatalayerConfig struct {
	// +optional
	// Collectors is an optional list of references to Collector plugins.
	Collectors []PluginRef `json:"collectors,omitempty"`

	// +optional
	// Extractors is an optional list of references to Extractor plugins.
	Extractors []PluginRef `json:"extractors,omitempty"`

	// +optional
	// Datasources is an optional list of references to DataSource plugins.
	Datasources []PluginRef `json:"datasources,omitempty"`
}

func (cfg PayloadProcessorConfig) String() string {
	contents := &strings.Builder{}

	fmt.Fprintf(contents, "Plugins: %v", cfg.Plugins)

	if cfg.PreProcessing != nil {
		fmt.Fprintf(contents, ", PreProcessing: %v", cfg.PreProcessing)
	}
	if cfg.ProfilePicker != nil {
		fmt.Fprintf(contents, ", ProfilePicker: %v", cfg.ProfilePicker)
	}
	fmt.Fprintf(contents, ", Profiles: %v", cfg.Profiles)
	if cfg.PostProcessing != nil {
		fmt.Fprintf(contents, ", PostProcessing: %v", cfg.PostProcessing)
	}
	if cfg.Datalayer != nil {
		fmt.Fprintf(contents, ", Datalayer: %v", cfg.Datalayer)
	}

	return "{" + contents.String() + "}"
}

// PluginSpec contains the information that describes a plugin that
// will be instantiated.
type PluginSpec struct {
	// +optional
	// Name provides a name for plugin entries to reference. If
	// omitted, the value of the Plugin's Type field will be used.
	Name string `json:"name,omitempty"`

	// +required
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// Type specifies the plugin type to be instantiated.
	Type string `json:"type,omitempty"`

	// +optional
	// Parameters are the set of parameters to be passed to the plugin's
	// factory function. The factory function is responsible
	// to parse the parameters.
	Parameters json.RawMessage `json:"parameters"`
}

func (ps PluginSpec) String() string {
	var parts []string
	if ps.Name != "" {
		parts = append(parts, "Name: "+ps.Name)
	}
	parts = append(parts, "Type: "+ps.Type)
	if len(ps.Parameters) > 0 {
		parts = append(parts, "Parameters: "+string(ps.Parameters))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// PluginRefList is a list of references to plugins
type PluginRefList struct {
	// +required
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// Plugins is the list of plugins being referenced.
	Plugins []PluginRef `json:"plugins"`
}

func (prl PluginRefList) String() string {
	contents := ""
	if len(prl.Plugins) > 0 {
		contents = fmt.Sprintf("Plugins: %v", prl.Plugins)
	}
	return fmt.Sprintf("{%s}", contents)
}

// Profile contains the information to create a Profile
// entry to be used by the PayloadProcessor.
type Profile struct {
	// +required
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// Name specifies the name of this PayloadProcessorProfile
	Name string `json:"name"`

	// +required
	// +kubebuilder:validation:Required
	// Plugins is the list of plugins for this Profile.
	Plugins *ProfilePlugins `json:"plugins"`
}

func (prof Profile) String() string {
	var parts []string
	parts = append(parts, "Name: "+prof.Name)
	if len(prof.Plugins.Request) > 0 || len(prof.Plugins.Response) > 0 {
		plugins := ""
		if len(prof.Plugins.Request) > 0 {
			plugins += fmt.Sprintf("Request: %v", prof.Plugins.Request)
		}
		if len(prof.Plugins.Response) > 0 {
			if len(plugins) > 0 {
				plugins += ", "
			}
			plugins += fmt.Sprintf("Response: %v", prof.Plugins.Response)
		}
		parts = append(parts, fmt.Sprintf("Plugins: {%s}", plugins))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// ProfilePlugins lists the set of references to instantiated plugins that will
// be used for request and response processing respectively.
// Plugins in the Request list may be RequestProcessor plugins or model-selector plugins
// (Filter, Scorer, Picker); the loader routes each entry by the interface it implements.
type ProfilePlugins struct {
	// +optional
	// Request is an optional ordered list of references to plugins
	// that will process incoming requests before they are sent for inferencing.
	// May include RequestProcessor plugins as well as model-selector plugins
	// (Filter, Scorer with an optional weight, Picker).
	Request []PluginRef `json:"request"`

	// +optional
	// Response is an optional ordered list of references to plugins
	// that will process the responses of incoming requests after they are
	// sent for inferencing
	Response []PluginRef `json:"response"`
}

// PluginRef is a reference to an instantiated plugin by name.
type PluginRef struct {
	// +required
	// +kubebuilder:validation:Required
	// PluginRef specifies a particular Plugin instance. The reference
	// is to the name of an entry of the Plugins defined in the
	// configuration's Plugins section.
	PluginRef string `json:"pluginRef"`

	// +optional
	// Weight is the weight to be used if this plugin is a Scorer.
	Weight *float64 `json:"weight"`
}

func (pr PluginRef) String() string {
	if pr.Weight != nil {
		return fmt.Sprintf("{PluginRef: %s, Weight: %f}", pr.PluginRef, *pr.Weight)
	}
	return fmt.Sprintf("{PluginRef: %s}", pr.PluginRef)
}
