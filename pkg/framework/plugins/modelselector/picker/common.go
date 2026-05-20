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

package picker

import (
	"math/rand/v2"
	"sync"
	"time"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/modelselector"
)

// PickerRand is a thread-safe random number generator shared by all pickers.
var PickerRand = newLockedRand()

type lockedRand struct {
	mu   sync.Mutex
	rand *rand.Rand
}

func newLockedRand() *lockedRand {
	seed := uint64(time.Now().UnixNano())

	return &lockedRand{
		rand: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)),
	}
}

func (r *lockedRand) Float64() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.rand.Float64()
}

func (r *lockedRand) Shuffle(n int, swap func(i, j int)) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.rand.Shuffle(n, swap)
}

// ShuffleScoredModels randomizes the order of the given scored models in-place.
func ShuffleScoredModels(scoredModels []*modelselector.ScoredModel) {
	PickerRand.Shuffle(len(scoredModels), func(i, j int) {
		scoredModels[i], scoredModels[j] = scoredModels[j], scoredModels[i]
	})
}
