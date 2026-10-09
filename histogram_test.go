package loadtest_test

import (
	"sync"
	"testing"
	"time"

	"github.com/lasttoss/go-loadtest-swarm"
)

// The number a reviewer will check first: a percentile is the sample it claims to be, within the
// bucket width, rather than something the histogram made up.
func TestAPercentileIsTheSampleItClaimsToBe(t *testing.T) {
	h := loadtest.NewHistogram()
	for _, d := range []time.Duration{3 * time.Millisecond, 30 * time.Millisecond, 300 * time.Millisecond, 3 * time.Second} {
		h.Observe(d)
	}

	if got := h.Percentile(1); within(got, 3*time.Second, 0.01) == false {
		t.Errorf("p100 = %v, want 3s within 1%%", got)
	}
	if got := h.Percentile(0.5); !within(got, 30*time.Millisecond, 0.01) {
		t.Errorf("p50 = %v, want the 30ms in the middle of the four samples", got)
	}
	if got := h.Percentile(0.25); !within(got, 3*time.Millisecond, 0.01) {
		t.Errorf("p25 = %v, want the 3ms of the four samples", got)
	}
}

// The reason there is a histogram here at all: the mean of a run with one bad minute hides the bad
// minute, and the p99 is the number an operator acts on.
func TestTheTailIsNotTheMean(t *testing.T) {
	h := loadtest.NewHistogram()
	for i := 0; i < 99; i++ {
		h.Observe(time.Millisecond)
	}
	h.Observe(time.Second)

	if got := h.Percentile(0.5); !within(got, time.Millisecond, 0.01) {
		t.Errorf("p50 = %v, want 1ms: 99 of the hundred requests were fast", got)
	}
	if got := h.Percentile(0.99); !within(got, time.Millisecond, 0.01) {
		t.Errorf("p99 = %v, want 1ms: one slow request is not yet one percent", got)
	}
	if got := h.Percentile(1); !within(got, time.Second, 0.01) {
		t.Errorf("p100 = %v, want the 1s that one player waited", got)
	}
	if mean := (99*time.Millisecond + time.Second) / 100; h.Percentile(0.5) > mean {
		t.Errorf("p50 = %v is above the mean %v, which is not possible", h.Percentile(0.5), mean)
	}
}

func TestAnEmptyHistogramHasNoLatency(t *testing.T) {
	h := loadtest.NewHistogram()
	if got := h.Percentile(0.95); got != 0 {
		t.Errorf("p95 of nothing = %v, want 0 rather than an invented latency", got)
	}
	if h.Count() != 0 || h.Max() != 0 {
		t.Errorf("count = %d, max = %v", h.Count(), h.Max())
	}
}

func TestPercentilesAreOrdered(t *testing.T) {
	h := loadtest.NewHistogram()
	for i := 1; i <= 1000; i++ {
		h.Observe(time.Duration(i) * time.Microsecond)
	}
	last := time.Duration(0)
	for _, p := range []float64{0, 0.5, 0.9, 0.95, 0.99, 0.999, 1} {
		got := h.Percentile(p)
		if got < last {
			t.Fatalf("p%v = %v, below the percentile before it (%v)", p, got, last)
		}
		last = got
	}
	if got := h.Percentile(1.5); !within(got, 1000*time.Microsecond, 0.01) {
		t.Errorf("a percentile above one = %v, want the maximum rather than a panic", got)
	}
}

// Per-worker histograms are how four hundred clients measure latency without one shared lock, so
// merging has to be the same as having observed everything in one place.
func TestMergingIsTheSameAsObservingEverything(t *testing.T) {
	one, two := loadtest.NewHistogram(), loadtest.NewHistogram()
	for i := 0; i < 500; i++ {
		one.Observe(time.Millisecond)
		two.Observe(10 * time.Millisecond)
	}

	merged := loadtest.NewHistogram()
	merged.Merge(one)
	merged.Merge(two)

	if merged.Count() != 1000 {
		t.Errorf("count = %d, want 1000", merged.Count())
	}
	if got := merged.Percentile(1); !within(got, 10*time.Millisecond, 0.01) {
		t.Errorf("p100 = %v, want 10ms", got)
	}
	if got := merged.Max(); !within(got, 10*time.Millisecond, 0.01) {
		t.Errorf("max = %v, want 10ms", got)
	}
	// and the half that was fast is still half of it
	if got := merged.Percentile(0.4); !within(got, time.Millisecond, 0.01) {
		t.Errorf("p40 = %v, want 1ms", got)
	}
}

func TestObservingFromManyGoroutinesLosesNothing(t *testing.T) {
	h := loadtest.NewHistogram()
	var wg sync.WaitGroup
	for worker := 0; worker < 50; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				h.Observe(time.Duration(i) * time.Microsecond)
			}
		}()
	}
	wg.Wait()

	if h.Count() != 5000 {
		t.Fatalf("count = %d, want 5000", h.Count())
	}
}

func TestANegativeLatencyIsNotRecordedAsNegative(t *testing.T) {
	h := loadtest.NewHistogram()
	h.Observe(-time.Second)

	if got := h.Percentile(1); got < 0 || got > time.Microsecond {
		t.Errorf("p100 = %v, want something at zero rather than a negative latency", got)
	}
}

// within reports whether got is want, to within tolerance of it.
func within(got, want time.Duration, tolerance float64) bool {
	low := time.Duration(float64(want) * (1 - tolerance))
	high := time.Duration(float64(want) * (1 + tolerance))
	return got >= low && got <= high
}
