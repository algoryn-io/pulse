// Package worker implements the Pulse distributed worker HTTP server.
// A worker listens for RunRequests from a coordinator, executes the load test,
// and returns a WorkerResult with full histogram data for accurate latency merging.
//
// Usage (library mode — pre-registered scenario):
//
//	srv := worker.New(myScenario)
//	if err := srv.ListenAndServe(ctx, ":9100"); err != nil {
//	    log.Fatal(err)
//	}
//
// Usage (CLI mode — scenario built from RunRequest.HTTPScenario):
//
//	srv := worker.New(nil)
//	if err := srv.ListenAndServe(ctx, ":9100"); err != nil {
//	    log.Fatal(err)
//	}
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"algoryn.io/pulse/distributed"
	"algoryn.io/pulse/engine"
	"algoryn.io/pulse/model"
	"algoryn.io/pulse/scheduler"
	"algoryn.io/pulse/transport"
)

// Server is a Pulse distributed worker. It accepts RunRequests from a
// coordinator over HTTP and executes the load test locally.
type Server struct {
	// scenario is the pre-registered scenario for library mode. When nil,
	// the server builds an HTTP scenario from RunRequest.HTTPScenario.
	scenario func(context.Context) (int, error)
}

// New creates a worker server. Pass a non-nil scenario for library mode;
// pass nil for CLI mode (scenario is derived from each RunRequest).
func New(scenario func(context.Context) (int, error)) *Server {
	return &Server{scenario: scenario}
}

// ListenAndServe starts the worker HTTP server on addr and blocks until ctx
// is cancelled or a fatal listen error occurs. addr must be a "host:port" string.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", s.handlePing)
	mux.HandleFunc("/run", s.handleRun)

	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("pulse worker: listen %s: %w", addr, err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(ln)
	}()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
		return ctx.Err()
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("pulse worker: serve: %w", err)
	}
}

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, distributed.PingResponse{Status: "ok"}, http.StatusOK)
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req distributed.RunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}

	scenario, err := s.resolveScenario(req.HTTPScenario)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	phases, err := toSchedulerPhases(req.Phases)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	eng := engine.NewWithOptions(phases, scenario, engine.Options{
		MaxConcurrency: req.MaxConcurrency,
		Saturation:     engine.SaturationPolicy(req.SaturationPolicy),
	})

	result, _ := eng.Run(r.Context())

	// Convert map[int]int64 status counts to string-keyed map for JSON wire format.
	statusCounts := make(map[string]int64, len(result.StatusCounts))
	for code, count := range result.StatusCounts {
		statusCounts[strconv.Itoa(code)] = count
	}

	workerResult := distributed.WorkerResult{
		Total:       result.Total,
		Failed:      result.Failed,
		Duration:    result.Duration,
		Scheduled:   result.Scheduled,
		Started:     result.Started,
		Dropped:     result.Dropped,
		DroppedRate: result.DroppedRate,
		Completed:   result.Completed,
		MaxActive:   result.MaxActive,
		Latency: distributed.LatencyStats{
			Min:  result.Latency.Min,
			Mean: result.Latency.Mean,
			P50:  result.Latency.P50,
			P90:  result.Latency.P90,
			P95:  result.Latency.P95,
			P99:  result.Latency.P99,
			Max:  result.Latency.Max,
		},
		StatusCounts: statusCounts,
		ErrorCounts:  result.ErrorCounts,
		Buckets:      result.Buckets,
	}

	writeJSON(w, workerResult, http.StatusOK)
}

// resolveScenario returns the scenario to execute. It prefers the pre-registered
// scenario over the HTTP scenario config from the RunRequest.
func (s *Server) resolveScenario(httpCfg *distributed.HTTPScenario) (func(context.Context) (int, error), error) {
	if s.scenario != nil {
		return s.scenario, nil
	}
	if httpCfg == nil {
		return nil, fmt.Errorf("pulse worker: no scenario registered and RunRequest.HTTPScenario is nil")
	}
	return buildHTTPScenario(httpCfg), nil
}

// buildHTTPScenario constructs a scenario function from an HTTPScenario config.
func buildHTTPScenario(cfg *distributed.HTTPScenario) func(context.Context) (int, error) {
	client := transport.NewHTTPClientWith(transport.HTTPClientConfig{
		Headers: cfg.Headers,
	})
	method := strings.ToUpper(cfg.Method)
	if method == "" {
		method = http.MethodGet
	}
	url := cfg.URL
	body := cfg.Body

	return func(ctx context.Context) (int, error) {
		var bodyReader io.Reader
		if body != "" {
			bodyReader = strings.NewReader(body)
		}
		return client.Do(ctx, method, url, bodyReader)
	}
}

func toSchedulerPhases(phases []distributed.Phase) ([]scheduler.Phase, error) {
	out := make([]scheduler.Phase, len(phases))
	for i, p := range phases {
		pt := model.PhaseType(strings.TrimSpace(p.Type))
		if pt == "" {
			return nil, fmt.Errorf("pulse worker: phase %d: type is required", i)
		}
		out[i] = scheduler.Phase{
			Type:          pt,
			Duration:      p.Duration,
			ArrivalRate:   p.ArrivalRate,
			From:          p.From,
			To:            p.To,
			Steps:         p.Steps,
			SpikeAt:       p.SpikeAt,
			SpikeDuration: p.SpikeDuration,
		}
	}
	return out, nil
}

func writeJSON(w http.ResponseWriter, v any, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}
