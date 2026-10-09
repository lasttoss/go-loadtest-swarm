// Command loadtest runs a scenario and exits with the answer, so that CI can fail on it.
//
//	exit 0  every assertion held
//	exit 1  the run finished and an assertion did not hold
//	exit 2  the run could not be made (a scenario that does not parse, a flag that makes no sense)
//
// A load test that cannot fail is a demonstration, not a test.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lasttoss/go-loadtest-swarm"
)

func main() {
	os.Exit(run())
}

func run() int {
	var (
		path     = flag.String("scenario", "", "the scenario file to run (required)")
		clients  = flag.Int("clients", 0, "override load.clients")
		duration = flag.Duration("duration", 0, "override load.duration, e.g. -duration 10s")
		quiet    = flag.Bool("quiet", false, "do not print progress while the run is going on")
		jsonOut  = flag.Bool("json", false, "print the report as JSON instead of markdown")
	)
	flag.Parse()

	if *path == "" {
		fmt.Fprintln(os.Stderr, "loadtest: -scenario is required")
		flag.Usage()
		return 2
	}

	scenario, err := loadtest.LoadScenario(*path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 2
	}
	if *clients > 0 {
		scenario.Load.Clients = *clients
	}
	if *duration > 0 {
		scenario.Load.Duration = *duration
	}
	if err := scenario.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "loadtest: %v\n", err)
		return 2
	}

	// Ctrl-C stops the run and still prints what happened: a run that fell over at second twelve is
	// the one worth reading about.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	options := loadtest.Options{}
	if !*quiet {
		options.Progress = func(elapsed time.Duration, requests, failures uint64) {
			fmt.Fprintf(os.Stderr, "\r%v: %d requests, %d failed   ", elapsed.Round(time.Second), requests, failures)
		}
	}

	report, err := loadtest.Run(ctx, scenario, options)
	if !*quiet {
		fmt.Fprint(os.Stderr, "\r")
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "loadtest: %v\n", err)
		return 2
	}

	if *jsonOut {
		if err := writeJSON(os.Stdout, report); err != nil {
			fmt.Fprintf(os.Stderr, "loadtest: %v\n", err)
			return 2
		}
	} else {
		fmt.Print(report.Markdown())
	}

	violations := report.Violations(scenario.Assert)
	if len(violations) == 0 {
		fmt.Printf("every assertion held: %d requests, p95 %v, %.1f requests/s\n",
			report.Requests, report.Latency.Percentile(0.95).Round(time.Millisecond), report.PerSecond())
		return 0
	}

	fmt.Println("\n### the run did not hold to its assertions")
	for _, violation := range violations {
		fmt.Printf("- %s\n", violation)
	}
	return 1
}
