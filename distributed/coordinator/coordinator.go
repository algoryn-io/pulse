// Package coordinator implements the Pulse distributed coordinator.
// It fans out RunRequests to a set of worker addresses, waits for all workers
// to complete, and merges their WorkerResults into a single metrics.Result.
package coordinator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"algoryn.io/pulse/distributed"
	"algoryn.io/pulse/distributed/merger"
	"algoryn.io/pulse/metrics"
)

// Coordinator fans out load-test runs to multiple workers and merges results.
type Coordinator struct {
	workers []string // "host:port" worker addresses
	client  *http.Client
}

// New creates a Coordinator that will distribute runs to the given worker addresses.
// Each address must be in "host:port" format.
func New(workers []string) *Coordinator {
	return &Coordinator{
		workers: workers,
		client: &http.Client{
			// No global timeout — runs can take minutes. Context cancellation is used instead.
			Timeout: 0,
		},
	}
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
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// Run fans out req to all workers concurrently, waits for all to complete, and
// returns the merged metrics.Result. If any worker fails, the first error is
// returned alongside any partial results from workers that succeeded.
func (c *Coordinator) Run(ctx context.Context, req distributed.RunRequest) (metrics.Result, error) {
	type outcome struct {
		result distributed.WorkerResult
		err    error
	}

	// Distribute arrival rates evenly; remainder goes to the first worker.
	reqs := splitRates(req, len(c.workers))

	ch := make(chan outcome, len(c.workers))
	for i, addr := range c.workers {
		go func(addr string, workerReq distributed.RunRequest) {
			result, err := c.runWorker(ctx, addr, workerReq)
			ch <- outcome{result: result, err: err}
		}(addr, reqs[i])
	}

	var results []distributed.WorkerResult
	var firstErr error
	for range c.workers {
		o := <-ch
		if o.err != nil {
			if firstErr == nil {
				firstErr = o.err
			}
		} else {
			results = append(results, o.result)
		}
	}

	merged := merger.Merge(results)
	return merged, firstErr
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

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return distributed.WorkerResult{}, fmt.Errorf("pulse coordinator: POST /run to %s: %w", addr, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return distributed.WorkerResult{}, fmt.Errorf("pulse coordinator: worker %s returned HTTP %d", addr, resp.StatusCode)
	}

	var result distributed.WorkerResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return distributed.WorkerResult{}, fmt.Errorf("pulse coordinator: decode WorkerResult from %s: %w", addr, err)
	}
	return result, nil
}

// splitRates returns one RunRequest per worker. Each worker gets total/N
// arrival rate per phase; the first worker absorbs the remainder from integer
// division so the exact total rate is preserved.
func splitRates(req distributed.RunRequest, n int) []distributed.RunRequest {
	if n <= 0 {
		return nil
	}
	reqs := make([]distributed.RunRequest, n)
	for i := range reqs {
		phases := make([]distributed.Phase, len(req.Phases))
		for j, p := range req.Phases {
			base := p.ArrivalRate / n
			from := p.From / n
			to := p.To / n
			if i == 0 {
				// First worker gets the remainder.
				base += p.ArrivalRate % n
				from += p.From % n
				to += p.To % n
			}
			phases[j] = distributed.Phase{
				Type:          p.Type,
				Duration:      p.Duration,
				ArrivalRate:   base,
				From:          from,
				To:            to,
				Steps:         p.Steps,
				SpikeAt:       p.SpikeAt,
				SpikeDuration: p.SpikeDuration,
			}
		}
		reqs[i] = distributed.RunRequest{
			Phases:           phases,
			MaxConcurrency:   req.MaxConcurrency / n,
			SaturationPolicy: req.SaturationPolicy,
			HTTPScenario:     req.HTTPScenario,
		}
	}
	return reqs
}

func workerURL(addr, path string) string {
	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		addr = "http://" + addr
	}
	return strings.TrimRight(addr, "/") + path
}

