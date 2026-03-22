package metrics

import (
	"context"
	"errors"

	"github.com/jmgo38/Pulse/transport"
)

// normalizeError maps an error to a stable category for ErrorCounts.
// For err == nil it returns "" (caller must not count).
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
	var httpErr *transport.HTTPStatusError
	if errors.As(err, &httpErr) {
		return "http_status_error"
	}
	return "unknown_error"
}
