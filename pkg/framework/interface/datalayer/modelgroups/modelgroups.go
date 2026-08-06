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

// Package modelgroups defines the shared attribute key and Cloneable type used
// to attach group membership to a Model in the datalayer. Producers (the
// model-config-datasource plugin) publish a model's group names here; consumers
// (the model-group-name-filter) read the same attribute to resolve "auto/<group>"
// selectors, so the storage contract has a single source of truth.
package modelgroups

import (
	"slices"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
)

// GroupsAttributeKey is the AttributeMap key under which a model's Groups is
// stored. A model with no group memberships does not have this attribute set.
const GroupsAttributeKey = "model_groups"

// Groups is a Cloneable list of group names a model belongs to. It is stored
// in the Model's AttributeMap under GroupsAttributeKey.
type Groups []string

// Clone implements datalayer.Cloneable.
func (g Groups) Clone() datalayer.Cloneable {
	c := make(Groups, len(g))
	copy(c, g)
	return c
}

// Contains reports whether name appears in g.
func (g Groups) Contains(name string) bool {
	return slices.Contains(g, name)
}
