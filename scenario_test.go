package loadtest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lasttoss/go-loadtest-swarm"
)

const goodScenario = `
name: events ingest
target:
  url: http://localhost:8080/v1/events
  method: post
  headers:
    content-type: application/json
  body: '{"events":[{"user":"p1"}]}'
load:
  clients: 50
  duration: 30s
  ramp: 5s
  think: 100ms
assert:
  p95: 50ms
  p99: 150ms
  max_error_rate: 0.01
  min_rps: 500
`

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scenario.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestAScenarioFileIsReadAndChecked(t *testing.T) {
	scenario, err := loadtest.LoadScenario(write(t, goodScenario))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if scenario.Name != "events ingest" {
		t.Errorf("name = %q", scenario.Name)
	}
	if scenario.HTTPMethod() != "POST" {
		t.Errorf("method = %q, want POST", scenario.HTTPMethod())
	}
	if scenario.Load.Clients != 50 || scenario.Load.Duration != 30*time.Second || scenario.Load.Ramp != 5*time.Second {
		t.Errorf("load = %+v", scenario.Load)
	}
	if scenario.Assert.P95 != 50*time.Millisecond || scenario.Assert.MinPerSec != 500 {
		t.Errorf("assert = %+v", scenario.Assert)
	}
}

func TestAMethodLeftOutIsAGet(t *testing.T) {
	scenario, err := loadtest.LoadScenario(write(t, "target:\n  url: http://localhost:8080/healthz\nload:\n  clients: 1\n  duration: 1s\n"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := scenario.HTTPMethod(); got != "GET" {
		t.Errorf("method = %q, want GET", got)
	}
	if got := scenario.Target.Headers; len(got) != 0 {
		t.Errorf("headers = %v, want none", got)
	}
}

// A misspelled key would otherwise be a scenario that silently does not test what it says it does -
// "clints: 50" would run one client and report a healthy server.
func TestAMisspelledKeyIsRefused(t *testing.T) {
	_, err := loadtest.LoadScenario(write(t, `
name: typo
target:
  url: http://localhost:8080/healthz
load:
  clints: 50
  duration: 10s
`))
	if err == nil {
		t.Fatal("a scenario with a misspelled key was accepted")
	}
	if !strings.Contains(err.Error(), "clints") {
		t.Errorf("err = %v, want it to name the key", err)
	}
}

func TestScenariosThatWouldNotMeasureAnythingAreRefused(t *testing.T) {
	cases := map[string]string{
		"no target":            "load:\n  clients: 1\n  duration: 1s\n",
		"no scheme":            "target:\n  url: localhost:8080/x\nload:\n  clients: 1\n  duration: 1s\n",
		"unknown method":       "target:\n  url: http://localhost:8080/x\n  method: teleport\nload:\n  clients: 1\n  duration: 1s\n",
		"no clients":           "target:\n  url: http://localhost:8080/x\nload:\n  clients: 0\n  duration: 1s\n",
		"too many clients":     "target:\n  url: http://localhost:8080/x\nload:\n  clients: 1000000\n  duration: 1s\n",
		"no duration":          "target:\n  url: http://localhost:8080/x\nload:\n  clients: 1\n",
		"ramp longer than run": "target:\n  url: http://localhost:8080/x\nload:\n  clients: 1\n  duration: 1s\n  ramp: 10s\n",
		"negative think":       "target:\n  url: http://localhost:8080/x\nload:\n  clients: 1\n  duration: 1s\n  think: -1s\n",
		"error rate over one":  "target:\n  url: http://localhost:8080/x\nload:\n  clients: 1\n  duration: 1s\nassert:\n  max_error_rate: 2\n",
		"negative assertion":   "target:\n  url: http://localhost:8080/x\nload:\n  clients: 1\n  duration: 1s\nassert:\n  p95: -1ms\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := loadtest.LoadScenario(write(t, body)); err == nil {
				t.Fatalf("this scenario was accepted:\n%s", body)
			}
		})
	}
}

func TestTheExampleScenarioIsValid(t *testing.T) {
	scenario, err := loadtest.LoadScenario(filepath.Join("examples", "events-ingest.yaml"))
	if err != nil {
		t.Fatalf("the example scenario does not load: %v", err)
	}
	if scenario.Load.Clients == 0 || scenario.Load.Duration == 0 {
		t.Errorf("the example is not a scenario anything could run: %+v", scenario.Load)
	}
}
