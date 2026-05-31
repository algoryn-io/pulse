package pulse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"algoryn.io/pulse/transport"
)

func newHealthyHTTPScenario(t *testing.T) Scenario {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	client := transport.NewHTTPClientWith(transport.HTTPClientConfig{
		Timeout: time.Second,
	})
	return func(ctx context.Context) (int, error) {
		return client.Get(ctx, srv.URL)
	}
}
