package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	pulse "github.com/jmgo38/Pulse"
	"github.com/jmgo38/Pulse/config"
	"github.com/jmgo38/Pulse/transport"
)

const usageMessage = "usage: pulse run [config.yaml] [--json] [--out <file>]\n\nRuns a sample load test or a YAML-defined test"
const textBanner = "⚡ Pulse — programmable load testing"

var errUsage = fmt.Errorf(usageMessage)
var execute = runTest

type runOptions struct {
	configPath string
	jsonOutput bool
	outFile    string
}

func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr))
}

func runCLI(args []string, stdout io.Writer, stderr io.Writer) int {
	err := run(args, stdout)
	if err == nil {
		return 0
	}
	if !isThresholdEvaluationFailureOnly(err) {
		fmt.Fprintln(stderr, err)
	}
	return exitCode(err)
}

// exitCode maps run errors to process exit codes for CI/CD:
//
//	0 — unused here (success exits before calling exitCode)
//	1 — configuration, runtime, or I/O failure
//	2 — run finished but threshold evaluation failed (only violation errors)
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if isThresholdEvaluationFailureOnly(err) {
		return 2
	}
	return 1
}

// isThresholdEvaluationFailureOnly reports whether err consists solely of
// *pulse.ThresholdViolationError leaves (including inside errors.Join).
func isThresholdEvaluationFailureOnly(err error) bool {
	leaves := unwrapErrorLeaves(err)
	if len(leaves) == 0 {
		return false
	}
	for _, e := range leaves {
		var tv *pulse.ThresholdViolationError
		if !errors.As(e, &tv) {
			return false
		}
	}
	return true
}

func unwrapErrorLeaves(err error) []error {
	seen := map[error]struct{}{}
	var out []error
	var walk func(error)
	walk = func(e error) {
		if e == nil {
			return
		}
		if _, ok := seen[e]; ok {
			return
		}
		seen[e] = struct{}{}

		switch x := e.(type) {
		case interface{ Unwrap() []error }:
			for _, inner := range x.Unwrap() {
				walk(inner)
			}
			return
		}
		if u := errors.Unwrap(e); u != nil {
			walk(u)
			return
		}
		out = append(out, e)
	}
	walk(err)
	return out
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
	showResults := runErr == nil || isThresholdEvaluationFailureOnly(runErr)

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

	if showResults {
		if options.jsonOutput {
			if err := writeJSON(stdout, result); err != nil {
				return err
			}
		} else {
			writeBanner(stdout)
			writeText(stdout, result)
			if isThresholdEvaluationFailureOnly(runErr) {
				fmt.Fprintln(stdout)
				fmt.Fprintln(stdout, "Thresholds failed. See results above.")
			}
		}
	}

	return runErr
}

func writeBanner(w io.Writer) {
	fmt.Fprintln(w, textBanner)
	fmt.Fprintln(w)
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
		Scenario: func(ctx context.Context) (int, error) {
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
	fmt.Fprintf(w, "RPS: %.2f\n", result.RPS)

	fmt.Fprintf(w, "Min latency: %v\n", result.Latency.Min)
	fmt.Fprintf(w, "P50 latency: %v\n", result.Latency.P50)
	fmt.Fprintf(w, "Mean latency: %v\n", result.Latency.Mean)
	fmt.Fprintf(w, "P95 latency: %v\n", result.Latency.P95)
	fmt.Fprintf(w, "P99 latency: %v\n", result.Latency.P99)
	fmt.Fprintf(w, "Max latency: %v\n", result.Latency.Max)

	if len(result.StatusCounts) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Status codes:")
		codes := make([]int, 0, len(result.StatusCounts))
		for code := range result.StatusCounts {
			codes = append(codes, code)
		}
		sort.Ints(codes)
		for _, code := range codes {
			fmt.Fprintf(w, "  %d: %d\n", code, result.StatusCounts[code])
		}
	}

	if len(result.ErrorCounts) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Errors:")
		keys := make([]string, 0, len(result.ErrorCounts))
		for k := range result.ErrorCounts {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(w, "  %s: %d\n", k, result.ErrorCounts[k])
		}
	}

	if len(result.ThresholdOutcomes) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Thresholds:")
		for _, o := range result.ThresholdOutcomes {
			if o.Pass {
				fmt.Fprintf(w, "  PASS %s\n", o.Description)
			} else {
				fmt.Fprintf(w, "  FAIL %s\n", o.Description)
			}
		}
	}
}

func writeJSON(w io.Writer, result pulse.Result) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}
