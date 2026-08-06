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

package modelselector

import (
	"context"
	"fmt"
	"testing"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/modelselector"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
)

func init() {
	// Initialize a discard logger to ensure consistent benchmark results.
	log.SetLogger(logr.Discard())
}

// BenchmarkModelSelectorPipelineRun measures the per-request cost of the
// ModelSelectorPipeline.Run hot path (filter → score → pick) at different
// model counts. This benchmark serves as a regression guard for per-request
// allocations in the model selection framework.
//
// Run:
//
//	go test -run='^$' -bench=BenchmarkModelSelectorPipelineRun -benchmem -count=5 \
//	    ./pkg/modelselector/ | tee bench.out
//	benchstat bench.out
func BenchmarkModelSelectorPipelineRun(b *testing.B) {
	ctx := context.Background()

	// Create test scorers that simulate realistic work
	scorer1 := &benchScorer{
		typedName: plugin.TypedName{Type: "bench-scorer", Name: "cost"},
	}
	scorer2 := &benchScorer{
		typedName: plugin.TypedName{Type: "bench-scorer", Name: "latency"},
	}
	scorer3 := &benchScorer{
		typedName: plugin.TypedName{Type: "bench-scorer", Name: "quality"},
	}

	picker := &benchPicker{
		typedName: plugin.TypedName{Type: "bench-picker", Name: "max-score"},
	}

	pipeline := NewModelSelectorPipeline().WithPicker(picker)
	if err := pipeline.AddPlugins(
		NewWeightedScorer(scorer1, 1.0),
		NewWeightedScorer(scorer2, 1.0),
		NewWeightedScorer(scorer3, 1.0),
	); err != nil {
		b.Fatalf("AddPlugins failed: %v", err)
	}

	request := requesthandling.NewInferenceRequest()

	// Test different model counts to see scaling behavior
	for _, n := range []int{5, 25, 100} {
		models := makeBenchmarkModels(n)
		b.Run(fmt.Sprintf("models=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				cycleState := plugin.NewCycleState()
				result, err := pipeline.Run(ctx, request, cycleState, models)
				if err != nil {
					b.Fatalf("Run failed: %v", err)
				}
				if result == nil {
					b.Fatal("nil result")
				}
			}
		})
	}
}

// makeBenchmarkModels creates n models with varied names for benchmarking.
func makeBenchmarkModels(n int) []datalayer.Model {
	models := make([]datalayer.Model, n)
	for i := 0; i < n; i++ {
		models[i] = datalayer.NewModel(fmt.Sprintf("model-%d", i))
	}
	return models
}

// benchScorer is a minimal scorer for benchmarking that produces deterministic
// scores without external dependencies.
type benchScorer struct {
	typedName plugin.TypedName
}

func (s *benchScorer) TypedName() plugin.TypedName { return s.typedName }

func (s *benchScorer) Score(_ context.Context, _ *plugin.CycleState, _ *requesthandling.InferenceRequest, models []datalayer.Model) map[datalayer.Model]float64 {
	scores := make(map[datalayer.Model]float64, len(models))
	for i, m := range models {
		// Produce varied but deterministic scores
		scores[m] = float64(i%10) / 10.0
	}
	return scores
}

// benchPicker is a minimal picker that selects the highest-scored model.
type benchPicker struct {
	typedName plugin.TypedName
}

func (p *benchPicker) TypedName() plugin.TypedName { return p.typedName }

func (p *benchPicker) Pick(_ context.Context, _ *plugin.CycleState, scoredModels []*modelselector.ScoredModel) *modelselector.PipelineRunResult {
	if len(scoredModels) == 0 {
		return nil
	}
	best := scoredModels[0]
	for _, sm := range scoredModels[1:] {
		if sm.Score > best.Score {
			best = sm
		}
	}
	return &modelselector.PipelineRunResult{TargetModel: best.Model}
}
