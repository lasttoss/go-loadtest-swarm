package loadtest_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lasttoss/go-loadtest-swarm"
)

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(request *http.Request) (*http.Response, error) { return f(request) }

func answer(status int) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

// clockWithLatency is the seam that makes this testable: the run measures every request against the
// clock it was handed, so a test can say "each of these took 10ms" and then assert that the p50 is
// 10ms - rather than hoping the machine was quiet.
func clockWithLatency(d time.Duration) func() time.Time {
	epoch := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	var calls atomic.Int64 // every client calls the clock, so a test clock has to be safe to call at once
	return func() time.Time {
		n := calls.Add(1)
		base := epoch.Add(time.Duration((n-1)/2) * time.Second)
		if n%2 == 0 {
			return base.Add(d) // the "after the request" reading of the pair
		}
		return base
	}
}

func scenarioFor(clients int, duration time.Duration) loadtest.Scenario {
	return loadtest.Scenario{
		Name:   "unit",
		Target: loadtest.Target{URL: "http://game.example/v1/events"},
		Load:   loadtest.Load{Clients: clients, Duration: duration},
	}
}

func TestARunMakesRequestsAndReportsTheirLatency(t *testing.T) {
	report, err := loadtest.Run(context.Background(), scenarioFor(2, 80*time.Millisecond), loadtest.Options{
		Doer: doerFunc(func(*http.Request) (*http.Response, error) { return answer(200), nil }),
		Now:  clockWithLatency(10 * time.Millisecond),
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if report.Requests == 0 {
		t.Fatal("no requests were made")
	}
	if report.Failures != 0 || report.Rejected != 0 {
		t.Errorf("failures = %d, rejected = %d, want none", report.Failures, report.Rejected)
	}
	if got := report.Statuses[200]; got != report.Requests {
		t.Errorf("200s = %d of %d requests", got, report.Requests)
	}
	if got := report.Latency.Percentile(0.5); !within(got, 10*time.Millisecond, 0.01) {
		t.Errorf("p50 = %v, want the 10ms every request took", got)
	}
	if report.Elapsed < 80*time.Millisecond {
		t.Errorf("the run lasted %v, shorter than the 80ms it was asked for", report.Elapsed)
	}
	if report.PerSecond() <= 0 {
		t.Errorf("requests a second = %v", report.PerSecond())
	}
	if violations := report.Violations(loadtest.Assert{P95: time.Second}); len(violations) != 0 {
		t.Errorf("a run with no failures violated %v", violations)
	}
}

// A 500 is the server failing, and it has to be impossible to read the report as a success.
func TestAServerThatAnswers500IsAFailedRun(t *testing.T) {
	report, err := loadtest.Run(context.Background(), scenarioFor(2, 60*time.Millisecond), loadtest.Options{
		Doer: doerFunc(func(*http.Request) (*http.Response, error) { return answer(500), nil }),
		Now:  clockWithLatency(time.Millisecond),
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if report.Failures != report.Requests {
		t.Errorf("failures = %d of %d requests", report.Failures, report.Requests)
	}
	if got := report.ErrorRate(); got != 1 {
		t.Errorf("error rate = %v, want 1", got)
	}
	violations := report.Violations(loadtest.Assert{})
	if len(violations) != 1 || !strings.Contains(violations[0], "failed") {
		t.Errorf("violations = %v, want one about the failures", violations)
	}
	if !strings.Contains(report.Markdown(), "| 500 |") {
		t.Error("the report does not show the status code the server answered")
	}
}

// A 4xx is a scenario that does not match the server - a wrong path, a missing token - and counting it
// as a server failure sends the reader looking in the wrong place.
func TestA4xxIsReportedAsTheScenariosProblem(t *testing.T) {
	report, err := loadtest.Run(context.Background(), scenarioFor(1, 40*time.Millisecond), loadtest.Options{
		Doer: doerFunc(func(*http.Request) (*http.Response, error) { return answer(404), nil }),
		Now:  clockWithLatency(time.Millisecond),
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if report.Rejected != report.Requests {
		t.Errorf("rejected = %d of %d requests", report.Rejected, report.Requests)
	}
	if report.Failures != 0 {
		t.Errorf("failures = %d, want none: the server answered", report.Failures)
	}
	if markdown := report.Markdown(); !strings.Contains(markdown, "4xx") {
		t.Error("the report does not warn that the requests were rejected")
	}
}

func TestAConnectionThatNeverArrivedIsAFailure(t *testing.T) {
	report, err := loadtest.Run(context.Background(), scenarioFor(1, 40*time.Millisecond), loadtest.Options{
		Doer: doerFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("dial tcp: connection refused")
		}),
		Now: clockWithLatency(time.Millisecond),
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if report.Failures != report.Requests {
		t.Errorf("failures = %d of %d requests", report.Failures, report.Requests)
	}
	if len(report.Statuses) != 0 {
		t.Errorf("statuses = %v, want nothing for requests that never arrived", report.Statuses)
	}
}

// The number of requests in flight is the whole of the load: a run that quietly made more would be
// reporting on a server it did not mean to test.
func TestNoMoreRequestsAreInFlightThanThereAreClients(t *testing.T) {
	const clients = 3
	var inFlight, worst int64

	report, err := loadtest.Run(context.Background(), scenarioFor(clients, 80*time.Millisecond), loadtest.Options{
		Doer: doerFunc(func(*http.Request) (*http.Response, error) {
			now := atomic.AddInt64(&inFlight, 1)
			for {
				seen := atomic.LoadInt64(&worst)
				if now <= seen || atomic.CompareAndSwapInt64(&worst, seen, now) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			atomic.AddInt64(&inFlight, -1)
			return answer(200), nil
		}),
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if worst > clients {
		t.Errorf("%d requests were in flight with %d clients", worst, clients)
	}
	if report.Requests < clients {
		t.Errorf("only %d requests were made by %d clients in 80ms", report.Requests, clients)
	}
}

func TestAGoodRunSaysWhatItHeldTo(t *testing.T) {
	report := loadtest.Report{
		Scenario: "wallet",
		Clients:  10,
		Elapsed:  time.Second,
		Requests: 1000,
		Latency:  loadtest.NewHistogram(),
		Statuses: map[int]uint64{200: 1000},
	}
	for i := 0; i < 1000; i++ {
		report.Latency.Observe(20 * time.Millisecond)
	}

	if violations := report.Violations(loadtest.Assert{
		P95:       50 * time.Millisecond,
		P99:       100 * time.Millisecond,
		MaxErrors: 0.01,
		MinPerSec: 500,
	}); len(violations) != 0 {
		t.Fatalf("violations = %v, want none", violations)
	}

	markdown := report.Markdown()
	for _, want := range []string{"## load test: wallet", "| 1000 |", "### latency", "### status codes"} {
		if !strings.Contains(markdown, want) {
			t.Errorf("the report is missing %q:\n%s", want, markdown)
		}
	}
}

func TestTheAssertionsSayWhichOneWasMissed(t *testing.T) {
	report := loadtest.Report{
		Scenario: "wallet",
		Clients:  10,
		Elapsed:  time.Second,
		Requests: 100,
		Failures: 10,
		Latency:  loadtest.NewHistogram(),
		Statuses: map[int]uint64{200: 90, 500: 10},
	}
	for i := 0; i < 100; i++ {
		report.Latency.Observe(200 * time.Millisecond)
	}

	violations := report.Violations(loadtest.Assert{
		P95:        100 * time.Millisecond,
		MaxErrors:  0.02,
		MinPerSec:  500,
		MaxLatency: 150 * time.Millisecond,
	})
	joined := strings.Join(violations, " | ")
	for _, want := range []string{"p95", "of requests failed", "requests a second", "slowest request"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the violations do not mention %q: %v", want, violations)
		}
	}
}

func TestARunRefusesAScenarioThatWouldNotMeasureAnything(t *testing.T) {
	if _, err := loadtest.Run(context.Background(), loadtest.Scenario{}, loadtest.Options{}); err == nil {
		t.Fatal("a scenario with no target ran anyway")
	}
}

func TestProgressIsReportedWhileTheRunIsGoingOn(t *testing.T) {
	seen := make(chan string, 4)
	report, err := loadtest.Run(context.Background(), scenarioFor(1, 1050*time.Millisecond), loadtest.Options{
		Doer: doerFunc(func(*http.Request) (*http.Response, error) { return answer(200), nil }),
		Progress: func(elapsed time.Duration, requests, failures uint64) {
			seen <- fmt.Sprintf("%v %d %d", elapsed.Round(time.Second), requests, failures)
		},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(seen) == 0 {
		t.Error("a run of over a second reported no progress at all")
	}
	if report.Requests == 0 {
		t.Error("the run made no requests")
	}
}

func TestARunStopsWhenTheContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(60 * time.Millisecond)
		cancel()
	}()

	started := time.Now()
	report, err := loadtest.Run(ctx, scenarioFor(1, 10*time.Second), loadtest.Options{
		Doer: doerFunc(func(*http.Request) (*http.Response, error) { return answer(200), nil }),
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Errorf("the run went on for %v after the context was cancelled", elapsed)
	}
	if report.Requests == 0 {
		t.Error("the report of a run that was stopped early has nothing in it")
	}
}
