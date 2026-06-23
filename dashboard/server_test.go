package dashboard

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── buildSSEMessage ──────────────────────────────────────────────────────────

func TestBuildSSEMessage(t *testing.T) {
	msg := buildSSEMessage("snapshot", []byte(`{"rps":42}`))
	got := string(msg)
	want := "event: snapshot\ndata: {\"rps\":42}\n\n"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

// ── handleIndex ──────────────────────────────────────────────────────────────

func TestHandleIndex_ServesHTML(t *testing.T) {
	srv := New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	srv.handleIndex(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Fatalf("expected text/html content-type, got %q", ct)
	}
	if !strings.Contains(w.Body.String(), "<title>") {
		t.Fatal("expected HTML body, got something else")
	}
}

func TestHandleIndex_NotFound(t *testing.T) {
	srv := New()
	req := httptest.NewRequest(http.MethodGet, "/unknown", nil)
	w := httptest.NewRecorder()
	srv.handleIndex(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// ── SSE broadcast ────────────────────────────────────────────────────────────

func TestPush_BroadcastsToConnectedClients(t *testing.T) {
	srv := New()

	// Register a fake SSE client.
	ch := make(chan []byte, 16)
	srv.register(ch)
	defer srv.deregister(ch)

	type snapshot struct {
		RPS float64 `json:"rps"`
	}
	srv.Push(snapshot{RPS: 123.4})

	select {
	case msg := <-ch:
		got := string(msg)
		if !strings.Contains(got, "event: snapshot") {
			t.Fatalf("expected snapshot event, got %q", got)
		}
		if !strings.Contains(got, "123.4") {
			t.Fatalf("expected RPS in payload, got %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for broadcast")
	}
}

func TestComplete_SendsDoneEvent(t *testing.T) {
	srv := New()

	ch := make(chan []byte, 16)
	srv.register(ch)
	defer srv.deregister(ch)

	type result struct {
		Total int64 `json:"total"`
	}
	srv.Complete(result{Total: 500}, true)

	select {
	case msg := <-ch:
		got := string(msg)
		if !strings.Contains(got, "event: done") {
			t.Fatalf("expected done event, got %q", got)
		}
		if !strings.Contains(got, "500") {
			t.Fatalf("expected total in payload, got %q", got)
		}
		if !strings.Contains(got, `"passed":true`) {
			t.Fatalf("expected passed=true in payload, got %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for done event")
	}
}

func TestBroadcast_SlowClientDropped(t *testing.T) {
	srv := New()

	// Full channel — broadcast should not block.
	ch := make(chan []byte) // unbuffered, always full
	srv.register(ch)
	defer srv.deregister(ch)

	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.Push(struct{ X int }{X: 1})
	}()

	select {
	case <-done:
		// Good — Push returned without blocking.
	case <-time.After(time.Second):
		t.Fatal("Push blocked on slow client")
	}
}

func TestBroadcast_ConcurrentClients(t *testing.T) {
	srv := New()
	const n = 50

	channels := make([]chan []byte, n)
	for i := range channels {
		channels[i] = make(chan []byte, 8)
		srv.register(channels[i])
	}
	defer func() {
		for _, ch := range channels {
			srv.deregister(ch)
		}
	}()

	type snap struct{ RPS float64 }
	srv.Push(snap{RPS: 99.9})

	var wg sync.WaitGroup
	for _, ch := range channels {
		ch := ch
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case msg := <-ch:
				if !strings.Contains(string(msg), "99.9") {
					t.Errorf("missing payload in message: %q", msg)
				}
			case <-time.After(time.Second):
				t.Errorf("client timed out")
			}
		}()
	}
	wg.Wait()
}

// ── ListenAndServe ───────────────────────────────────────────────────────────

func TestListenAndServe_RespondsToGET(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv := New()
	ready := make(chan string, 1)

	go func() {
		// Pick a random port.
		_ = srv.ListenAndServe(ctx, "127.0.0.1:0")
	}()

	// Use an httptest server to simulate the HTTP layer without a real listener.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv.handleIndex(w, r)
	}))
	defer ts.Close()
	ready <- ts.URL

	baseURL := <-ready
	resp, err := http.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

// ── SSE stream (end-to-end via httptest) ─────────────────────────────────────

func TestSSEStream_ReceivesSnapshot(t *testing.T) {
	srv := New()
	ts := httptest.NewServer(http.HandlerFunc(srv.handleSSE))
	defer ts.Close()

	// Connect SSE client.
	req, _ := http.NewRequestWithContext(context.Background(), "GET", ts.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /events: %v", err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("expected text/event-stream, got %q", ct)
	}

	// Push a snapshot after a brief delay (ensures the client is reading).
	time.AfterFunc(50*time.Millisecond, func() {
		srv.Push(struct{ RPS float64 }{RPS: 77.7})
	})

	// Read lines until we find the snapshot event.
	found := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, "77.7") {
				found <- true
				return
			}
		}
		found <- false
	}()

	select {
	case ok := <-found:
		if !ok {
			t.Fatal("snapshot payload not received in SSE stream")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for snapshot in SSE stream")
	}
}
