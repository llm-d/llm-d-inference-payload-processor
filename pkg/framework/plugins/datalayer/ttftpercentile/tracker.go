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

type percentileTracker interface {
	add(value float64, inflight int64, ts time.Time)
	quantile(p float64, now time.Time, maxAge time.Duration, minCount int) (float64, bool)
	// quantileWithInflight returns the p-th percentile TTFT and the average inflight
	// of observations in the [p-0.1, p+0.1] band — more stable than a single-point inflight.
	quantileWithInflight(p float64, now time.Time, maxAge time.Duration, minCount int) (float64, float64, bool)
	// p10Low computes a two-level P10: first finds the P10 TTFT threshold from all
	// observations, then returns the P10 of observations at or below that threshold.
	// This isolates low-queue-wait observations without requiring a fixed inflight cut-off,
	// so P10Low updates continuously regardless of sustained load level.
	p10Low(now time.Time, maxAge time.Duration, minCount int) (float64, bool)
}

type observation struct {
	value    float64
	inflight int64 // inflight_at_dispatch
	ts       time.Time
}

type slidingWindowTracker struct {
	buf  []observation
	head int
	n    int
}

func newSlidingWindowTracker(cap int) *slidingWindowTracker {
	return &slidingWindowTracker{buf: make([]observation, cap)}
}

func (t *slidingWindowTracker) add(value float64, inflight int64, ts time.Time) {
	t.buf[t.head] = observation{value: value, inflight: inflight, ts: ts}
	t.head = (t.head + 1) % len(t.buf)
	if t.n < len(t.buf) {
		t.n++
	}
}

func (t *slidingWindowTracker) recentObs(now time.Time, maxAge time.Duration) []observation {
	cutoff := now.Add(-maxAge)
	cap := len(t.buf)
	out := make([]observation, 0, t.n)
	for i := 0; i < t.n; i++ {
		obs := t.buf[(t.head-1-i+cap)%cap]
		if !obs.ts.Before(cutoff) {
			out = append(out, obs)
		}
	}
	return out
}

func (t *slidingWindowTracker) quantile(p float64, now time.Time, maxAge time.Duration, minCount int) (float64, bool) {
	obs := t.recentObs(now, maxAge)
	if len(obs) < minCount {
		return 0, false
	}
	vals := make([]float64, len(obs))
	for i, o := range obs {
		vals[i] = o.value
	}
	sort.Float64s(vals)
	idx := p * float64(len(vals)-1)
	lo := int(idx)
	if lo+1 >= len(vals) {
		return vals[lo], true
	}
	return vals[lo] + (idx-float64(lo))*(vals[lo+1]-vals[lo]), true
}

func (t *slidingWindowTracker) p10Low(now time.Time, maxAge time.Duration, minCount int) (float64, bool) {
	obs := t.recentObs(now, maxAge)
	if len(obs) < minCount {
		return 0, false
	}
	vals := make([]float64, len(obs))
	for i, o := range obs {
		vals[i] = o.value
	}
	sort.Float64s(vals)

	// Level 1: P10 threshold — index of the 10th-percentile observation.
	lo := int(0.10 * float64(len(vals)-1))

	// Level 2: P10 of the bottom-decile slice (vals[:lo+1], already sorted).
	// This is ~P1 of all observations: the fastest requests in the window
	// regardless of their inflight count, approximating the hardware floor.
	low := vals[:lo+1]
	idx2 := 0.10 * float64(len(low)-1)
	lo2 := int(idx2)
	if lo2+1 >= len(low) {
		return low[lo2], true
	}
	return low[lo2] + (idx2-float64(lo2))*(low[lo2+1]-low[lo2]), true
}

func (t *slidingWindowTracker) quantileWithInflight(p float64, now time.Time, maxAge time.Duration, minCount int) (float64, float64, bool) {
	obs := t.recentObs(now, maxAge)
	if len(obs) < minCount {
		return 0, 0, false
	}
	sort.Slice(obs, func(i, j int) bool { return obs[i].value < obs[j].value })
	n := len(obs)

	idx := p * float64(n-1)
	lo := int(idx)
	var value float64
	if lo+1 >= n {
		value = obs[lo].value
	} else {
		value = obs[lo].value + (idx-float64(lo))*(obs[lo+1].value-obs[lo].value)
	}

	// Average inflight of observations in the [p-0.1, p+0.1] band.
	// Using a band rather than a single point makes inflightAtP50 more stable.
	bandLo := int((p - 0.10) * float64(n-1))
	if bandLo < 0 {
		bandLo = 0
	}
	bandHi := int((p + 0.10) * float64(n-1))
	if bandHi >= n {
		bandHi = n - 1
	}
	var sum float64
	for i := bandLo; i <= bandHi; i++ {
		sum += float64(obs[i].inflight)
	}
	avgInflight := sum / float64(bandHi-bandLo+1)

	return value, avgInflight, true
}
