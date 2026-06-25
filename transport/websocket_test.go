package transport

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"

	"algoryn.io/pulse/internal/reqmetrics"
)

// wsEchoServer starts an httptest server that echoes every WebSocket message and
// returns a ws:// URL pointing at it.
func wsEchoServer(t *testing.T) (string, func()) {
	t.Helper()
	srv := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		_, _ = io.Copy(ws, ws)
	}))
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	return url, srv.Close
}

func TestWebSocketRoundtripAndByteMetrics(t *testing.T) {
	url, stop := wsEchoServer(t)
	defer stop()

	c, err := NewWebSocketClient(WebSocketConfig{URL: url})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	ctx, sample := reqmetrics.NewContext(context.Background())
	reply, err := c.Roundtrip(ctx, "ping")
	if err != nil {
		t.Fatalf("roundtrip: %v", err)
	}
	if string(reply) != "ping" {
		t.Fatalf("reply = %q, want ping", reply)
	}
	if sample.BytesOut() != 4 {
		t.Errorf("BytesOut = %d, want 4", sample.BytesOut())
	}
	if sample.BytesIn() != 4 {
		t.Errorf("BytesIn = %d, want 4", sample.BytesIn())
	}
}

func TestWebSocketSendBinary(t *testing.T) {
	url, stop := wsEchoServer(t)
	defer stop()
	c, err := NewWebSocketClient(WebSocketConfig{URL: url})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	if err := c.SendBinary(ctx, []byte{1, 2, 3, 4, 5}); err != nil {
		t.Fatalf("send binary: %v", err)
	}
	got, err := c.Receive(ctx)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("got %d bytes, want 5", len(got))
	}
}

func TestWebSocketDialError(t *testing.T) {
	_, err := NewWebSocketClient(WebSocketConfig{URL: "ws://127.0.0.1:1/nope"})
	if err == nil {
		t.Fatal("expected a dial error")
	}
}

func TestWebSocketReceiveRespectsDeadline(t *testing.T) {
	// Server that accepts the connection but never sends anything.
	srv := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		<-make(chan struct{}) // block forever
	}))
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http")

	c, err := NewWebSocketClient(WebSocketConfig{URL: url})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := c.Receive(ctx); err == nil {
		t.Fatal("expected a timeout error")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("receive did not honor the deadline: took %v", time.Since(start))
	}
}

func TestWebSocketCall(t *testing.T) {
	if code, err := CallWebSocket(func() error { return nil }); code != 200 || err != nil {
		t.Fatalf("success: code=%d err=%v", code, err)
	}
	if code, _ := CallWebSocket(func() error { return io.EOF }); code != 0 {
		t.Fatalf("error: code=%d, want 0", code)
	}
}

func TestWSOrigin(t *testing.T) {
	cases := map[string]string{
		"ws://h/p":         "http://h",
		"wss://h:443/p":    "https://h:443",
	}
	for in, want := range cases {
		got, err := wsOrigin(WebSocketConfig{URL: in})
		if err != nil || got != want {
			t.Errorf("wsOrigin(%q) = %q,%v want %q", in, got, err, want)
		}
	}
	if got, _ := wsOrigin(WebSocketConfig{URL: "ws://x", Origin: "http://custom"}); got != "http://custom" {
		t.Errorf("explicit origin ignored: %q", got)
	}
}
