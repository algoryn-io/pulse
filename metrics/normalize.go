package metrics

import (
	"context"
	"errors"
	"net"

	"algoryn.io/pulse/transport"
)

// ErrUser marks an error as originating from scenario/user code — a business-rule
// violation, bad test data, client-side validation, or any failure that is the
// test's responsibility rather than the target's. Wrap a scenario error with it
// (most easily via pulse.UserError) so it is counted under the "user_error"
// category instead of "unknown_error". Detect it with errors.Is.
var ErrUser = errors.New("pulse: user error")

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
	// An explicit user/scenario error. Checked before the transport/status
	// classifications so a deliberately marked error wins even when it wraps a
	// network or status error.
	if errors.Is(err, ErrUser) {
		return "user_error"
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
