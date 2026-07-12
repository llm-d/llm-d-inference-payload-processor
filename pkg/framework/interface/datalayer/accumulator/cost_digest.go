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

package accumulator

import (
	"github.com/caio/go-tdigest/v5"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
)

// CostDigestAttributeKey is the AttributeMap key under which a model's
// *CostDigest is stored. Producers (the requestcostmetadata extractor)
// publish a snapshot of the running digest here; consumers (the CostGuard
// scorer and any other cost-aware reader) fetch the snapshot via Get.
const CostDigestAttributeKey = "cost_digest"

// CostDigest is a Cloneable wrapper around a *tdigest.TDigest from
// github.com/caio/go-tdigest/v5, recording the per-request cost
// distribution of a model. It is stored on a Model's AttributeMap under
// CostDigestAttributeKey.
//
// The wrapped digest must be non-nil. Cloning is delegated to the
// library's own Clone, which produces an independent digest with the
// same centroids; per the library docs the RNG state is not cloned.
type CostDigest struct {
	Digest *tdigest.TDigest
}

// Clone implements datalayer.Cloneable. It returns a *CostDigest whose
// inner digest is independent of the original — adding samples to the
// clone does not affect the source, and vice versa.
func (c *CostDigest) Clone() datalayer.Cloneable {
	return &CostDigest{Digest: c.Digest.Clone()}
}
