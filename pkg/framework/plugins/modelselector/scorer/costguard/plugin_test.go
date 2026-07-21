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

package costguard

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/caio/go-tdigest/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer/accumulator"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
)

func newTestScorer(t *testing.T) *CostGuardScorer {
	t.Helper()
	p, err := ScorerFactory("test-cg", nil, nil)
	require.NoError(t, err)
	s, ok := p.(*CostGuardScorer)
	require.True(t, ok)
	return s
}

// newCostDigestN builds a *accumulator.CostDigest containing n copies of v
// (via AddWeighted). Used to cheaply cross the sampleThreshold in tests
// that don't care about distribution shape.
func newCostDigestN(t *testing.T, v float64, n uint64) *accumulator.CostDigest {
	t.Helper()
	d, err := tdigest.New()
	require.NoError(t, err)
	require.NoError(t, d.AddWeighted(v, n))
	return &accumulator.CostDigest{Digest: d}
}

// modelWithDigest returns a Model whose AttributeMap holds cd under
// CostDigestAttributeKey.
func modelWithDigest(t *testing.T, name string, cd *accumulator.CostDigest) datalayer.Model {
	t.Helper()
	m := datalayer.NewModel(name)
	m.GetAttributes().Put(accumulator.CostDigestAttributeKey, cd)
	return m
}

// TestScore_AllNeutral covers cases where Score should return neutral for
// every input model (or the empty map when there are no models):
//   - "empty-input": zero models — Score returns an empty map.
//   - "single-well-explored": one over-threshold model — the len(explored)
//     < 2 guard collapses it to neutralScore regardless of digest quality.
//   - "single-explored-plus-under-explored": the len(explored) < 2 guard —
//     with one explored model there is no meaningful sigmoid to compute.
//   - "identical-ranks": the sigma == 0 guard — when all explored models
//     have identical rank the sigmoid degenerates.
func TestScore_AllNeutral(t *testing.T) {
	s := newTestScorer(t)
	under := s.sampleThreshold - 1
	over := s.sampleThreshold + 50

	type modelSpec struct {
		name  string
		cost  float64
		count uint64
	}
	tests := []struct {
		name   string
		models []modelSpec
	}{
		{
			name:   "empty-input",
			models: nil,
		},
		{
			name: "single-well-explored",
			models: []modelSpec{
				{"only", 42.0, over},
			},
		},
		{
			name: "single-explored-plus-under-explored",
			models: []modelSpec{
				{"explored", 1.0, over},
				{"under1", 2.0, under},
				{"under2", 3.0, under},
			},
		},
		{
			name: "identical-ranks",
			models: []modelSpec{
				{"m1", 5.0, over},
				{"m2", 5.0, over},
				{"m3", 5.0, over},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			models := make([]datalayer.Model, len(tt.models))
			for i, spec := range tt.models {
				models[i] = modelWithDigest(t, spec.name, newCostDigestN(t, spec.cost, spec.count))
			}
			scores := s.Score(context.Background(), plugin.NewCycleState(), requesthandling.NewInferenceRequest(), models)
			require.Len(t, scores, len(models))
			for _, m := range models {
				assert.Equal(t, neutralScore, scores[m])
			}
		})
	}
}

// TestScore_TwoDistinctRanks verifies the two-model exploit path: the
// cheaper model scores above 0.5, the more expensive below, and the two
// scores are symmetric around 0.5 (sum to 1.0).
func TestScore_TwoDistinctRanks(t *testing.T) {
	s := newTestScorer(t)
	overCount := s.sampleThreshold + 50
	cheap := modelWithDigest(t, "cheap", newCostDigestN(t, 1.0, overCount))
	expensive := modelWithDigest(t, "expensive", newCostDigestN(t, 3.0, overCount))
	models := []datalayer.Model{cheap, expensive}
	scores := s.Score(context.Background(), plugin.NewCycleState(), requesthandling.NewInferenceRequest(), models)
	require.Len(t, scores, len(models))
	assert.Greater(t, scores[cheap], 0.5, "cheaper model should score above neutral")
	assert.Less(t, scores[expensive], 0.5, "more expensive model should score below neutral")
	// Algebraic identity: with n=2 the ranks straddle the median symmetrically,
	// so the sigmoid arguments are equal-and-opposite. sigmoid(x)+sigmoid(-x)=1
	// for any x (1/(1+e^x)+1/(1+e^-x)=1), so the sum is exactly 1.
	assert.InDelta(t, 1.0, scores[cheap]+scores[expensive], 1e-9, "two-model scores must be symmetric around 0.5 by construction")
}

// TestScore_ThreeDistinctRanks verifies the three-model exploit path:
// - the median-rank model scores exactly 0.5 (sigmoid(0)),
// - the cheapest scores highest and above the median-rank model,
// - the most expensive scores lowest and below the median-rank model.
// The symmetric-outer-scores property is covered by TestScore_TwoDistinctRanks.
func TestScore_ThreeDistinctRanks(t *testing.T) {
	s := newTestScorer(t)
	overCount := s.sampleThreshold + 50
	cheap := modelWithDigest(t, "cheap", newCostDigestN(t, 1.0, overCount))
	mid := modelWithDigest(t, "mid", newCostDigestN(t, 2.0, overCount))
	expensive := modelWithDigest(t, "expensive", newCostDigestN(t, 3.0, overCount))
	models := []datalayer.Model{cheap, mid, expensive}
	scores := s.Score(context.Background(), plugin.NewCycleState(), requesthandling.NewInferenceRequest(), models)
	require.Len(t, scores, len(models))
	assert.InDelta(t, 0.5, scores[mid], 1e-9, "median-rank model should score exactly neutral")
	assert.Greater(t, scores[cheap], scores[mid])
	assert.Less(t, scores[expensive], scores[mid])
}

// TestScore_SelfCalibratesToScale asserts that the sigmoid is invariant
// under uniform scaling of the ranks. Two fixtures with the same relative
// geometry but wildly different absolute scale — costs (1, 2, 3) versus
// (1000, 2000, 3000) — must produce identical score distributions.
//
// This pins the beta = 1/stddevPop calibration: the score function depends
// only on each rank's normalised distance from the median, not on absolute scale.
func TestScore_SelfCalibratesToScale(t *testing.T) {
	s := newTestScorer(t)
	overCount := s.sampleThreshold + 50

	// Small-scale fixture.
	smallCheap := modelWithDigest(t, "small-cheap", newCostDigestN(t, 1.0, overCount))
	smallMid := modelWithDigest(t, "small-mid", newCostDigestN(t, 2.0, overCount))
	smallExpensive := modelWithDigest(t, "small-expensive", newCostDigestN(t, 3.0, overCount))
	smallScores := s.Score(context.Background(), plugin.NewCycleState(), requesthandling.NewInferenceRequest(),
		[]datalayer.Model{smallCheap, smallMid, smallExpensive})

	// Large-scale fixture — same relative geometry, costs scaled by 1000.
	largeCheap := modelWithDigest(t, "large-cheap", newCostDigestN(t, 1000.0, overCount))
	largeMid := modelWithDigest(t, "large-mid", newCostDigestN(t, 2000.0, overCount))
	largeExpensive := modelWithDigest(t, "large-expensive", newCostDigestN(t, 3000.0, overCount))
	largeScores := s.Score(context.Background(), plugin.NewCycleState(), requesthandling.NewInferenceRequest(),
		[]datalayer.Model{largeCheap, largeMid, largeExpensive})

	assert.InDelta(t, smallScores[smallCheap], largeScores[largeCheap], 1e-9)
	assert.InDelta(t, smallScores[smallMid], largeScores[largeMid], 1e-9)
	assert.InDelta(t, smallScores[smallExpensive], largeScores[largeExpensive], 1e-9)
}

// TestScore_UnevenSpread asserts that when one rank is a far outlier, its
// score saturates near the sigmoid extreme while the inner ranks stay near
// neutral. Fixture costs (1.0, 2.0, 100.0) put the expensive model at ~49x
// the distance from the median compared to the cheap model, so its
// normalised beta*(rank - median) is deep in the sigmoid tail.
//
// This complements TestScore_SelfCalibratesToScale by pinning geometry-
// dependent behavior: the shape of the score distribution reflects the
// relative geometry of the ranks, even though it is invariant to scale.
func TestScore_UnevenSpread(t *testing.T) {
	s := newTestScorer(t)
	overCount := s.sampleThreshold + 50
	cheap := modelWithDigest(t, "cheap", newCostDigestN(t, 1.0, overCount))
	mid := modelWithDigest(t, "mid", newCostDigestN(t, 2.0, overCount))
	expensive := modelWithDigest(t, "expensive", newCostDigestN(t, 100.0, overCount))
	scores := s.Score(context.Background(), plugin.NewCycleState(), requesthandling.NewInferenceRequest(),
		[]datalayer.Model{cheap, mid, expensive})
	require.Len(t, scores, 3)
	assert.InDelta(t, 0.5, scores[mid], 1e-9, "median-rank model still scores exactly neutral")
	assert.Greater(t, scores[cheap], scores[mid], "cheap ranks below median so scores above neutral")
	assert.Less(t, scores[expensive], 0.15, "expensive is a far outlier and saturates the sigmoid tail")
	assert.Less(t, scores[cheap], 0.55, "cheap is only slightly below median so its score barely exceeds neutral")
}

// TestScore_UnderExplored verifies the two paths that classify a model as
// under-explored — the lookupDigest nil-digest branch (missing attribute)
// and the sampleThreshold gate (count below threshold).
// There are two contexts:
//   - "alone": the under-explored model is the only input; the len(explored)
//     < 2 guard collapses it to neutralScore.
//   - "with-explored-pair": two well-explored models coexist with the
//     under-explored one; the under-explored model still scores neutral,
//     and the two explored models are scored on the exploit path exactly
//     as they would be alone (symmetric around 0.5).
func TestScore_UnderExplored(t *testing.T) {
	s := newTestScorer(t)
	over := s.sampleThreshold + 50
	under := s.sampleThreshold - 1

	underVariants := []struct {
		name  string
		build func(t *testing.T) datalayer.Model
	}{
		{
			"missing cost_digest attribute",
			func(t *testing.T) datalayer.Model { return datalayer.NewModel("under") },
		},
		{
			"count below sampleThreshold",
			func(t *testing.T) datalayer.Model {
				return modelWithDigest(t, "under", newCostDigestN(t, 2.0, under))
			},
		},
	}
	contexts := []struct {
		name         string
		withExplored bool
	}{
		{"alone", false},
		{"with-explored-pair", true},
	}
	for _, uv := range underVariants {
		for _, ctx := range contexts {
			t.Run(uv.name+"/"+ctx.name, func(t *testing.T) {
				u := uv.build(t)
				models := []datalayer.Model{u}
				var cheap, expensive datalayer.Model
				if ctx.withExplored {
					cheap = modelWithDigest(t, "cheap", newCostDigestN(t, 1.0, over))
					expensive = modelWithDigest(t, "expensive", newCostDigestN(t, 3.0, over))
					models = []datalayer.Model{cheap, expensive, u}
				}
				scores := s.Score(context.Background(), plugin.NewCycleState(), requesthandling.NewInferenceRequest(), models)
				require.Len(t, scores, len(models))
				assert.Equal(t, neutralScore, scores[u])
				if ctx.withExplored {
					assert.Greater(t, scores[cheap], 0.5)
					assert.Less(t, scores[expensive], 0.5)
					assert.InDelta(t, 1.0, scores[cheap]+scores[expensive], 1e-9)
				}
			})
		}
	}
}

// TestFactory_DefaultConfig verifies factory behavior with nil parameters and
// with an empty JSON object; both must produce the same defaulted scorer.
func TestFactory_DefaultConfig(t *testing.T) {
	tests := []struct {
		name string
		raw  json.RawMessage
	}{
		{"nil parameters", nil},
		{"empty object", json.RawMessage(`{}`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := ScorerFactory("test-cg", tt.raw, nil)
			require.NoError(t, err)
			s, ok := p.(*CostGuardScorer)
			require.True(t, ok)
			assert.Equal(t, defaultEpsilon, s.epsilon)
			assert.Equal(t, defaultAlpha, s.alpha)
			assert.Equal(t, defaultLambda, s.lambda)
			assert.Equal(t, 2*time.Hour, s.windowDuration)
			// Wald CI at defaults (alpha=0.95, w=0.03) — see README table.
			assert.Equal(t, uint64(203), s.sampleThreshold)
			assert.Equal(t, PluginType, s.TypedName().Type)
			assert.Equal(t, "test-cg", s.TypedName().Name)
		})
	}
}

// TestFactory_CustomConfig verifies that custom parameters override defaults.
func TestFactory_CustomConfig(t *testing.T) {
	raw := json.RawMessage(`{"epsilon":0.2,"alpha":0.9,"lambda":2.0,"windowDuration":"30m","percentileMarginError":0.05}`)
	p, err := ScorerFactory("custom", raw, nil)
	require.NoError(t, err)
	s := p.(*CostGuardScorer)
	assert.Equal(t, 0.2, s.epsilon)
	assert.Equal(t, 0.9, s.alpha)
	assert.Equal(t, 2.0, s.lambda)
	assert.Equal(t, 30*time.Minute, s.windowDuration)
	// Wald CI at alpha=0.9, w=0.05: ceil(1.96^2 * 0.09 / 0.0025) = 139.
	assert.Equal(t, uint64(139), s.sampleThreshold)
}

// TestFactory_ValidationErrors verifies that each out-of-range or malformed
// parameter causes the factory to return an error.
func TestFactory_ValidationErrors(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"malformed json", `{invalid`},
		{"epsilon below 0", `{"epsilon":-0.1}`},
		{"epsilon above 1", `{"epsilon":1.1}`},
		{"alpha zero", `{"alpha":0}`},
		{"alpha one", `{"alpha":1}`},
		{"alpha above 1", `{"alpha":1.5}`},
		{"lambda negative", `{"lambda":-0.1}`},
		{"pme zero", `{"percentileMarginError":0}`},
		{"pme one", `{"percentileMarginError":1}`},
		{"windowDuration unparsable", `{"windowDuration":"not-a-duration"}`},
		{"windowDuration zero", `{"windowDuration":"0s"}`},
		{"windowDuration negative", `{"windowDuration":"-1s"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ScorerFactory("bad", json.RawMessage(tt.raw), nil)
			require.Error(t, err)
		})
	}
}

// TestFactory_AcceptedBoundaries verifies that the boundary values documented
// as accepted stay accepted; pins down current strict-vs-inclusive semantics
// so a refactor cannot flip a guard without a failing test.
func TestFactory_AcceptedBoundaries(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		check func(*testing.T, *CostGuardScorer)
	}{
		{
			"epsilon at lower bound",
			`{"epsilon":0}`,
			func(t *testing.T, s *CostGuardScorer) { assert.Equal(t, 0.0, s.epsilon) },
		},
		{
			"epsilon at upper bound",
			`{"epsilon":1}`,
			func(t *testing.T, s *CostGuardScorer) { assert.Equal(t, 1.0, s.epsilon) },
		},
		{
			"lambda at lower bound",
			`{"lambda":0}`,
			func(t *testing.T, s *CostGuardScorer) { assert.Equal(t, 0.0, s.lambda) },
		},
		{
			"alpha just inside strict (0, 1)",
			`{"alpha":0.01}`,
			func(t *testing.T, s *CostGuardScorer) { assert.Equal(t, 0.01, s.alpha) },
		},
		{
			"percentileMarginError just inside strict (0, 1)",
			`{"percentileMarginError":0.01}`,
			func(t *testing.T, s *CostGuardScorer) {
				// Formula stays well-defined at the small end.
				assert.Greater(t, s.sampleThreshold, uint64(0))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := ScorerFactory("boundary", json.RawMessage(tt.raw), nil)
			require.NoError(t, err)
			tt.check(t, p.(*CostGuardScorer))
		})
	}
}

func TestWithName(t *testing.T) {
	s := newTestScorer(t)
	result := s.WithName("custom-name")
	assert.Same(t, s, result, "WithName should return the same instance for chaining")
	assert.Equal(t, "custom-name", s.TypedName().Name)
}

// TestMedian verifies median across the branch matrix: odd-length picks
// the middle element, even-length averages the two middle elements,
// single-element returns itself, and unsorted input is sorted internally
// (verifying the slices.Clone + sort.Float64s contract).
func TestMedian(t *testing.T) {
	tests := []struct {
		name string
		in   []float64
		want float64
	}{
		{"single element", []float64{5}, 5},
		{"odd length sorted", []float64{1, 2, 3}, 2},
		{"even length sorted", []float64{1, 2, 3, 4}, 2.5},
		{"unsorted input", []float64{3, 1, 2}, 2},
		{"odd length with duplicates", []float64{7, 7, 7, 7, 7}, 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Snapshot input to verify the slice is not mutated.
			original := append([]float64(nil), tt.in...)
			assert.InDelta(t, tt.want, median(tt.in), 1e-9)
			assert.Equal(t, original, tt.in, "median must not mutate its input")
		})
	}
}

