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

// datalayerSuccessConfigText has a valid notification-source reference.
const datalayerSuccessConfigText = `
apiVersion: llm-d.ai/v1alpha1
kind: PayloadProcessorConfig
plugins:
- name: my-notif-source
  type: notification-source
notificationSources:
- pluginRef: my-notif-source
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
notificationSources:
- pluginRef: does-not-exist
`

// datalayerWrongTypeConfigText references a plugin that is not a NotificationSource.
const datalayerWrongTypeConfigText = `
apiVersion: llm-d.ai/v1alpha1
kind: PayloadProcessorConfig
plugins:
- name: test1
  type: test-plugin
  parameters:
    threshold: 10
notificationSources:
- pluginRef: test1
`
