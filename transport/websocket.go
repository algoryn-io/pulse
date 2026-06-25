package transport

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/net/websocket"

	"algoryn.io/pulse/internal/reqmetrics"
)

// WebSocketConfig holds connection settings for a WebSocketClient.
type WebSocketConfig struct {
	// URL is the server endpoint, e.g. "ws://host/path" or "wss://host/path".
	URL string
	// Origin sets the Origin header. When empty it defaults to the http(s) form
	// of the URL's scheme and host (required by the WebSocket handshake).
	Origin string
	// Subprotocol optionally requests a Sec-WebSocket-Protocol.
	Subprotocol string
	// Header carries extra request headers for the handshake (e.g. Authorization).
	Header http.Header
	// TLSConfig configures TLS for wss:// endpoints. Nil uses defaults.
	TLSConfig *tls.Config
}

// WebSocketClient is a thin wrapper over a single WebSocket connection for use
// in Pulse scenarios. It is NOT safe for concurrent use: a connection carries a
// single message stream, so each virtual-user iteration should dial its own
// client (call NewWebSocketClient inside the scenario) or manage a per-goroutine
// pool. Send and Receive feed byte counts into Pulse's throughput metrics; TTFB
// is not measured for WebSocket.
type WebSocketClient struct {
	conn *websocket.Conn
}

// NewWebSocketClient dials the configured endpoint and returns a connected
// client. Close it when done (typically via defer).
func NewWebSocketClient(cfg WebSocketConfig) (*WebSocketClient, error) {
	origin, err := wsOrigin(cfg)
	if err != nil {
		return nil, fmt.Errorf("pulse: websocket origin: %w", err)
	}
	wsCfg, err := websocket.NewConfig(cfg.URL, origin)
	if err != nil {
		return nil, fmt.Errorf("pulse: websocket config %q: %w", cfg.URL, err)
	}
	if cfg.Subprotocol != "" {
		wsCfg.Protocol = []string{cfg.Subprotocol}
	}
	if cfg.TLSConfig != nil {
		wsCfg.TlsConfig = cfg.TLSConfig
	}
	if cfg.Header != nil {
		wsCfg.Header = cfg.Header
	}
	conn, err := websocket.DialConfig(wsCfg)
	if err != nil {
		return nil, fmt.Errorf("pulse: websocket dial %q: %w", cfg.URL, err)
	}
	return &WebSocketClient{conn: conn}, nil
}

// SendText sends msg as a text frame, counting its bytes as outbound throughput.
func (c *WebSocketClient) SendText(ctx context.Context, msg string) error {
	c.applyDeadline(ctx)
	if err := websocket.Message.Send(c.conn, msg); err != nil {
		return err
	}
	reqmetrics.FromContext(ctx).Observe(0, 0, int64(len(msg)))
	return nil
}

// SendBinary sends b as a binary frame, counting its bytes as outbound throughput.
func (c *WebSocketClient) SendBinary(ctx context.Context, b []byte) error {
	c.applyDeadline(ctx)
	if err := websocket.Message.Send(c.conn, b); err != nil {
		return err
	}
	reqmetrics.FromContext(ctx).Observe(0, 0, int64(len(b)))
	return nil
}

// Receive reads the next message (text or binary) and returns its payload,
// counting its bytes as inbound throughput.
func (c *WebSocketClient) Receive(ctx context.Context) ([]byte, error) {
	c.applyDeadline(ctx)
	var data []byte
	if err := websocket.Message.Receive(c.conn, &data); err != nil {
		return nil, err
	}
	reqmetrics.FromContext(ctx).Observe(0, int64(len(data)), 0)
	return data, nil
}

// Roundtrip sends a text message and waits for one reply — the common
// request/response WebSocket pattern. The engine times the whole call as latency.
func (c *WebSocketClient) Roundtrip(ctx context.Context, msg string) ([]byte, error) {
	if err := c.SendText(ctx, msg); err != nil {
		return nil, err
	}
	return c.Receive(ctx)
}

// Close closes the underlying connection.
func (c *WebSocketClient) Close() error {
	return c.conn.Close()
}

// applyDeadline mirrors the context deadline onto the connection so a per-request
// timeout (or run cancellation deadline) bounds Send/Receive. A context with no
// deadline clears any previous one.
func (c *WebSocketClient) applyDeadline(ctx context.Context) {
	if dl, ok := ctx.Deadline(); ok {
		_ = c.conn.SetDeadline(dl)
	} else {
		_ = c.conn.SetDeadline(time.Time{})
	}
}

// CallWebSocket adapts a WebSocket interaction to Pulse's (statusCode, error)
// scenario shape: a nil error maps to 200, an error to 0 (so it counts as a
// failure under the transport/unknown categories).
//
//	scenario := func(ctx context.Context) (int, error) {
//	    return transport.CallWebSocket(func() error {
//	        _, err := ws.Roundtrip(ctx, `{"op":"ping"}`)
//	        return err
//	    })
//	}
func CallWebSocket(fn func() error) (int, error) {
	if err := fn(); err != nil {
		return 0, err
	}
	return 200, nil
}

func wsOrigin(cfg WebSocketConfig) (string, error) {
	if cfg.Origin != "" {
		return cfg.Origin, nil
	}
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return "", err
	}
	scheme := "http"
	if u.Scheme == "wss" {
		scheme = "https"
	}
	return scheme + "://" + u.Host, nil
}
