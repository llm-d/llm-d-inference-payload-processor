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

package datalayer

import "encoding/json"

type Model interface {
	GetName() string
	GetAttributes() AttributeMap
}

// compile-time type validation
var _ Model = &model{}

func NewModel(name string) Model {
	return &model{
		name:       name,
		attributes: NewAttributes(),
	}
}

type model struct {
	name       string
	attributes AttributeMap
}

func (m *model) GetName() string {
	return m.name
}

func (m *model) GetAttributes() AttributeMap {
	return m.attributes
}

// MarshalJSON projects the unexported name field to an exported JSON key
// so a Model is visible in structured logs.
// Attributes are intentionally omitted because
// AttributeMap is itself an interface backed by an unexported sync.Map
func (m *model) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Name string `json:"name"`
	}{Name: m.name})
}
