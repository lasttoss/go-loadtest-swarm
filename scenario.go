package loadtest

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Scenario is what to run: a target, a load, and what would make the run a failure.
//
// It is a file rather than a command line because a load test is an argument somebody will need to
// repeat - in CI, on a release, when a player reports lag - and because the numbers in it are claims
// that a reviewer should be able to read without a shell history.
type Scenario struct {
	Name   string `yaml:"name"`
	Target Target `yaml:"target"`
	Load   Load   `yaml:"load"`
	Assert Assert `yaml:"assert"`
}

// Target is the request to make.
type Target struct {
	URL     string            `yaml:"url"`
	Method  string            `yaml:"method"`
	Body    string            `yaml:"body"`
	Headers map[string]string `yaml:"headers"`
	Timeout time.Duration     `yaml:"timeout"`
}

// Load is how much of it to make.
type Load struct {
	// Clients is how many requests are in flight at once. A game server's capacity is the number of
	// players it can hold in a match, so concurrent clients is the unit the answer is wanted in.
	Clients int `yaml:"clients"`
	// Duration is how long to keep it up. A load test that measures the first second measures the warm
	// cache: long enough for a p99 to mean something is the point.
	Duration time.Duration `yaml:"duration"`
	// Ramp spreads the clients over this long, so that a hundred clients do not arrive in the same
	// millisecond and measure the accept queue rather than the server.
	Ramp time.Duration `yaml:"ramp"`
	// Think is how long a client waits between two requests, which is what makes a simulated player
	// behave like one rather than like a benchmark tool.
	Think time.Duration `yaml:"think"`
}

// Assert is what the run has to hold to. A load test whose result nobody can fail is a demo.
type Assert struct {
	P95        time.Duration `yaml:"p95"`
	P99        time.Duration `yaml:"p99"`
	MaxErrors  float64       `yaml:"max_error_rate"`
	MinPerSec  float64       `yaml:"min_rps"`
	MaxLatency time.Duration `yaml:"max_latency"`
}

// LoadScenario reads a scenario file.
func LoadScenario(path string) (Scenario, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Scenario{}, fmt.Errorf("loadtest: %w", err)
	}

	var scenario Scenario
	decoder := yaml.NewDecoder(strings.NewReader(string(raw)))
	decoder.KnownFields(true) // a misspelled key is a scenario that does not test what it says
	if err := decoder.Decode(&scenario); err != nil {
		return Scenario{}, fmt.Errorf("loadtest: %s: %w", path, err)
	}
	if err := scenario.Validate(); err != nil {
		return Scenario{}, fmt.Errorf("loadtest: %s: %w", path, err)
	}
	return scenario, nil
}

// Validate refuses a scenario that would produce a number nobody could act on.
func (s Scenario) Validate() error {
	if strings.TrimSpace(s.Target.URL) == "" {
		return fmt.Errorf("target.url is empty: there is nothing to load")
	}
	if !strings.HasPrefix(s.Target.URL, "http://") && !strings.HasPrefix(s.Target.URL, "https://") {
		return fmt.Errorf("target.url %q needs a scheme", s.Target.URL)
	}
	switch strings.ToUpper(s.Target.Method) {
	case "", "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD":
	default:
		return fmt.Errorf("target.method %q is not a method this can send", s.Target.Method)
	}
	switch {
	case s.Load.Clients <= 0:
		return fmt.Errorf("load.clients is %d: a load test needs at least one client", s.Load.Clients)
	case s.Load.Clients > 100000:
		return fmt.Errorf("load.clients is %d, which is more than one machine can open", s.Load.Clients)
	case s.Load.Duration <= 0:
		return fmt.Errorf("load.duration is %v: a run with no duration does not measure anything", s.Load.Duration)
	case s.Load.Ramp < 0 || s.Load.Think < 0:
		return fmt.Errorf("load.ramp and load.think cannot be negative")
	case s.Load.Ramp > s.Load.Duration:
		return fmt.Errorf("load.ramp (%v) is longer than load.duration (%v), so the run would end before the clients had started", s.Load.Ramp, s.Load.Duration)
	}
	if s.Assert.P95 < 0 || s.Assert.P99 < 0 || s.Assert.MaxLatency < 0 {
		return fmt.Errorf("an assertion cannot be a negative latency")
	}
	if s.Assert.MaxErrors < 0 || s.Assert.MaxErrors > 1 {
		return fmt.Errorf("assert.max_error_rate is %v, want 0..1", s.Assert.MaxErrors)
	}
	return nil
}

// Method is the request method, defaulted.
func (s Scenario) HTTPMethod() string {
	if s.Target.Method == "" {
		return "GET"
	}
	return strings.ToUpper(s.Target.Method)
}
