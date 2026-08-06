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

// --- Valid Configurations ---

// successConfigText represents a fully populated, valid configuration.
// It uses a mix of explicit names and type-derived names.
const successConfigText = `
apiVersion: llm-d.ai/v1alpha1
kind: PayloadProcessorConfig
plugins:
- type: test-request-processor
- type: test-response-processor
- name: test1
  type: test-plugin
  parameters:
    threshold: 10
- type: test-scorer
  parameters:
    cost: 42
- name: testPicker
  type: test-picker
`

const successConfigWithProfileText = `
apiVersion: llm-d.ai/v1alpha1
kind: PayloadProcessorConfig
plugins:
- type: test-profile-picker
- type: test-request-processor
- type: test-response-processor
profilePicker:
  pluginRef: test-profile-picker
profiles:
- name: default
  plugins:
    request:
    - pluginRef: test-request-processor
    response:
    - pluginRef: test-response-processor
`

const successConfigWithTwoProfilesText = `
apiVersion: llm-d.ai/v1alpha1
kind: PayloadProcessorConfig
plugins:
- type: test-profile-picker
- type: test-request-processor
- type: test-response-processor
profilePicker:
  pluginRef: test-profile-picker
profiles:
- name: one
  plugins:
    request:
    - pluginRef: test-request-processor
- name: two
  plugins:
    response:
    - pluginRef: test-response-processor
`

const successConfigWithNoProfilePickerText = `
apiVersion: llm-d.ai/v1alpha1
kind: PayloadProcessorConfig
plugins:
- type: test-request-processor
- type: test-response-processor
profiles:
- name: default
  plugins:
    request:
    - pluginRef: test-request-processor
    response:
    - pluginRef: test-response-processor
`
const successConfigWithProfilePickerNotReferencedText = `
apiVersion: llm-d.ai/v1alpha1
kind: PayloadProcessorConfig
plugins:
- type: test-profile-picker
- type: test-request-processor
- type: test-response-processor
profiles:
- name: default
  plugins:
    request:
    - pluginRef: test-request-processor
    response:
    - pluginRef: test-response-processor
`

// successConfigPreAndPostProcessorsText represents both pre-processors and post-processors.
const successConfigPreAndPostProcessorsText = `
apiVersion: llm-d.ai/v1alpha1
kind: PayloadProcessorConfig
plugins:
- type: test-request-processor
- name: pre-processor-2
  type: test-request-processor
- type: test-response-processor
preProcessing:
  plugins:
  - pluginRef: pre-processor-2
  - pluginRef: test-request-processor
postProcessing:
  plugins:
  - pluginRef: test-response-processor
`

// successConfigPreProcessorsOnlyText represents only pre-processors.
const successConfigPreProcessorsOnlyText = `
apiVersion: llm-d.ai/v1alpha1
kind: PayloadProcessorConfig
plugins:
- type: test-request-processor
- name: pre-processor-2
  type: test-request-processor
- type: test-response-processor
preProcessing:
  plugins:
  - pluginRef: pre-processor-2
  - pluginRef: test-request-processor
postProcessing:
  plugins:
`

// successConfigPostProcessorsOnlyText represents only post-processors.
const successConfigPostProcessorsOnlyText = `
apiVersion: llm-d.ai/v1alpha1
kind: PayloadProcessorConfig
plugins:
- type: test-request-processor
- name: pre-processor-2
  type: test-request-processor
- type: test-response-processor
preProcessing:
postProcessing:
  plugins:
  - pluginRef: test-response-processor
`

// datalayerSuccessConfigText has valid datalayer references across all three categories.
const datalayerSuccessConfigText = `
apiVersion: llm-d.ai/v1alpha1
kind: PayloadProcessorConfig
plugins:
- name: test-col
  type: test-collector
- name: test-ext
  type: test-extractor
- name: test-ds
  type: test-datasource
datalayer:
  collectors:
  - pluginRef: test-col
  extractors:
  - pluginRef: test-ext
  datasources:
  - pluginRef: test-ds
`

// datalayerMissingRefConfigText references a plugin that does not exist.
const datalayerMissingRefConfigText = `
apiVersion: llm-d.ai/v1alpha1
kind: PayloadProcessorConfig
plugins:
- name: test1
  type: test-plugin
  parameters:
    threshold: 10
datalayer:
  extractors:
  - pluginRef: does-not-exist
`

// modelSelectorAllPluginTypesText wires a Filter, a weighted Scorer, and a Picker
// alongside a model-selector RequestProcessor plugin in the same profile.
const modelSelectorAllPluginTypesText = `
apiVersion: llm-d.ai/v1alpha1
kind: PayloadProcessorConfig
plugins:
- type: model-selector
- type: test-filter
- type: cost-scorer
- type: max-score-picker
profiles:
- name: default
  plugins:
    request:
    - pluginRef: model-selector
    - pluginRef: test-filter
    - pluginRef: cost-scorer
      weight: 2.5
    - pluginRef: max-score-picker
`

// --- Invalid Configurations (Syntax/Structure) ---

// errorBadYamlText contains invalid YAML syntax.
const errorBadYamlText = `
apiVersion: llm-d.ai/v1alpha1
kind: PayloadProcessorConfig
plugins:
- testing 1 2 3
`

// errorBadPluginReferenceText is missing the required 'type' field.
const errorBadPluginReferenceText = `
apiVersion: llm-d.ai/v1alpha1
kind: PayloadProcessorConfig
plugins:
- parameters:
    a: 1234
`

// errorBadPluginReferencePluginText references a plugin type that does not exist in the registry.
const errorBadPluginReferencePluginText = `
apiVersion: llm-d.ai/v1alpha1
kind: PayloadProcessorConfig
plugins:
- name: testx
  type: unknown-plugin-type
- name: profileHandler
  type: test-profile-handler
`

// errorBadPluginJSONText has invalid JSON in parameters (string where int expected).
const errorBadPluginJSONText = `
apiVersion: llm-d.ai/v1alpha1
kind: PayloadProcessorConfig
plugins:
- name: testScorer
  type: test-scorer
  parameters:
    cost: asdf
`

// errorDuplicatePluginText defines the same plugin name twice.
const errorDuplicatePluginText = `
apiVersion: llm-d.ai/v1alpha1
kind: PayloadProcessorConfig
plugins:
- name: test1
  type: test-plugin
  parameters:
    threshold: 10
- name: test1
  type: test-plugin
  parameters:
    threshold: 20
`

const errorConfigWithTwoProfilesNoPickerText = `
apiVersion: llm-d.ai/v1alpha1
kind: PayloadProcessorConfig
plugins:
- type: test-request-processor
- type: test-response-processor
profiles:
- name: one
  plugins:
    request:
    - pluginRef: test-request-processor
- name: two
  plugins:
    response:
    - pluginRef: test-response-processor
`

// modelSelectorScorerMissingWeightText is an error case: scorer without a weight.
const modelSelectorScorerMissingWeightText = `
apiVersion: llm-d.ai/v1alpha1
kind: PayloadProcessorConfig
plugins:
- type: model-selector
- type: cost-scorer
profiles:
- name: default
  plugins:
    request:
    - pluginRef: model-selector
    - pluginRef: cost-scorer
`

// modelSelectorUnknownPluginTypeText is an error case: plugin that is none of the accepted interfaces.
const modelSelectorUnknownPluginTypeText = `
apiVersion: llm-d.ai/v1alpha1
kind: PayloadProcessorConfig
plugins:
- type: model-selector
- name: bare-plugin
  type: test-plugin
  parameters:
    threshold: 1
profiles:
- name: default
  plugins:
    request:
    - pluginRef: model-selector
    - pluginRef: bare-plugin
`

// errorBadPreProcessorsText represents a reference to a plugin that is not a RequestProcessor
const errorBadPreProcessorsText = `
apiVersion: llm-d.ai/v1alpha1
kind: PayloadProcessorConfig
plugins:
- type: test-plugin
preProcessing:
  plugins:
  - pluginRef: test-plugin
`

// errorBadPostProcessorsText represents a reference to a plugin that is not a ResponseProcessor
const errorBadPostProcessorsText = `
apiVersion: llm-d.ai/v1alpha1
kind: PayloadProcessorConfig
plugins:
- type: test-plugin
postProcessing:
  plugins:
  - pluginRef: test-plugin
`

// errorMissingPreProcessorsText represents a reference to an unknown pre-processor
const errorMissingPreProcessorsText = `
apiVersion: llm-d.ai/v1alpha1
kind: PayloadProcessorConfig
plugins:
- type: test-request-processor
preProcessing:
  plugins:
  - pluginRef: test-pre-processor
`

// errorMissingPostProcessorsText represents a reference to an unknown post-processor
const errorMissingPostProcessorsText = `
apiVersion: llm-d.ai/v1alpha1
kind: PayloadProcessorConfig
plugins:
- type: test-response-processor
postProcessing:
  plugins:
  - pluginRef: test-post-processor
`
