package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	pulse "algoryn.io/pulse"
	"algoryn.io/pulse/config"
	"algoryn.io/pulse/distributed/worker"
)

const usageMessage = "usage: pulse run <config.yaml> [--format text|json] [--quiet] [--dry-run] [--seed <n>] [--out <file>] [--junit <file>] [--workers host:port,...]\n\nRuns the load test defined in <config.yaml>\n\nDistributed mode: pulse worker --addr <host:port>"
const textBanner = "Pulse"
const textBannerSubtitle = "Programmable load testing"
const textStatusPassed = "✔ Test passed"
const textStatusThresholdFailed = "❌ Thresholds failed"

var errUsage = fmt.Errorf(usageMessage)
var execute = runTest

type runOptions struct {
	configPath string
	format     string
	quiet      bool
	dryRun     bool
	seed       *int64
	outFile    string
	junitFile  string
	workers    []string // distributed worker addresses
}

type jsonSummary struct {
	Total       int64   `json:"total"`
	Failed      int64   `json:"failed"`
	RPS         float64 `json:"rps"`
	DurationMS  int64   `json:"duration_ms"`
	Scheduled   int64   `json:"scheduled"`
	Started     int64   `json:"started"`
	Dropped     int64   `json:"dropped"`
	DroppedRate float64 `json:"dropped_rate"`
	Completed   int64   `json:"completed"`
	MaxActive   int64   `json:"max_active"`
}

type jsonLatency struct {
	MinMS  float64 `json:"min_ms"`
	P50MS  float64 `json:"p50_ms"`
	MeanMS float64 `json:"mean_ms"`
	P90MS  float64 `json:"p90_ms"`
	P95MS  float64 `json:"p95_ms"`
	P99MS  float64 `json:"p99_ms"`
	MaxMS  float64 `json:"max_ms"`
}

type jsonThreshold struct {
	Description string `json:"description"`
	Pass        bool   `json:"pass"`
}

type jsonSnapshot struct {
	StartedAt   string           `json:"started_at"`
	Summary     jsonSummary      `json:"summary"`
	Latency     jsonLatency      `json:"latency"`
	StatusCodes map[string]int64 `json:"status_codes"`
	Errors      map[string]int64 `json:"errors"`
}

type jsonResult struct {
	SchemaVersion int              `json:"schema_version"`
	Summary       jsonSummary      `json:"summary"`
	Latency       jsonLatency      `json:"latency"`
	StatusCodes   map[string]int64 `json:"status_codes"`
	Errors        map[string]int64 `json:"errors"`
	Thresholds    []jsonThreshold  `json:"thresholds"`
	Snapshots     []jsonSnapshot   `json:"snapshots"`
	Passed        bool             `json:"passed"`
}

func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr))
}

func runCLI(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "worker" {
		if err := runWorker(args[1:]); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}

	err := run(args, stdout)
	if err == nil {
		return 0
	}
	if !isThresholdEvaluationFailureOnly(err) {
		fmt.Fprintln(stderr, err)
	}
	return exitCode(err)
}

// runWorker starts a distributed worker server and blocks until SIGINT/SIGTERM.
// Usage: pulse worker --addr host:port
func runWorker(args []string) error {
	addr := ":9100" // default
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--addr":
			if i+1 >= len(args) {
				return fmt.Errorf("usage: pulse worker --addr <host:port>")
			}
			addr = args[i+1]
			i++
		default:
			return fmt.Errorf("usage: pulse worker --addr <host:port>")
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	fmt.Fprintf(os.Stderr, "Pulse worker listening on %s\n", addr)
	// CLI worker has no pre-registered scenario: it uses HTTPScenario from RunRequest.
	return worker.New(nil).ListenAndServe(ctx, addr)
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
	// CLI --seed takes precedence over seed in YAML (Config.Seed is applied
	// inside RunContext only when SetSeed has not been called).
	if options.seed != nil {
		pulse.SetSeed(*options.seed)
	}

	if options.dryRun {
		return runDryRun(options, stdout)
	}

	executeArgs := []string{}
	if options.configPath != "" {
		executeArgs = append(executeArgs, options.configPath)
	}

	progress := newProgressReporter(stdout, options.format)
	progress.start()
	var result pulse.Result
	var runErr error
	if len(options.workers) > 0 {
		result, runErr = runTestWithWorkers(executeArgs, options.workers)
	} else {
		result, runErr = execute(executeArgs)
	}
	progress.stop()
	showResults := runErr == nil || isThresholdEvaluationFailureOnly(runErr)

	if options.outFile != "" {
		if err := writeFileAtomic(options.outFile, func(w io.Writer) error {
			return writeJSON(w, result, runErr == nil)
		}); err != nil {
			return err
		}
	}

	if options.junitFile != "" {
		if err := writeFileAtomic(options.junitFile, func(w io.Writer) error {
			return writeJUnit(w, result, runErr)
		}); err != nil {
			return err
		}
	}

	if showResults {
		if options.format == "json" {
			if err := writeJSON(stdout, result, runErr == nil); err != nil {
				return err
			}
		} else {
			writeTextOutput(stdout, result, options.quiet, isThresholdEvaluationFailureOnly(runErr))
		}
	}

	return runErr
}

func writeBanner(w io.Writer) {
	fmt.Fprintln(w, textBanner)
	fmt.Fprintln(w, textBannerSubtitle)
	fmt.Fprintln(w)
}

func runTest(args []string) (pulse.Result, error) {
	if len(args) != 1 {
		return pulse.Result{}, errUsage
	}
	test, err := config.Load(args[0])
	if err != nil {
		return pulse.Result{}, err
	}
	return pulse.Run(test)
}

// runTestWithWorkers loads the config and overrides Workers with the CLI flag value.
func runTestWithWorkers(args []string, workers []string) (pulse.Result, error) {
	if len(args) != 1 {
		return pulse.Result{}, errUsage
	}
	test, err := config.Load(args[0])
	if err != nil {
		return pulse.Result{}, err
	}
	test.Config.Workers = workers
	return pulse.Run(test)
}

func parseRunArgs(args []string) (runOptions, error) {
	if len(args) == 0 || args[0] != "run" {
		return runOptions{}, errUsage
	}

	options := runOptions{format: "text"}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--json":
			if options.format != "text" {
				return runOptions{}, errUsage
			}
			options.format = "json"
		case "--format":
			if i+1 >= len(args) {
				return runOptions{}, errUsage
			}
			if options.format != "text" && options.format != args[i+1] {
				return runOptions{}, errUsage
			}
			switch args[i+1] {
			case "text", "json":
				options.format = args[i+1]
			default:
				return runOptions{}, errUsage
			}
			i++
		case "--quiet":
			options.quiet = true
			if options.format == "json" {
				return runOptions{}, errUsage
			}
		case "--dry-run":
			options.dryRun = true
		case "--seed":
			if i+1 >= len(args) {
				return runOptions{}, errUsage
			}
			seed, err := strconv.ParseInt(args[i+1], 10, 64)
			if err != nil {
				return runOptions{}, errUsage
			}
			options.seed = &seed
			i++
		case "--out":
			if i+1 >= len(args) {
				return runOptions{}, errUsage
			}

			options.outFile = args[i+1]
			i++
		case "--junit":
			if i+1 >= len(args) {
				return runOptions{}, errUsage
			}
			options.junitFile = args[i+1]
			i++
		case "--workers":
			if i+1 >= len(args) {
				return runOptions{}, errUsage
			}
			for _, w := range strings.Split(args[i+1], ",") {
				w = strings.TrimSpace(w)
				if w != "" {
					options.workers = append(options.workers, w)
				}
			}
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

	if options.quiet && options.format == "json" {
		return runOptions{}, errUsage
	}
	return options, nil
}

// runDryRun loads and validates the config, prints a phase summary, and
// returns without sending any traffic. It requires a config file.
func runDryRun(options runOptions, w io.Writer) error {
	if options.configPath == "" {
		return fmt.Errorf("pulse: --dry-run requires a config file\n\n%s", usageMessage)
	}
	test, err := config.Load(options.configPath)
	if err != nil {
		return err
	}
	writeDryRunSummary(w, options.configPath, test.Config, options.quiet)
	return nil
}

// writeDryRunSummary prints a human-readable summary of cfg to w.
func writeDryRunSummary(w io.Writer, path string, cfg pulse.Config, quiet bool) {
	if !quiet {
		fmt.Fprintln(w, textBanner+" (dry run)")
		fmt.Fprintln(w, textBannerSubtitle)
		fmt.Fprintln(w)
		fmt.Fprintf(w, "Config: %s\n", path)
		fmt.Fprintln(w)
	}

	fmt.Fprintf(w, "Phases (%d):\n", len(cfg.Phases))
	var total time.Duration
	for i, p := range cfg.Phases {
		total += p.Duration
		switch {
		case p.IsConstant():
			fmt.Fprintf(w, "  %d  constant  %d rps  %v\n", i+1, p.ArrivalRate, p.Duration)
		case p.IsRamp():
			fmt.Fprintf(w, "  %d  ramp      %d→%d rps  %v\n", i+1, p.From, p.To, p.Duration)
		case p.IsStep():
			fmt.Fprintf(w, "  %d  step      %d→%d rps  %d steps  %v\n", i+1, p.From, p.To, p.Steps, p.Duration)
		case p.IsSpike():
			fmt.Fprintf(w, "  %d  spike     %d→%d rps  spike for %v at %v  %v\n",
				i+1, p.From, p.To, p.SpikeDuration, p.SpikeAt, p.Duration)
		default:
			fmt.Fprintf(w, "  %d  %s  %v\n", i+1, p.Type, p.Duration)
		}
	}
	fmt.Fprintf(w, "Total duration: %v\n", total)

	if !quiet {
		fmt.Fprintln(w)
		if cfg.MaxConcurrency > 0 {
			fmt.Fprintf(w, "MaxConcurrency: %d\n", cfg.MaxConcurrency)
		} else {
			fmt.Fprintln(w, "MaxConcurrency: unlimited")
		}
		if cfg.SaturationPolicy != "" {
			fmt.Fprintf(w, "SaturationPolicy: %s\n", cfg.SaturationPolicy)
		} else {
			fmt.Fprintln(w, "SaturationPolicy: drop (default)")
		}

		t := cfg.Thresholds
		hasThresholds := t.ErrorRate > 0 || t.MaxMeanLatency > 0 || t.MaxP95Latency > 0 ||
			t.MaxP99Latency > 0 || t.MaxDroppedRate > 0
		if hasThresholds {
			fmt.Fprintln(w)
			fmt.Fprintln(w, "Thresholds:")
			if t.ErrorRate > 0 {
				fmt.Fprintf(w, "  error_rate < %g\n", t.ErrorRate)
			}
			if t.MaxMeanLatency > 0 {
				fmt.Fprintf(w, "  mean_latency < %v\n", t.MaxMeanLatency)
			}
			if t.MaxP95Latency > 0 {
				fmt.Fprintf(w, "  p95_latency < %v\n", t.MaxP95Latency)
			}
			if t.MaxP99Latency > 0 {
				fmt.Fprintf(w, "  p99_latency < %v\n", t.MaxP99Latency)
			}
			if t.MaxDroppedRate > 0 {
				fmt.Fprintf(w, "  dropped_rate < %g\n", t.MaxDroppedRate)
			}
		}
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "✔ Config is valid. No traffic will be sent.")
}

func writeTextOutput(w io.Writer, result pulse.Result, quiet bool, thresholdFailed bool) {
	if !quiet {
		writeBanner(w)
	}
	writeText(w, result, quiet)
	if quiet {
		if thresholdFailed {
			fmt.Fprintln(w, textStatusThresholdFailed)
		} else {
			fmt.Fprintln(w, textStatusPassed)
		}
		return
	}
	if thresholdFailed {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Thresholds failed. See results above.")
		fmt.Fprintln(w)
		fmt.Fprintln(w, textStatusThresholdFailed)
	} else {
		fmt.Fprintln(w)
		fmt.Fprintln(w, textStatusPassed)
	}
}

func writeText(w io.Writer, result pulse.Result, quiet bool) {
	fmt.Fprintf(w, "Total requests: %d\n", result.Total)
	fmt.Fprintf(w, "Failed requests: %d\n", result.Failed)
	fmt.Fprintf(w, "Duration: %v\n", result.Duration)
	fmt.Fprintf(w, "RPS: %.2f\n", result.RPS)
	if quiet {
		return
	}
	if result.Scheduled > 0 {
		fmt.Fprintf(w, "Scheduled arrivals: %d\n", result.Scheduled)
		fmt.Fprintf(w, "Started requests: %d\n", result.Started)
		fmt.Fprintf(w, "Dropped arrivals: %d (%.2f%%)\n", result.Dropped, result.DroppedRate*100)
		fmt.Fprintf(w, "Completed requests: %d\n", result.Completed)
		fmt.Fprintf(w, "Max active requests: %d\n", result.MaxActive)
	}

	fmt.Fprintf(w, "Min latency: %v\n", result.Latency.Min)
	fmt.Fprintf(w, "P50 latency: %v\n", result.Latency.P50)
	fmt.Fprintf(w, "Mean latency: %v\n", result.Latency.Mean)
	fmt.Fprintf(w, "P90 latency: %v\n", result.Latency.P90)
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

type progressReporter struct {
	w   io.Writer
	tty bool
}

func newProgressReporter(stdout io.Writer, format string) progressReporter {
	if format == "json" {
		return progressReporter{}
	}
	f, ok := stdout.(*os.File)
	if !ok {
		return progressReporter{}
	}
	stat, err := f.Stat()
	if err != nil || stat.Mode()&os.ModeCharDevice == 0 {
		return progressReporter{}
	}
	return progressReporter{w: os.Stderr, tty: true}
}

func (p progressReporter) start() {
	if !p.tty {
		return
	}
	fmt.Fprintln(p.w, "Running...")
}

func (p progressReporter) stop() {
	if !p.tty {
		return
	}
	fmt.Fprintln(p.w, "Done.")
}

func writeJSON(w io.Writer, result pulse.Result, passed bool) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(toJSONResult(result, passed))
}

func toJSONResult(result pulse.Result, passed bool) jsonResult {
	return jsonResult{
		SchemaVersion: 1,
		Summary: jsonSummary{
			Total:       result.Total,
			Failed:      result.Failed,
			RPS:         result.RPS,
			DurationMS:  durationToMillisecondsInt(result.Duration),
			Scheduled:   result.Scheduled,
			Started:     result.Started,
			Dropped:     result.Dropped,
			DroppedRate: result.DroppedRate,
			Completed:   result.Completed,
			MaxActive:   result.MaxActive,
		},
		Latency: jsonLatency{
			MinMS:  durationToMilliseconds(result.Latency.Min),
			P50MS:  durationToMilliseconds(result.Latency.P50),
			MeanMS: durationToMilliseconds(result.Latency.Mean),
			P90MS:  durationToMilliseconds(result.Latency.P90),
			P95MS:  durationToMilliseconds(result.Latency.P95),
			P99MS:  durationToMilliseconds(result.Latency.P99),
			MaxMS:  durationToMilliseconds(result.Latency.Max),
		},
		StatusCodes: toJSONCountMap(result.StatusCounts),
		Errors:      cloneStringCountMap(result.ErrorCounts),
		Thresholds:  toJSONThresholds(result.ThresholdOutcomes),
		Snapshots:   toJSONSnapshots(result.Snapshots),
		Passed:      passed,
	}
}

func toJSONSnapshots(snapshots []pulse.Snapshot) []jsonSnapshot {
	if len(snapshots) == 0 {
		return []jsonSnapshot{}
	}
	result := make([]jsonSnapshot, len(snapshots))
	for i, snapshot := range snapshots {
		result[i] = jsonSnapshot{
			StartedAt: snapshot.StartedAt.Format(time.RFC3339Nano),
			Summary: jsonSummary{
				Total:       snapshot.Total,
				Failed:      snapshot.Failed,
				RPS:         snapshot.RPS,
				DurationMS:  durationToMillisecondsInt(snapshot.Duration),
				Scheduled:   snapshot.Scheduled,
				Started:     snapshot.Started,
				Dropped:     snapshot.Dropped,
				DroppedRate: snapshot.DroppedRate,
				Completed:   snapshot.Completed,
				MaxActive:   snapshot.MaxActive,
			},
			Latency: jsonLatency{
				MinMS:  durationToMilliseconds(snapshot.Latency.Min),
				P50MS:  durationToMilliseconds(snapshot.Latency.P50),
				MeanMS: durationToMilliseconds(snapshot.Latency.Mean),
				P90MS:  durationToMilliseconds(snapshot.Latency.P90),
				P95MS:  durationToMilliseconds(snapshot.Latency.P95),
				P99MS:  durationToMilliseconds(snapshot.Latency.P99),
				MaxMS:  durationToMilliseconds(snapshot.Latency.Max),
			},
			StatusCodes: toJSONCountMap(snapshot.StatusCounts),
			Errors:      cloneStringCountMap(snapshot.ErrorCounts),
		}
	}
	return result
}

func durationToMilliseconds(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}

func durationToMillisecondsInt(d time.Duration) int64 {
	return d.Milliseconds()
}

func toJSONCountMap(counts map[int]int64) map[string]int64 {
	if len(counts) == 0 {
		return map[string]int64{}
	}

	out := make(map[string]int64, len(counts))
	for code, count := range counts {
		out[strconv.Itoa(code)] = count
	}
	return out
}

func cloneStringCountMap(counts map[string]int64) map[string]int64 {
	if len(counts) == 0 {
		return map[string]int64{}
	}

	out := make(map[string]int64, len(counts))
	for key, count := range counts {
		out[key] = count
	}
	return out
}

func toJSONThresholds(outcomes []pulse.ThresholdOutcome) []jsonThreshold {
	if len(outcomes) == 0 {
		return []jsonThreshold{}
	}

	out := make([]jsonThreshold, len(outcomes))
	for i, outcome := range outcomes {
		out[i] = jsonThreshold{
			Description: outcome.Description,
			Pass:        outcome.Pass,
		}
	}
	return out
}

// writeFileAtomic writes to a temporary file in the same directory as path
// and renames it into place when the write function returns nil. This avoids
// truncating the target on error and prevents symlink-based attacks: the temp
// file is created with O_EXCL (via os.CreateTemp) so it is never a symlink.
func writeFileAtomic(path string, write func(io.Writer) error) (retErr error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".pulse-out-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	// Always clean up the temp file on failure.
	defer func() {
		if retErr != nil {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()

	if err := write(tmp); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
