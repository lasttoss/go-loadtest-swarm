// Package loadtest runs a scenario against a server and reports what the server did with it.
//
// The numbers a load test exists for are the tail ones - p95 and p99, not the mean - because a mean
// latency of 40ms is what a player feels as a stutter once a minute. A histogram that keeps every
// sample is either a memory leak or a lost tail, so this one buckets: a value is stored in the bucket
// for the next 1% above it, which bounds the error of any percentile at under 1% and costs a few
// thousand counters however long the run is.
package loadtest

import (
	"math"
	"sort"
	"sync"
	"time"
)

// bucketRatio is the width of a bucket. 1.01 gives buckets at most 1% apart, so a reported percentile
// is within 0.5% of the sample it came from - far below the run-to-run noise of any real measurement.
const bucketRatio = 1.01

var bucketLog = math.Log(bucketRatio)

// Histogram is a latency distribution, safe to observe into from several goroutines at once.
type Histogram struct {
	mu      sync.Mutex
	buckets map[int]uint64
	count   uint64
	max     time.Duration
}

// NewHistogram returns an empty histogram.
func NewHistogram() *Histogram {
	return &Histogram{buckets: map[int]uint64{}}
}

// Observe records one latency.
func (h *Histogram) Observe(d time.Duration) {
	if d < 0 {
		d = 0
	}
	index := bucketOf(d)

	h.mu.Lock()
	h.buckets[index]++
	h.count++
	if d > h.max {
		h.max = d
	}
	h.mu.Unlock()
}

// Count is how many samples were recorded.
func (h *Histogram) Count() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.count
}

// Max is the longest latency observed.
func (h *Histogram) Max() time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.max
}

// Percentile is the latency at p (0..1), to within 1%. An empty histogram has no latency and returns
// zero rather than inventing one.
func (h *Histogram) Percentile(p float64) time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.count == 0 {
		return 0
	}
	if p <= 0 {
		p = 0
	}
	if p > 1 {
		p = 1
	}

	// Nearest-rank: the p95 of a hundred samples is the ninety-fifth of them, not an interpolation
	// between two that a player never felt.
	rank := uint64(math.Ceil(p * float64(h.count)))
	if rank == 0 {
		rank = 1
	}

	indexes := make([]int, 0, len(h.buckets))
	for index := range h.buckets {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)

	var seen uint64
	for _, index := range indexes {
		seen += h.buckets[index]
		if seen >= rank {
			return valueOf(index)
		}
	}
	return h.max
}

// Merge folds another histogram into this one, which is how per-worker histograms become one report
// without every worker contending on a single lock.
func (h *Histogram) Merge(other *Histogram) {
	if other == nil {
		return
	}
	other.mu.Lock()
	buckets := make(map[int]uint64, len(other.buckets))
	for index, count := range other.buckets {
		buckets[index] = count
	}
	count, max := other.count, other.max
	other.mu.Unlock()

	h.mu.Lock()
	defer h.mu.Unlock()
	for index, observed := range buckets {
		h.buckets[index] += observed
	}
	h.count += count
	if max > h.max {
		h.max = max
	}
}

// bucketOf is the bucket a latency belongs in: the smallest one whose upper bound is at least this
// value, so a reported number is never below the sample it came from.
func bucketOf(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int(math.Floor(math.Log(float64(d)) / bucketLog))
}

// valueOf is a latency that the bucket could have held: its midpoint, within half a bucket of every
// sample in it.
func valueOf(index int) time.Duration {
	return time.Duration(math.Exp((float64(index) + 0.5) * bucketLog))
}
