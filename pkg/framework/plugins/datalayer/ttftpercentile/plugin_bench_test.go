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

package ttftpercentile

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/datastore"
	dlsrc "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer/datasource"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
)

func init() {
	// Discard logs so logging cost doesn't skew results.
	log.SetLogger(logr.Discard())
}

// fillTracker adds n observations in increasing-timestamp order (as the live extractor
// does), all within the last n milliseconds so both windows stay fully populated.
func fillTracker(n int) *slidingWindowTracker {
	t := newSlidingWindowTracker(n)
	now := time.Now()
	for i := 0; i < n; i++ {
		ts := now.Add(-time.Duration(n-i) * time.Millisecond) // oldest first → newest last
		t.add(0.1+float64(i%100)/100.0, int64(i%16), ts)
	}
	return t
}

// BenchmarkWindow isolates the buffer scan + value sort for the short (capped) window that
// feeds P10/P50/inflightAtP50 and the bucket (uncapped) window whose P10 feeds the floor.
//
//	go test -run='^$' -bench=. -benchmem -count=5 \
//	    ./pkg/framework/plugins/datalayer/ttftpercentile/ | tee bench.out
//	benchstat bench.out
func BenchmarkWindow(b *testing.B) {
	now := time.Now()
	for _, size := range []int{5000, 50000} {
		tr := fillTracker(size)
		b.Run(fmt.Sprintf("short_cap100/size=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = tr.window(now, 3*time.Minute, 100)
			}
		})
		b.Run(fmt.Sprintf("bucket_uncapped/size=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = tr.window(now, time.Minute, 0)
			}
		})
	}
}

// BenchmarkFlush measures the periodic percentile recompute — the short window (P10/P50/
// inflightAtP50) plus the bucketed floor (bucket-window scan, per-bucket P10, history sort
// and min/max evict). This is the extractor's heaviest path (runs once per bucketDuration
// per model). bucketStart is reset each iteration to force a bucket rollover so the floor
// path runs every time; the history fills to bucketHistorySize and then holds there.
func BenchmarkFlush(b *testing.B) {
	now := time.Now()
	for _, size := range []int{5000, 50000} {
		s := &modelPercentileState{tracker: fillTracker(size)}
		b.Run(fmt.Sprintf("size=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s.bucketStart = now.Add(-time.Minute) // force a bucket rollover this call
				s.flush(now, 3*time.Minute, time.Minute, 100, 720)
			}
		})
	}
}

// BenchmarkExtract measures end-to-end per-batch throughput: request+response pairs with
// inflight bookkeeping, CycleState correlation, tracker.add, and the per-batch attribute
// publish. Flush is gated by intervalDuration so it rarely fires here — BenchmarkFlush
// covers that path separately.
func BenchmarkExtract(b *testing.B) {
	ctx := context.Background()

	for _, nModels := range []int{1, 4} {
		events := makeReqRespEvents(256, nModels)
		b.Run(fmt.Sprintf("models=%d", nModels), func(b *testing.B) {
			ext := NewTTFTPercentileExtractor(datastore.NewFakeDataStore())
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := ext.Extract(ctx, events); err != nil {
					b.Fatalf("Extract failed: %v", err)
				}
			}
		})
	}
}

// makeReqRespEvents builds `pairs` request→response event pairs round-robin across nModels,
// each pair sharing its own CycleState (as the real handler does).
func makeReqRespEvents(pairs, nModels int) []dlsrc.Event {
	events := make([]dlsrc.Event, 0, pairs*2)
	for i := 0; i < pairs; i++ {
		req := requesthandling.NewInferenceRequest()
		req.Body["model"] = fmt.Sprintf("model-%d", i%nModels)
		cs := plugin.NewCycleState()
		events = append(events,
			dlsrc.Event{Type: dlsrc.RequestEventType, Payload: dlsrc.RequestPayload{Request: req, CycleState: cs}},
			dlsrc.Event{Type: dlsrc.ResponseEventType, Payload: dlsrc.ResponsePayload{Request: req, CycleState: cs, TTFT: 500 * time.Millisecond}},
		)
	}
	return events
}
