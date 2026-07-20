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
	"sort"
	"time"
)

type observation struct {
	value    float64
	inflight int64 // inflight_at_dispatch
	ts       time.Time
}

// slidingWindowTracker is a fixed-capacity ring buffer of TTFT observations.
type slidingWindowTracker struct {
	buf  []observation
	head int
	n    int
}

func newSlidingWindowTracker(capacity int) *slidingWindowTracker {
	return &slidingWindowTracker{buf: make([]observation, capacity)}
}

func (t *slidingWindowTracker) add(value float64, inflight int64, ts time.Time) {
	t.buf[t.head] = observation{value: value, inflight: inflight, ts: ts}
	t.head = (t.head + 1) % len(t.buf)
	if t.n < len(t.buf) {
		t.n++
	}
}

// window returns the observations within maxAge, capped to the most recent maxN
// (0 = no cap), sorted ascending by value. Observations are stored in time order,
// so the newest-first scan stops at the first one older than the cutoff.
func (t *slidingWindowTracker) window(now time.Time, maxAge time.Duration, maxN int) []observation {
	cutoff := now.Add(-maxAge)
	size := len(t.buf)
	capHint := t.n
	if maxN > 0 && maxN < capHint {
		capHint = maxN
	}
	out := make([]observation, 0, capHint)
	for i := 0; i < t.n; i++ {
		o := t.buf[(t.head-1-i+size)%size]
		if o.ts.Before(cutoff) {
			break
		}
		out = append(out, o)
		if maxN > 0 && len(out) == maxN {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].value < out[j].value })
	return out
}

// percentileOf returns the p-th percentile value of a value-sorted slice by linear interpolation.
func percentileOf(sorted []observation, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := p * float64(len(sorted)-1)
	lo := int(idx)
	if lo+1 >= len(sorted) {
		return sorted[lo].value
	}
	return sorted[lo].value + (idx-float64(lo))*(sorted[lo+1].value-sorted[lo].value)
}

// percentileFloat64 returns the p-th percentile of a value-sorted float slice by
// linear interpolation. Mirrors percentileOf for the per-bucket P10 history.
func percentileFloat64(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	idx := p * float64(n-1)
	lo := int(idx)
	if lo+1 >= n {
		return sorted[lo]
	}
	return sorted[lo] + (idx-float64(lo))*(sorted[lo+1]-sorted[lo])
}

// dropMinMax removes the smallest and largest entries from a value-sorted slice,
// reusing the backing array. It is the floor history's eviction policy: when the
// per-bucket P10 history is full, the extreme buckets are dropped rather than the
// oldest, so a single anomalously fast or slow bucket never sticks in the history.
func dropMinMax(sorted []float64) []float64 {
	if len(sorted) < 2 {
		return sorted
	}
	return sorted[:copy(sorted, sorted[1:len(sorted)-1])]
}

// bandInflight returns the average inflight of observations in the [p-0.1, p+0.1] band of
// a value-sorted slice. Averaging a band rather than a single point stabilises the estimate.
func bandInflight(sorted []observation, p float64) float64 {
	n := len(sorted)
	lo := int((p - 0.10) * float64(n-1))
	if lo < 0 {
		lo = 0
	}
	hi := int((p + 0.10) * float64(n-1))
	if hi >= n {
		hi = n - 1
	}
	var sum float64
	for i := lo; i <= hi; i++ {
		sum += float64(sorted[i].inflight)
	}
	return sum / float64(hi-lo+1)
}
