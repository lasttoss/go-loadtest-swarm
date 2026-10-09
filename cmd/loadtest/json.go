package main

import (
	"encoding/json"
	"io"
	"strconv"

	"github.com/lasttoss/go-loadtest-swarm"
)

// reportJSON is the report as a machine reads it: a CI job can put these numbers in a chart over time,
// which is the only way a latency regression is visible before a player reports it.
type reportJSON struct {
	Scenario  string            `json:"scenario"`
	Clients   int               `json:"clients"`
	ElapsedMS int64             `json:"elapsed_ms"`
	Requests  uint64            `json:"requests"`
	Failures  uint64            `json:"failures"`
	Rejected  uint64            `json:"rejected"`
	PerSecond float64           `json:"requests_per_second"`
	ErrorRate float64           `json:"error_rate"`
	Statuses  map[string]uint64 `json:"statuses"`
	LatencyMS map[string]int64  `json:"latency_ms"`
}

func writeJSON(out io.Writer, report loadtest.Report) error {
	statuses := map[string]uint64{}
	for code, count := range report.Statuses {
		statuses[strconv.Itoa(code)] = count
	}

	latency := map[string]int64{}
	for _, p := range []struct {
		name string
		at   float64
	}{
		{"p50", 0.5}, {"p90", 0.9}, {"p95", 0.95}, {"p99", 0.99}, {"p99_9", 0.999},
	} {
		latency[p.name] = report.Latency.Percentile(p.at).Milliseconds()
	}
	latency["max"] = report.Latency.Max().Milliseconds()

	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(reportJSON{
		Scenario:  report.Scenario,
		Clients:   report.Clients,
		ElapsedMS: report.Elapsed.Milliseconds(),
		Requests:  report.Requests,
		Failures:  report.Failures,
		Rejected:  report.Rejected,
		PerSecond: report.PerSecond(),
		ErrorRate: report.ErrorRate(),
		Statuses:  statuses,
		LatencyMS: latency,
	})
}
