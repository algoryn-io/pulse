// Package coordinator implements the Pulse distributed coordinator.
// It fans out RunRequests to a set of worker addresses, waits for all workers
// to complete, and merges their WorkerResults into a single metrics.Result.
package coordinator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"algoryn.io/pulse/distributed"
	"algoryn.io/pulse/distributed/merger"
	"algoryn.io/pulse/metrics"
)

// Coordinator fans out load-test runs to multiple workers and merges results.
type Coordinator struct {
	workers   []string // "host:port" worker addresses
	client    *http.Client
	authToken string
	weights   []int // per-worker capacity weights; nil means equal weighting
}

// Options configures a Coordinator.
type Options struct {
	// AuthToken, when non-empty, is sent to every worker as
	// "Authorization: Bearer <token>". It must match the worker's configured
	// token (see worker.Options.AuthToken).
	AuthToken string

	// Weights optionally assigns a relative capacity to each worker, in the same
	// order as the worker addresses. Arrival rate and concurrency are split
	// proportionally (e.g. weights {2,1} send a 2:1 share to the first worker).
	// When nil, or when its length does not match the worker count, or when any
	// value is non-positive, all workers are weighted equally.
	Weights []int
}

// New creates a Coordinator that will distribute runs to the given worker addresses.
// Each address must be in "host:port" format.
func New(workers []string) *Coordinator {
	return NewWithOptions(workers, Options{})
}

// NewWithOptions creates a Coordinator with the given options, e.g. an auth
// token shared with the worker fleet.
func NewWithOptions(workers []string, opts Options) *Coordinator {
	return &Coordinator{
		workers: workers,
		client: &http.Client{
			// No global timeout — runs can take minutes. Context cancellation is used instead.
			Timeout: 0,
		},
		authToken: opts.AuthToken,
		weights:   normalizeWeights(opts.Weights, len(workers)),
	}
}

// normalizeWeights returns a valid per-worker weight slice of length n. It
// returns nil (meaning "equal weighting") when the supplied weights are absent,
// the wrong length, or contain a non-positive value.
func normalizeWeights(weights []int, n int) []int {
	if len(weights) != n {
		return nil
	}
	for _, w := range weights {
		if w <= 0 {
			return nil
		}
	}
	out := make([]int, n)
	copy(out, weights)
	return out
}

// Ping checks that all workers are reachable and returns an error listing any
// that did not respond with {"status":"ok"}. Use before Run to validate the
// worker fleet before committing to a long load test.
func (c *Coordinator) Ping(ctx context.Context) error {
	type result struct {
		addr string
		err  error
	}

	ch := make(chan result, len(c.workers))
	for _, addr := range c.workers {
		go func(addr string) {
			ch <- result{addr: addr, err: c.ping(ctx, addr)}
		}(addr)
	}

	var errs []string
	for range c.workers {
		r := <-ch
		if r.err != nil {
			errs = append(errs, fmt.Sprintf("  %s: %v", r.addr, r.err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("pulse coordinator: unreachable workers:\n%s", strings.Join(errs, "\n"))
	}
	return nil
}

func (c *Coordinator) ping(ctx context.Context, addr string) error {
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(pingCtx, http.MethodGet, workerURL(addr, "/ping"), nil)
	if err != nil {
		return err
	}
	c.setAuth(req)
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// Run fans out req to all workers concurrently, waits for all to complete, and
// returns the merged metrics.Result. Arrival rate and concurrency are split
// across workers proportionally to their configured weights (equal by default).
// If any worker fails, the merged result reflects only the workers that
// succeeded and the returned error joins every worker failure so none are
// hidden.
func (c *Coordinator) Run(ctx context.Context, req distributed.RunRequest) (metrics.Result, error) {
	type outcome struct {
		result distributed.WorkerResult
		err    error
	}

	reqs := splitRates(req, c.weights, len(c.workers))

	ch := make(chan outcome, len(c.workers))
	for i, addr := range c.workers {
		go func(addr string, workerReq distributed.RunRequest) {
			result, err := c.runWorker(ctx, addr, workerReq)
			ch <- outcome{result: result, err: err}
		}(addr, reqs[i])
	}

	var results []distributed.WorkerResult
	var errs []error
	for range c.workers {
		o := <-ch
		if o.err != nil {
			errs = append(errs, o.err)
		} else {
			results = append(results, o.result)
		}
	}

	merged := merger.Merge(results)
	if len(errs) > 0 {
		// Prefix a summary so callers can see how much of the fleet failed
		// before the individual errors.
		summary := fmt.Errorf("pulse coordinator: %d of %d workers failed", len(errs), len(c.workers))
		return merged, errors.Join(append([]error{summary}, errs...)...)
	}
	return merged, nil
}

func (c *Coordinator) runWorker(ctx context.Context, addr string, req distributed.RunRequest) (distributed.WorkerResult, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return distributed.WorkerResult{}, fmt.Errorf("pulse coordinator: marshal RunRequest: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, workerURL(addr, "/run"), bytes.NewReader(body))
	if err != nil {
		return distributed.WorkerResult{}, fmt.Errorf("pulse coordinator: build request to %s: %w", addr, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	c.setAuth(httpReq)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return distributed.WorkerResult{}, fmt.Errorf("pulse coordinator: POST /run to %s: %w", addr, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return distributed.WorkerResult{}, fmt.Errorf("pulse coordinator: worker %s returned HTTP %d", addr, resp.StatusCode)
	}

	var result distributed.WorkerResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return distributed.WorkerResult{}, fmt.Errorf("pulse coordinator: decode WorkerResult from %s: %w", addr, err)
	}
	return result, nil
}

// splitRates returns one RunRequest per worker. Arrival rate, ramp endpoints,
// and concurrency are divided across workers proportionally to weights (equal
// weighting when weights is nil) using the largest-remainder method, so the
// exact total is preserved and any remainder is spread fairly across workers
// rather than dumped on the first one.
func splitRates(req distributed.RunRequest, weights []int, n int) []distributed.RunRequest {
	if n <= 0 {
		return nil
	}
	if weights == nil {
		weights = make([]int, n)
		for i := range weights {
			weights[i] = 1
		}
	}

	maxConc := splitInt(req.MaxConcurrency, weights)

	// Pre-split each phase field across workers so every worker's slice is built
	// from the same proportional division.
	type phaseSplit struct {
		arrival, from, to []int
	}
	splits := make([]phaseSplit, len(req.Phases))
	for j, p := range req.Phases {
		splits[j] = phaseSplit{
			arrival: splitInt(p.ArrivalRate, weights),
			from:    splitInt(p.From, weights),
			to:      splitInt(p.To, weights),
		}
	}

	reqs := make([]distributed.RunRequest, n)
	for i := range reqs {
		phases := make([]distributed.Phase, len(req.Phases))
		for j, p := range req.Phases {
			phases[j] = distributed.Phase{
				Type:          p.Type,
				Duration:      p.Duration,
				ArrivalRate:   splits[j].arrival[i],
				From:          splits[j].from[i],
				To:            splits[j].to[i],
				Steps:         p.Steps,
				SpikeAt:       p.SpikeAt,
				SpikeDuration: p.SpikeDuration,
			}
		}
		reqs[i] = distributed.RunRequest{
			Phases:           phases,
			MaxConcurrency:   maxConc[i],
			SaturationPolicy: req.SaturationPolicy,
			HTTPScenario:     req.HTTPScenario,
		}
	}
	return reqs
}

// splitInt divides total across len(weights) buckets proportionally to weights
// using the largest-remainder (Hamilton) method: each bucket gets the floor of
// its proportional share, and the leftover units (total minus the sum of floors)
// are handed out one each to the buckets with the largest fractional remainders.
// The returned slice always sums to total. With equal weights this spreads the
// remainder across the first buckets instead of concentrating it on one.
func splitInt(total int, weights []int) []int {
	n := len(weights)
	out := make([]int, n)
	if n == 0 || total <= 0 {
		return out
	}
	sum := 0
	for _, w := range weights {
		sum += w
	}
	if sum <= 0 {
		return out
	}

	remainders := make([]int, n) // fractional parts scaled by sum
	assigned := 0
	for i, w := range weights {
		share := total * w
		out[i] = share / sum
		remainders[i] = share % sum
		assigned += out[i]
	}

	// Order indices by remainder descending, ties broken by lower index, then
	// give one leftover unit to each of the first `leftover` indices.
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return remainders[order[a]] > remainders[order[b]]
	})
	leftover := total - assigned
	for k := 0; k < leftover && k < n; k++ {
		out[order[k]]++
	}
	return out
}

// setAuth attaches the bearer token to req when one is configured.
func (c *Coordinator) setAuth(req *http.Request) {
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}
}

func workerURL(addr, path string) string {
	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		addr = "http://" + addr
	}
	return strings.TrimRight(addr, "/") + path
}

