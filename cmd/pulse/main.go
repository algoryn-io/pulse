package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	pulse "github.com/jmgo38/Pulse"
	"github.com/jmgo38/Pulse/config"
	"github.com/jmgo38/Pulse/transport"
)

const usageMessage = "usage: pulse run [config.yaml] [--json] [--out <file>]\n\nRuns a sample load test or a YAML-defined test"

var errUsage = fmt.Errorf(usageMessage)
var execute = runTest

type runOptions struct {
	configPath string
	jsonOutput bool
	outFile    string
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	options, err := parseRunArgs(args)
	if err != nil {
		return err
	}

	executeArgs := []string{}
	if options.configPath != "" {
		executeArgs = append(executeArgs, options.configPath)
	}

	result, runErr := execute(executeArgs)

	if options.outFile != "" {
		file, err := os.Create(options.outFile)
		if err != nil {
			return err
		}
		defer file.Close()

		if err := writeJSON(file, result); err != nil {
			return err
		}
	}

	if options.jsonOutput {
		if err := writeJSON(stdout, result); err != nil {
			return err
		}
	} else {
		writeText(stdout, result)
	}

	return runErr
}

func runTest(args []string) (pulse.Result, error) {
	if len(args) == 1 {
		test, err := config.Load(args[0])
		if err != nil {
			return pulse.Result{}, err
		}

		return pulse.Run(test)
	}

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

func parseRunArgs(args []string) (runOptions, error) {
	if len(args) == 0 || args[0] != "run" {
		return runOptions{}, errUsage
	}

	var options runOptions
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--json":
			options.jsonOutput = true
		case "--out":
			if i+1 >= len(args) {
				return runOptions{}, errUsage
			}

			options.outFile = args[i+1]
			i++
		default:
			if len(args[i]) > 2 && args[i][:2] == "--" {
				return runOptions{}, errUsage
			}
			if options.configPath != "" {
				return runOptions{}, errUsage
			}

			options.configPath = args[i]
		}
	}

	return options, nil
}

func writeText(w io.Writer, result pulse.Result) {
	fmt.Fprintf(w, "Total requests: %d\n", result.Total)
	fmt.Fprintf(w, "Failed requests: %d\n", result.Failed)
	fmt.Fprintf(w, "Duration: %v\n", result.Duration)
	fmt.Fprintf(w, "Min latency: %v\n", result.Latency.Min)
	fmt.Fprintf(w, "Max latency: %v\n", result.Latency.Max)
	fmt.Fprintf(w, "Mean latency: %v\n", result.Latency.Mean)
}

func writeJSON(w io.Writer, result pulse.Result) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}
