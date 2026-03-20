package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	pulse "github.com/jmgo38/Pulse"
	"github.com/jmgo38/Pulse/transport"
)

const usageMessage = "usage: pulse run\n\nRuns a sample load test"

var errUsage = fmt.Errorf(usageMessage)
var execute = runTest

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	if len(args) != 1 || args[0] != "run" {
		return errUsage
	}

	result, err := execute()
	fmt.Fprintf(stdout, "Total requests: %d\n", result.Total)
	fmt.Fprintf(stdout, "Failed requests: %d\n", result.Failed)
	fmt.Fprintf(stdout, "Duration: %v\n", result.Duration)
	fmt.Fprintf(stdout, "Min latency: %v\n", result.Latency.Min)
	fmt.Fprintf(stdout, "Max latency: %v\n", result.Latency.Max)
	fmt.Fprintf(stdout, "Mean latency: %v\n", result.Latency.Mean)

	return err
}

func runTest() (pulse.Result, error) {
	client := transport.NewHTTPClient()
	test := pulse.Test{
		Config: pulse.Config{
			Phases: []pulse.Phase{
				{
					Type:        pulse.PhaseTypeConstant,
					Duration:    3 * time.Second,
					ArrivalRate: 5,
				},
			},
			MaxConcurrency: 5,
		},
		Scenario: func(ctx context.Context) error {
			return client.Get(ctx, "https://httpbin.org/get")
		},
	}

	return pulse.Run(test)
}
