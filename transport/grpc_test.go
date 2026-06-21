package transport

import (
	"errors"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// -- grpcCodeToHTTP --

func TestGRPCCodeToHTTP(t *testing.T) {
	cases := []struct {
		code codes.Code
		want int
	}{
		{codes.OK, 200},
		{codes.Canceled, 499},
		{codes.Unknown, 500},
		{codes.InvalidArgument, 400},
		{codes.DeadlineExceeded, 504},
		{codes.NotFound, 404},
		{codes.AlreadyExists, 409},
		{codes.PermissionDenied, 403},
		{codes.Unauthenticated, 401},
		{codes.ResourceExhausted, 429},
		{codes.FailedPrecondition, 400},
		{codes.Aborted, 409},
		{codes.OutOfRange, 400},
		{codes.Unimplemented, 501},
		{codes.Internal, 500},
		{codes.Unavailable, 503},
		{codes.DataLoss, 500},
	}
	for _, tc := range cases {
		got := grpcCodeToHTTP(tc.code)
		if got != tc.want {
			t.Errorf("grpcCodeToHTTP(%v) = %d, want %d", tc.code, got, tc.want)
		}
	}
}

// -- CallGRPC --

func TestCallGRPCReturns200OnNilError(t *testing.T) {
	code, err := CallGRPC(func() error { return nil })
	if err != nil || code != 200 {
		t.Fatalf("expected (200, nil), got (%d, %v)", code, err)
	}
}

func TestCallGRPCMapsGRPCStatusError(t *testing.T) {
	grpcErr := status.Error(codes.NotFound, "not found")
	code, err := CallGRPC(func() error { return grpcErr })
	if !errors.Is(err, grpcErr) {
		t.Fatalf("expected original error, got %v", err)
	}
	if code != 404 {
		t.Fatalf("expected 404 for NotFound, got %d", code)
	}
}

func TestCallGRPCMapsUnauthenticated(t *testing.T) {
	code, _ := CallGRPC(func() error {
		return status.Error(codes.Unauthenticated, "invalid token")
	})
	if code != 401 {
		t.Fatalf("expected 401, got %d", code)
	}
}

func TestCallGRPCMapsResourceExhausted(t *testing.T) {
	code, _ := CallGRPC(func() error {
		return status.Error(codes.ResourceExhausted, "rate limited")
	})
	if code != 429 {
		t.Fatalf("expected 429, got %d", code)
	}
}

func TestCallGRPCMapsNonGRPCErrorToUnknown(t *testing.T) {
	// A plain Go error has no gRPC status — status.Code returns Unknown → 500
	code, _ := CallGRPC(func() error { return errors.New("connection refused") })
	if code != 500 {
		t.Fatalf("expected 500 for plain error, got %d", code)
	}
}

// -- NewGRPCClient --

func TestNewGRPCClientInsecureConnects(t *testing.T) {
	// Start a bare gRPC server on a random port
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	go srv.Serve(lis) //nolint:errcheck
	defer srv.Stop()

	client, err := NewGRPCClient(GRPCClientConfig{
		Target:   lis.Addr().String(),
		Insecure: true,
	})
	if err != nil {
		t.Fatalf("NewGRPCClient: %v", err)
	}
	defer client.Close()

	if client.Conn() == nil {
		t.Fatal("expected non-nil Conn()")
	}
}

func TestNewGRPCClientConnReturnsConn(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	go srv.Serve(lis) //nolint:errcheck
	defer srv.Stop()

	client, err := NewGRPCClient(GRPCClientConfig{Target: lis.Addr().String(), Insecure: true})
	if err != nil {
		t.Fatalf("NewGRPCClient: %v", err)
	}
	defer client.Close()

	if client.Conn() != client.conn {
		t.Fatal("Conn() should return the underlying conn")
	}
}
