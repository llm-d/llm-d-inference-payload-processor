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

// Package costguard implements a cost-minimising scorer for the ModelSelector
// framework. It routes inference to the model with the lowest observed cost,
// using an epsilon-greedy Multi-Arm Bandit strategy over a risk-aware rank
// that combines each model's body cost (trimmed mean) and Conditional Tail
// Expectation of the cost distribution. See the package README.md for the
// full algorithm and configuration reference.
package costguard

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"sort"

	"github.com/caio/go-tdigest/v5"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer/accumulator"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/modelselector"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
)

const (
	// PluginType is the identifier used when registering this scorer.
	PluginType = "costguard"

	// z95 is the standard-normal 95% quantile used in sampleThreshold.
	z95 = 1.96

	// neutralScore is returned for any model that the scorer has no strong opinion
	// on (missing / empty / under-explored cost data, degenerate sigmoid
	// inputs). It composes with other scorers in the pipeline without pushing
	// selection either way.
	neutralScore = 0.5

	defaultEpsilon               = 0.1
	defaultAlpha                 = 0.95
	defaultLambda                = 1.0
	defaultPercentileMarginError = 0.03
)

// compile-time interface assertion
var _ modelselector.Scorer = &CostGuardScorer{}

// Config defines the JSON configuration for the plugin. All fields have
// sensible defaults, so an empty config is valid.
type Config struct {
	// Epsilon is the probability of random exploration per request: with
	// probability epsilon Score picks uniformly (preferring under-explored
	// models when any exist) instead of ranking on the cost digests. Must be
	// in [0, 1]. Defaults to 0.1.
	Epsilon float64 `json:"epsilon"`

	// Alpha is the quantile that separates the body of the cost distribution
	// from the tail. Must be in (0, 1). Defaults to 0.95.
	Alpha float64 `json:"alpha"`

	// Lambda is the penalty weight applied to the tail cost contribution
	// (Conditional Tail Expectation). Must be >= 0. Defaults to 1.0.
	Lambda float64 `json:"lambda"`

	// PercentileMarginError (w) controls how tightly the empirical alpha-
	// percentile threshold captures the true probability mass. With the
	// defaults (alpha=0.95, w=0.03), at 95% confidence the observed p95
	// threshold covers true probability mass in [alpha-w, alpha+w] — i.e.
	// falls between p92 and p98 of the underlying distribution.
	// Defaults to 0.03. Smaller values require quadratically more samples.
	PercentileMarginError float64 `json:"percentileMarginError"`
}

// CostGuardScorer consumes per-model cost samples (published by the
// requestcostmetadata extractor) and scores candidate models to minimise
// typical cost and the risk of expensive-tail responses.
type CostGuardScorer struct {
	typedName plugin.TypedName
	epsilon   float64
	alpha     float64
	lambda    float64
	// sampleThreshold is the minimum sample count for the empirical
	// alpha-percentile threshold to cover the true probability mass to within
	// +/-percentileMarginError at a 95% confidence level, derived from the
	// Wald margin-of-error formula for a proportion.
	sampleThreshold uint64
}

// ScorerFactory validates rawParameters into a Config and constructs a scorer.
func ScorerFactory(name string, parameters *json.Decoder, _ plugin.Handle) (plugin.Plugin, error) {
	config := Config{
		Epsilon:               defaultEpsilon,
		Alpha:                 defaultAlpha,
		Lambda:                defaultLambda,
		PercentileMarginError: defaultPercentileMarginError,
	}
	if parameters != nil {
		if err := parameters.Decode(&config); err != nil {
			return nil, fmt.Errorf("costguard %q: failed to parse parameters: %w", name, err)
		}
	}

	if config.Epsilon < 0 || config.Epsilon > 1 {
		return nil, fmt.Errorf("costguard %q: epsilon must be in [0, 1], got %f", name, config.Epsilon)
	}
	if config.Alpha <= 0 || config.Alpha >= 1 {
		return nil, fmt.Errorf("costguard %q: alpha must be in (0, 1), got %f", name, config.Alpha)
	}
	if config.Lambda < 0 {
		return nil, fmt.Errorf("costguard %q: lambda must be >= 0, got %f", name, config.Lambda)
	}
	if config.PercentileMarginError <= 0 || config.PercentileMarginError >= 1 {
		return nil, fmt.Errorf("costguard %q: percentileMarginError must be in (0, 1), got %f", name, config.PercentileMarginError)
	}

	return NewCostGuardScorer(
		config.Epsilon,
		config.Alpha,
		config.Lambda,
		config.PercentileMarginError,
	).WithName(name), nil
}

// NewCostGuardScorer constructs a scorer with the given parameters. No
// validation — prefer ScorerFactory for JSON-configured construction.
func NewCostGuardScorer(epsilon, alpha, lambda, percentileMarginError float64) *CostGuardScorer {
	// Wald CI sample-size formula
	threshold := uint64(math.Ceil(z95 * z95 * alpha * (1 - alpha) / (percentileMarginError * percentileMarginError)))
	return &CostGuardScorer{
		typedName:       plugin.TypedName{Type: PluginType, Name: PluginType},
		epsilon:         epsilon,
		alpha:           alpha,
		lambda:          lambda,
		sampleThreshold: threshold,
	}
}

// TypedName returns the typed name of the plugin instance.
func (s *CostGuardScorer) TypedName() plugin.TypedName {
	return s.typedName
}

// WithName sets the instance name.
func (s *CostGuardScorer) WithName(name string) *CostGuardScorer {
	s.typedName.Name = name
	return s
}

// Score returns a score in [0, 1] per model. With probability epsilon Score
// takes the explore branch (uniform pick, preferring under-explored models
// when any exist; the pick receives 1.0, everything else neutralScore).
// Otherwise it takes the exploit branch: explored models are ranked as
// TrimmedMean(0, alpha) + lambda*CTE(alpha), where CTE is the discrete
// Conditional Tail Expectation (tail mean above alpha). Under-explored,
// missing, or malformed digests yield neutralScore. Ranks map to scores via
// a sigmoid centred at the median.
func (s *CostGuardScorer) Score(_ context.Context, _ *requesthandling.InferenceRequest, models []datalayer.Model) map[datalayer.Model]float64 {
	if len(models) == 0 {
		return map[datalayer.Model]float64{}
	}
	if rand.Float64() < s.epsilon {
		return s.explore(models)
	}
	return s.exploit(models)
}

// exploit ranks explored models by TrimmedMean + lambda*CTE and maps ranks
// through a self-calibrating sigmoid centred at the median. Under-explored
// models and every model in a degenerate configuration (fewer than two
// explored models, or all identical ranks) receive neutralScore.
func (s *CostGuardScorer) exploit(models []datalayer.Model) map[datalayer.Model]float64 {
	scores := make(map[datalayer.Model]float64, len(models))

	// Partition by sampleThreshold; a nil digest is treated as under-explored.
	explored := make([]datalayer.Model, 0, len(models))
	digests := make([]*tdigest.TDigest, 0, len(models))
	for _, m := range models {
		d := lookupDigest(m)
		if d == nil || d.Count() < s.sampleThreshold {
			scores[m] = neutralScore
			continue
		}
		explored = append(explored, m)
		digests = append(digests, d)
	}

	// Need >= 2 explored to compute a sigmoid; otherwise every model is neutral.
	if len(explored) < 2 {
		for _, m := range explored {
			scores[m] = neutralScore
		}
		return scores
	}

	// Rank = TrimmedMean(0, alpha) + lambda * TrimmedMean(alpha, 1). The tail
	// term is the discrete Conditional Tail Expectation (CTE). Lower rank is better.
	ranks := make([]float64, len(explored))
	for i, d := range digests {
		body := d.TrimmedMean(0, s.alpha)
		tail := d.TrimmedMean(s.alpha, 1)
		ranks[i] = body + s.lambda*tail
	}

	// Self-calibrating sigmoid; identical ranks (sigma == 0) collapse to neutral.
	med := median(ranks)
	sigma := stddevPop(ranks)
	if sigma == 0 {
		for _, m := range explored {
			scores[m] = neutralScore
		}
		return scores
	}
	beta := 1 / sigma
	for i, m := range explored {
		scores[m] = sigmoid(beta * (ranks[i] - med))
	}
	return scores
}

// explore picks one model uniformly at random and gives it score 1.0; every
// other model receives neutralScore. If under-explored models (nil digest or
// count below sampleThreshold) exist, they are preferred.
// When every model is explored, the candidate pool becomes all models.
func (s *CostGuardScorer) explore(models []datalayer.Model) map[datalayer.Model]float64 {
	underExplored := make([]datalayer.Model, 0, len(models))
	for _, m := range models {
		d := lookupDigest(m)
		if d == nil || d.Count() < s.sampleThreshold {
			underExplored = append(underExplored, m)
		}
	}
	if len(underExplored) == 0 {
		underExplored = models
	}
	pick := underExplored[rand.IntN(len(underExplored))]

	scores := make(map[datalayer.Model]float64, len(models))
	for _, m := range models {
		scores[m] = neutralScore
	}
	scores[pick] = 1.0
	return scores
}

// sigmoid maps a centred rank to a score in (0, 1). Sign is negated inline
// (1/(1+exp(x)) == 1/(1+exp(-(-x)))) so that lower rank yields a higher score.
func sigmoid(x float64) float64 {
	return 1 / (1 + math.Exp(x))
}

// median returns the median of ranks. Precondition: len(ranks) > 0.
// The input slice is not mutated.
func median(ranks []float64) float64 {
	c := slices.Clone(ranks)
	sort.Float64s(c)
	n := len(c)
	if n%2 == 1 {
		return c[n/2]
	}
	return (c[n/2-1] + c[n/2]) / 2
}

// stddevPop returns the population standard deviation of ranks (divisor N).
// Ranks enumerate every candidate model in the current scoring call, so the
// set is the population. Returns 0 when len(ranks) < 2.
func stddevPop(ranks []float64) float64 {
	if len(ranks) < 2 {
		return 0
	}
	var mean float64
	for _, v := range ranks {
		mean += v
	}
	mean /= float64(len(ranks))
	var ss float64
	for _, v := range ranks {
		diff := v - mean
		ss += diff * diff
	}
	return math.Sqrt(ss / float64(len(ranks)))
}

// lookupDigest fetches the *tdigest.TDigest inside m's *accumulator.CostDigest
// stored under accumulator.CostDigestAttributeKey. Returns nil if
// ReadAttributeKey reports the attribute missing or of the wrong type;
// callers treat nil the same as an under-explored model.
//
// Note: a nil inner Digest is unreachable here because ReadAttributeKey
// returns a Clone of the stored value, and CostDigest.Clone dereferences
// its inner Digest. Thus a nil inner Digest would panic in CostDigest.Clone
// before this function is called.
func lookupDigest(m datalayer.Model) *tdigest.TDigest {
	cd, err := datalayer.ReadAttributeKey[*accumulator.CostDigest](m.GetAttributes(), accumulator.CostDigestAttributeKey)
	if err != nil {
		return nil
	}
	return cd.Digest
}
