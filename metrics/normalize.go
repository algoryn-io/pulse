package metrics

import (
	"context"
	"errors"
	"net"

	"algoryn.io/pulse/transport"
)

// normalizeError maps an error to a stable category for ErrorCounts.
// For err == nil it returns "" (caller must not count).
//
// Categories are additive: existing ones are never renamed, but new, more
// specific ones may be introduced over time (e.g. "timeout" and "transport"
// now split out network failures that previously fell into "unknown_error").
// Consumers should treat ErrorCounts as an open-ended map and not assume a
// fixed set of keys.
func normalizeError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "context_canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	// A response check failed: the request completed but the body, headers, or
	// status did not match the configured expectations. Distinct from a server
	// error (http_status_error) and from transport failures.
	if errors.Is(err, transport.ErrCheckFailed) {
		return "check_failed"
	}
	var httpErr *transport.HTTPStatusError
	if errors.As(err, &httpErr) {
		return "http_status_error"
	}
	// Network-level failures: distinguish I/O timeouts (e.g. dial/read deadline
	// exceeded, not driven by the context) from other transport errors such as
	// connection refused or DNS resolution failure.
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return "timeout"
		}
		return "transport"
	}
	return "unknown_error"
}
