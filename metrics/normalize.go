package metrics

import (
	"context"
	"errors"
	"strings"
)

// Prefix must match transport.HTTPClient when status is >= 400.
const transportHTTPStatusErrPrefix = "transport: unexpected status code:"

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
	if strings.HasPrefix(err.Error(), transportHTTPStatusErrPrefix) {
		return "http_status_error"
	}
	return "unknown_error"
}
