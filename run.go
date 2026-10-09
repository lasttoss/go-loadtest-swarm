package loadtest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Doer is what this needs from HTTP, so that a test can answer requests without a server and a run can
// use a client with its own connection pool and TLS settings.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Options are the seams a test uses.
type Options struct {
	// Doer makes the requests. Defaults to an HTTP client with the scenario's timeout.
	Doer Doer
	// Now is the clock the latencies are measured against. Defaults to the wall clock; a test hands in
	// a function that returns the times it wants, and then a p99 is a fact rather than a hope. Every
	// client calls it, so it has to be safe to call from several goroutines at once - which the wall
	// clock is and a test clock counting its calls is not, until it says so. A test clock that pairs one
	// reading with the next will only measure the first client correctly, for the same reason.
	Now func() time.Time
	// Progress is called once a second with the running totals, for a run a human is watching.
	Progress func(elapsed time.Duration, requests, failures uint64)
}

// Report is what happened.
type Report struct {
	Scenario string
	Clients  int
	Elapsed  time.Duration

	Requests uint64
	// Failures are the requests the server did not answer: a transport error, or a status of 500 and
	// up. A 4xx is not counted here - a scenario that gets 400s is usually loading a URL that does not
	// exist or is missing a token, which is a bug in the scenario and is reported as Rejected so that
	// it cannot be mistaken for a server failure.
	Failures uint64
	Rejected uint64

	Statuses map[int]uint64
	Latency  *Histogram
}

// PerSecond is the achieved request rate.
func (r Report) PerSecond() float64 {
	if r.Elapsed <= 0 {
		return 0
	}
	return float64(r.Requests) / r.Elapsed.Seconds()
}

// ErrorRate is the share of requests the server failed to answer.
func (r Report) ErrorRate() float64 {
	if r.Requests == 0 {
		return 0
	}
	return float64(r.Failures) / float64(r.Requests)
}

// Violations lists the assertions the run did not hold to, in the words of the scenario file. An
// assertion left at zero is not asserted at all, which is how a scenario says "I do not know yet".
func (r Report) Violations(a Assert) []string {
	var violations []string

	if a.P95 > 0 && r.Latency.Percentile(0.95) > a.P95 {
		violations = append(violations, fmt.Sprintf("p95 is %v, over the %v the scenario allows",
			r.Latency.Percentile(0.95).Round(time.Millisecond), a.P95))
	}
	if a.P99 > 0 && r.Latency.Percentile(0.99) > a.P99 {
		violations = append(violations, fmt.Sprintf("p99 is %v, over the %v the scenario allows",
			r.Latency.Percentile(0.99).Round(time.Millisecond), a.P99))
	}
	if a.MaxLatency > 0 && r.Latency.Max() > a.MaxLatency {
		violations = append(violations, fmt.Sprintf("the slowest request took %v, over the %v the scenario allows",
			r.Latency.Max().Round(time.Millisecond), a.MaxLatency))
	}
	if a.MaxErrors >= 0 && a.MaxErrors > 0 && r.ErrorRate() > a.MaxErrors {
		violations = append(violations, fmt.Sprintf("%.2f%% of requests failed, over the %.2f%% the scenario allows",
			r.ErrorRate()*100, a.MaxErrors*100))
	}
	if a.MaxErrors == 0 && r.Failures > 0 {
		violations = append(violations, fmt.Sprintf("%d requests failed and the scenario allows none", r.Failures))
	}
	if a.MinPerSec > 0 && r.PerSecond() < a.MinPerSec {
		violations = append(violations, fmt.Sprintf("the run made %.1f requests a second, under the %.0f the scenario asks for",
			r.PerSecond(), a.MinPerSec))
	}
	return violations
}

// Markdown is the report as it goes into a pull request, a CI summary, or an issue about lag.
func (r Report) Markdown() string {
	var out strings.Builder
	fmt.Fprintf(&out, "## load test: %s\n\n", r.Scenario)
	fmt.Fprintf(&out, "| requests | failures | rejected (4xx) | requests/s | clients | duration |\n")
	fmt.Fprintf(&out, "|---|---|---|---|---|---|\n")
	fmt.Fprintf(&out, "| %d | %d | %d | %.1f | %d | %v |\n\n",
		r.Requests, r.Failures, r.Rejected, r.PerSecond(), r.Clients, r.Elapsed.Round(time.Millisecond))

	fmt.Fprintf(&out, "### latency (histogram, within 1%%)\n\n")
	fmt.Fprintf(&out, "| p50 | p90 | p95 | p99 | p99.9 | max |\n|---|---|---|---|---|---|\n")
	for _, p := range []float64{0.5, 0.9, 0.95, 0.99, 0.999} {
		fmt.Fprintf(&out, "| %v ", r.Latency.Percentile(p).Round(time.Millisecond))
	}
	fmt.Fprintf(&out, "| %v |\n\n", r.Latency.Max().Round(time.Millisecond))

	if len(r.Statuses) > 0 {
		fmt.Fprintf(&out, "### status codes\n\n| code | count |\n|---|---|\n")
		for _, code := range sortedCodes(r.Statuses) {
			fmt.Fprintf(&out, "| %d | %d |\n", code, r.Statuses[code])
		}
		out.WriteString("\n")
	}
	if r.Rejected > 0 {
		fmt.Fprintf(&out, "> %d requests were answered with a 4xx. That is usually the scenario rather than the\n"+
			"> server: a wrong path, a missing token, a body the handler refuses.\n\n", r.Rejected)
	}
	return out.String()
}

func sortedCodes(statuses map[int]uint64) []int {
	codes := make([]int, 0, len(statuses))
	for code := range statuses {
		codes = append(codes, code)
	}
	sort.Ints(codes)
	return codes
}

// worker is one client's own totals, merged into the report at the end. The two counters the progress
// report reads while the run is going on are atomic, because -race found exactly that: a progress tick
// reading a plain uint64 while a client wrote it.
type worker struct {
	latency  *Histogram
	statuses map[int]uint64
	requests atomic.Uint64
	failures atomic.Uint64
	rejected uint64
}

// Run makes the scenario's requests until the context is done, and reports what came back.
//
// Each client is a goroutine with its own histogram, and they are merged at the end: one lock shared
// by four hundred clients is a load test measuring its own contention.
func Run(ctx context.Context, scenario Scenario, opts Options) (Report, error) {
	if err := scenario.Validate(); err != nil {
		return Report{}, err
	}

	doer := opts.Doer
	if doer == nil {
		timeout := scenario.Target.Timeout
		if timeout <= 0 {
			timeout = 10 * time.Second
		}
		doer = &http.Client{Timeout: timeout}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	started := time.Now()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	workers := make([]*worker, scenario.Load.Clients)
	var wg sync.WaitGroup
	for i := range workers {
		workers[i] = &worker{latency: NewHistogram(), statuses: map[int]uint64{}}
		wg.Add(1)
		go func(w *worker, index int) {
			defer wg.Done()
			// The ramp: client index waits its share of it, so that the server sees them arrive over
			// time rather than all in the same millisecond.
			if scenario.Load.Ramp > 0 {
				wait := time.Duration(float64(scenario.Load.Ramp) * float64(index) / float64(len(workers)))
				if !sleep(ctx, wait) {
					return
				}
			}
			clientLoop(ctx, scenario, doer, now, w)
		}(workers[i], i)
	}

	// A human watching a load test needs to see it is running, and needs to be able to stop it: the
	// report is still worth printing after a Ctrl-C, because a run that fell over at second twelve is
	// the interesting one.
	progressDone := make(chan struct{})
	if opts.Progress != nil {
		go func() {
			defer close(progressDone)
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					var requests, failures uint64
					for _, w := range workers {
						requests += w.requests.Load()
						failures += w.failures.Load()
					}
					opts.Progress(time.Since(started), requests, failures)
				}
			}
		}()
	} else {
		close(progressDone)
	}

	// Wait out the run, or until whoever started it gives up. Either way the workers are then told to
	// stop: without that they would keep making requests and wg.Wait never returns, which is how this
	// looked the first time - a load test that loads forever.
	sleep(ctx, scenario.Load.Duration)
	cancel()
	wg.Wait()
	<-progressDone

	report := Report{
		Scenario: scenario.Name,
		Clients:  scenario.Load.Clients,
		Elapsed:  time.Since(started),
		Statuses: map[int]uint64{},
		Latency:  NewHistogram(),
	}
	for _, w := range workers {
		report.Requests += w.requests.Load()
		report.Failures += w.failures.Load()
		report.Rejected += w.rejected
		for code, count := range w.statuses {
			report.Statuses[code] += count
		}
		report.Latency.Merge(w.latency)
	}
	return report, nil
}

// clientLoop is one client: request, measure, wait a player's worth of time, repeat.
func clientLoop(ctx context.Context, scenario Scenario, doer Doer, now func() time.Time, w *worker) {
	for {
		if ctx.Err() != nil {
			return
		}

		request, err := requestFor(ctx, scenario)
		if err != nil {
			w.requests.Add(1)
			w.failures.Add(1)
			return
		}

		before := now()
		response, err := doer.Do(request)
		took := now().Sub(before)
		if took < 0 {
			took = 0 // a clock that stepped backwards is not a latency this can report
		}
		w.latency.Observe(took)
		w.requests.Add(1)

		switch {
		case err != nil:
			w.failures.Add(1)
		default:
			code := response.StatusCode
			w.statuses[code]++
			switch {
			case code >= 500:
				w.failures.Add(1)
			case code >= 400:
				w.rejected++
			}
			// The body has to be read and closed, or the connection is not reused and the run measures
			// connection setup instead of the server.
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<16))
			_ = response.Body.Close()
		}

		if scenario.Load.Think > 0 {
			if !sleep(ctx, scenario.Load.Think) {
				return
			}
		}
	}
}

func requestFor(ctx context.Context, scenario Scenario) (*http.Request, error) {
	var body io.Reader
	if scenario.Target.Body != "" {
		body = bytes.NewReader([]byte(scenario.Target.Body))
	}
	request, err := http.NewRequestWithContext(ctx, scenario.HTTPMethod(), scenario.Target.URL, body)
	if err != nil {
		return nil, err
	}
	for name, value := range scenario.Target.Headers {
		request.Header.Set(name, value)
	}
	return request, nil
}

// sleep waits, unless the run is over first.
func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
