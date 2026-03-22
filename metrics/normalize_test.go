package metrics

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jmgo38/Pulse/transport"
)

func TestNormalizeError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "nil", err: nil, want: ""},
		{name: "canceled", err: context.Canceled, want: "context_canceled"},
		{name: "wrapped canceled", err: fmt.Errorf("wrap: %w", context.Canceled), want: "context_canceled"},
		{name: "deadline", err: context.DeadlineExceeded, want: "deadline_exceeded"},
		{name: "wrapped deadline", err: fmt.Errorf("wrap: %w", context.DeadlineExceeded), want: "deadline_exceeded"},
		{name: "http status error", err: &transport.HTTPStatusError{StatusCode: 503}, want: "http_status_error"},
		{name: "wrapped http status error", err: fmt.Errorf("client: %w", &transport.HTTPStatusError{StatusCode: 404}), want: "http_status_error"},
		{name: "arbitrary", err: errors.New("boom"), want: "unknown_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeError(tt.err); got != tt.want {
				t.Fatalf("normalizeError(%v): want %q, got %q", tt.err, tt.want, got)
			}
		})
	}
}
