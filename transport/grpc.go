package transport

import (
	"context"
	"crypto/tls"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
)

// GRPCClientConfig holds connection settings for a GRPCClient.
type GRPCClientConfig struct {
	// Target is the server address in "host:port" form.
	Target string
	// Insecure disables TLS. Mutually exclusive with TLSConfig.
	Insecure bool
	// TLSConfig configures TLS. When nil and Insecure is false, the system
	// certificate pool is used.
	TLSConfig *tls.Config
	// DialTimeout caps how long NewGRPCClient waits for the initial connection.
	// Zero uses the gRPC default (no timeout on Dial; connections are lazy).
	DialTimeout time.Duration
	// KeepAlive configures client-side keepalive pings. Zero value disables
	// explicit keepalive configuration (gRPC defaults apply).
	KeepAlive keepalive.ClientParameters
}

// GRPCClient wraps a *grpc.ClientConn and exposes it for use in Pulse scenarios.
// Obtain via NewGRPCClient and pass Conn() to generated gRPC client constructors.
type GRPCClient struct {
	conn *grpc.ClientConn
}

// NewGRPCClient dials the target and returns a GRPCClient.
// The connection is established lazily by default; use DialTimeout to enforce
// an upper bound on the initial dial.
func NewGRPCClient(cfg GRPCClientConfig) (*GRPCClient, error) {
	opts := []grpc.DialOption{}

	switch {
	case cfg.Insecure:
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	case cfg.TLSConfig != nil:
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(cfg.TLSConfig)))
	default:
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewClientTLSFromCert(nil, "")))
	}

	if cfg.KeepAlive != (keepalive.ClientParameters{}) {
		opts = append(opts, grpc.WithKeepaliveParams(cfg.KeepAlive))
	}

	ctx := context.Background()
	if cfg.DialTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.DialTimeout)
		defer cancel()
	}

	//nolint:staticcheck // grpc.DialContext is deprecated in newer grpc but NewClient requires explicit connect
	conn, err := grpc.DialContext(ctx, cfg.Target, opts...)
	if err != nil {
		return nil, err
	}
	return &GRPCClient{conn: conn}, nil
}

// Conn returns the underlying *grpc.ClientConn for passing to generated
// gRPC client constructors (e.g. pb.NewMyServiceClient(client.Conn())).
func (c *GRPCClient) Conn() *grpc.ClientConn {
	return c.conn
}

// Close tears down the connection. Call it when the test is done, typically
// via defer after NewGRPCClient.
func (c *GRPCClient) Close() error {
	return c.conn.Close()
}

// CallGRPC executes a gRPC call and returns a Pulse-compatible (statusCode, error)
// pair. The status code is mapped from the gRPC status code to an HTTP-equivalent
// integer so Pulse metrics and thresholds behave consistently across HTTP and gRPC
// scenarios. A nil error maps to 200.
//
// Example:
//
//	scenario := func(ctx context.Context) (int, error) {
//	    return transport.CallGRPC(func() error {
//	        _, err := svc.GetUser(ctx, &pb.GetUserRequest{Id: 1})
//	        return err
//	    })
//	}
func CallGRPC(fn func() error) (int, error) {
	err := fn()
	return grpcCodeToHTTP(status.Code(err)), err
}

// grpcCodeToHTTP maps gRPC status codes to HTTP-equivalent integers.
// This is intentionally not a strict 1:1 mapping — it approximates the
// semantics so Pulse threshold rules (error rate, status codes) work the
// same way regardless of transport.
func grpcCodeToHTTP(c codes.Code) int {
	switch c {
	case codes.OK:
		return 200
	case codes.Canceled:
		return 499
	case codes.InvalidArgument, codes.FailedPrecondition, codes.OutOfRange:
		return 400
	case codes.NotFound:
		return 404
	case codes.AlreadyExists, codes.Aborted:
		return 409
	case codes.PermissionDenied:
		return 403
	case codes.Unauthenticated:
		return 401
	case codes.ResourceExhausted:
		return 429
	case codes.Unimplemented:
		return 501
	case codes.Unavailable:
		return 503
	case codes.DeadlineExceeded:
		return 504
	default: // Unknown, Internal, DataLoss
		return 500
	}
}
