// Package dashboard provides a live HTTP dashboard that streams Pulse metrics
// to a browser in real time using Server-Sent Events (SSE).
//
// Usage:
//
//	srv := dashboard.New()
//	go srv.ListenAndServe(ctx, ":9090")
//
//	// Wire into pulse.Config:
//	cfg.OnSnapshot = func(s pulse.Snapshot) { srv.Push(s) }
//	cfg.OnResult   = func(r pulse.Result, passed bool) { srv.Complete(r, passed) }
package dashboard

import (
	"context"
	"encoding/json"
	_ "embed"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

//go:embed index.html
var indexHTML []byte

// Server is a lightweight HTTP server that serves a live metrics dashboard.
// Metrics are pushed to all connected browsers via SSE. It is safe to call
// Push and Complete from multiple goroutines.
type Server struct {
	mu      sync.RWMutex
	clients map[chan []byte]struct{}
}

// New creates a new dashboard Server.
func New() *Server {
	return &Server{
		clients: make(map[chan []byte]struct{}),
	}
}

// ListenAndServe starts the HTTP server on addr and blocks until ctx is
// cancelled or an unrecoverable error occurs. The error returned is non-nil
// only if the server fails to bind or start; context cancellation is not
// treated as an error.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/events", s.handleSSE)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("dashboard: listen %s: %w", addr, err)
	}

	srv := &http.Server{
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 0, // SSE connections are long-lived; no write deadline.
		IdleTimeout:  120 * time.Second,
	}

	// Shut down when the context is cancelled.
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("dashboard: serve: %w", err)
	}
	return nil
}

// Push broadcasts a snapshot to all connected SSE clients. It must not block;
// clients that cannot receive within 100ms are skipped.
func (s *Server) Push(snap any) {
	data, err := json.Marshal(snap)
	if err != nil {
		log.Printf("dashboard: marshal snapshot: %v", err)
		return
	}
	msg := buildSSEMessage("snapshot", data)
	s.broadcast(msg)
}

// Complete sends the final result to all SSE clients and closes the stream.
func (s *Server) Complete(result any, passed bool) {
	type donePayload struct {
		Passed bool `json:"passed"`
		// Embed the result fields by marshalling result separately and merging.
	}
	// Merge result + passed into a single JSON object.
	resultBytes, err := json.Marshal(result)
	if err != nil {
		log.Printf("dashboard: marshal result: %v", err)
		return
	}
	var m map[string]any
	if err := json.Unmarshal(resultBytes, &m); err != nil {
		log.Printf("dashboard: unmarshal result: %v", err)
		return
	}
	m["passed"] = passed

	data, err := json.Marshal(m)
	if err != nil {
		log.Printf("dashboard: marshal done payload: %v", err)
		return
	}
	msg := buildSSEMessage("done", data)
	s.broadcast(msg)
}

// ── SSE handlers ─────────────────────────────────────────────────────────────

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(indexHTML)
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	// Only GET is allowed.
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Verify the client supports streaming.
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Set SSE headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering
	w.WriteHeader(http.StatusOK)

	// Send a comment to establish the connection and flush immediately.
	_, _ = fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	// Register this client.
	ch := make(chan []byte, 64)
	s.register(ch)
	defer s.deregister(ch)

	// Stream events until the client disconnects or the request context ends.
	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if _, err := w.Write(msg); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// ── Client registry ───────────────────────────────────────────────────────────

func (s *Server) register(ch chan []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients[ch] = struct{}{}
}

func (s *Server) deregister(ch chan []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.clients, ch)
}

func (s *Server) broadcast(msg []byte) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for ch := range s.clients {
		select {
		case ch <- msg:
		default:
			// Slow client — drop this event rather than blocking.
		}
	}
}

// ── SSE framing ───────────────────────────────────────────────────────────────

// buildSSEMessage returns a properly framed SSE message:
//
//	event: <name>\n
//	data: <payload>\n
//	\n
func buildSSEMessage(event string, data []byte) []byte {
	msg := make([]byte, 0, 8+len(event)+7+len(data)+2)
	msg = append(msg, "event: "...)
	msg = append(msg, event...)
	msg = append(msg, '\n')
	msg = append(msg, "data: "...)
	msg = append(msg, data...)
	msg = append(msg, '\n', '\n')
	return msg
}
