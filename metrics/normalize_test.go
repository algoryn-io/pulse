package metrics

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"testing"

	"algoryn.io/pulse/transport"
)

// fakeNetError implements net.Error with a configurable Timeout result.
type fakeNetError struct {
	msg     string
	timeout bool
}

func (e *fakeNetError) Error() string   { return e.msg }
func (e *fakeNetError) Timeout() bool   { return e.timeout }
func (e *fakeNetError) Temporary() bool { return false }

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
		{name: "check failed", err: transport.ErrCheckFailed, want: "check_failed"},
		{name: "wrapped check failed", err: fmt.Errorf("assertion failed: %w", transport.ErrCheckFailed), want: "check_failed"},
		{name: "user error", err: ErrUser, want: "user_error"},
		{name: "wrapped user error", err: fmt.Errorf("bad fixture: %w", ErrUser), want: "user_error"},
		{name: "user error wins over net error", err: fmt.Errorf("%w: %w", ErrUser, &fakeNetError{msg: "i/o timeout", timeout: true}), want: "user_error"},
		{name: "http status error", err: &transport.HTTPStatusError{StatusCode: 503}, want: "http_status_error"},
		{name: "wrapped http status error", err: fmt.Errorf("client: %w", &transport.HTTPStatusError{StatusCode: 404}), want: "http_status_error"},
		{name: "net timeout", err: &fakeNetError{msg: "i/o timeout", timeout: true}, want: "timeout"},
		{name: "net timeout wrapped in url.Error", err: &url.Error{Op: "Get", URL: "http://x", Err: &fakeNetError{msg: "i/o timeout", timeout: true}}, want: "timeout"},
		{name: "net non-timeout (transport)", err: &fakeNetError{msg: "connection refused", timeout: false}, want: "transport"},
		{name: "real DNS error", err: &net.OpError{Op: "dial", Err: &net.DNSError{Err: "no such host"}}, want: "transport"},
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
