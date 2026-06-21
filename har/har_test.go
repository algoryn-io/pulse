package har_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pulse "algoryn.io/pulse"
	"algoryn.io/pulse/har"
)

func writeHAR(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.har")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writeHAR: %v", err)
	}
	return path
}

func TestLoadReturnsScenarioThatReplaysRequest(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	harJSON := strings.NewReader(`{"log":{"entries":[
		{"request":{"method":"GET","url":"` + srv.URL + `","headers":[],"postData":{}}}
	]}}`)

	scenario, err := har.Load(harJSON, har.Config{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	code, err := scenario(context.Background())
	if err != nil || code != 200 {
		t.Fatalf("expected (200, nil), got (%d, %v)", code, err)
	}
	if !called {
		t.Fatal("server was not called")
	}
}

func TestLoadFileReturnsScenario(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	path := writeHAR(t, `{"log":{"entries":[
		{"request":{"method":"GET","url":"`+srv.URL+`","headers":[],"postData":{}}}
	]}}`)

	scenario, err := har.LoadFile(path, har.Config{})
	if err != nil || scenario == nil {
		t.Fatalf("LoadFile: err=%v scenario=%v", err, scenario)
	}
}

func TestLoadForwardsHeaders(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	harJSON := strings.NewReader(`{"log":{"entries":[
		{"request":{"method":"GET","url":"` + srv.URL + `",
		 "headers":[{"name":"Authorization","value":"Bearer tok123"}],
		 "postData":{}}}
	]}}`)

	scenario, _ := har.Load(harJSON, har.Config{})
	scenario(context.Background()) //nolint:errcheck

	if gotAuth != "Bearer tok123" {
		t.Fatalf("expected Authorization: Bearer tok123, got %q", gotAuth)
	}
}

func TestLoadStripsHopByHopHeaders(t *testing.T) {
	var gotConn string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotConn = r.Header.Get("Connection")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	harJSON := strings.NewReader(`{"log":{"entries":[
		{"request":{"method":"GET","url":"` + srv.URL + `",
		 "headers":[{"name":"Connection","value":"keep-alive"}],
		 "postData":{}}}
	]}}`)

	scenario, _ := har.Load(harJSON, har.Config{})
	scenario(context.Background()) //nolint:errcheck

	if gotConn != "" {
		t.Fatalf("Connection header should be stripped, got %q", gotConn)
	}
}

func TestLoadFilterSkipsEntries(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	harJSON := strings.NewReader(`{"log":{"entries":[
		{"request":{"method":"GET","url":"` + srv.URL + `/api","headers":[],"postData":{}}},
		{"request":{"method":"GET","url":"` + srv.URL + `/static/logo.png","headers":[],"postData":{}}}
	]}}`)

	scenario, err := har.Load(harJSON, har.Config{
		Filter: func(req har.Request) bool {
			return !strings.Contains(req.URL, "/static/")
		},
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	scenario(context.Background()) //nolint:errcheck
	if calls != 1 {
		t.Fatalf("expected 1 call (filtered /static/), got %d", calls)
	}
}

func TestLoadSendsPostBody(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	harJSON := strings.NewReader(`{"log":{"entries":[
		{"request":{"method":"POST","url":"` + srv.URL + `",
		 "headers":[],"postData":{"mimeType":"application/json","text":"{\"id\":1}"}}}
	]}}`)

	scenario, _ := har.Load(harJSON, har.Config{})
	code, err := scenario(context.Background())
	if err != nil || code != 201 {
		t.Fatalf("expected (201, nil), got (%d, %v)", code, err)
	}
	if gotBody != `{"id":1}` {
		t.Fatalf("unexpected body: %q", gotBody)
	}
}

func TestLoadErrorsOnStatus4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	harJSON := strings.NewReader(`{"log":{"entries":[
		{"request":{"method":"GET","url":"` + srv.URL + `","headers":[],"postData":{}}}
	]}}`)

	scenario, _ := har.Load(harJSON, har.Config{})
	code, err := scenario(context.Background())
	if err == nil || code != 404 {
		t.Fatalf("expected (404, err), got (%d, %v)", code, err)
	}
}

func TestLoadMultipleEntriesRunInSequence(t *testing.T) {
	var order []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	harJSON := strings.NewReader(`{"log":{"entries":[
		{"request":{"method":"GET","url":"` + srv.URL + `/first","headers":[],"postData":{}}},
		{"request":{"method":"GET","url":"` + srv.URL + `/second","headers":[],"postData":{}}},
		{"request":{"method":"GET","url":"` + srv.URL + `/third","headers":[],"postData":{}}}
	]}}`)

	scenario, _ := har.Load(harJSON, har.Config{})
	scenario(context.Background()) //nolint:errcheck

	if len(order) != 3 || order[0] != "/first" || order[1] != "/second" || order[2] != "/third" {
		t.Fatalf("unexpected order: %v", order)
	}
}

func TestLoadStopsOnFirstFailure(t *testing.T) {
	var secondCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/first" {
			http.Error(w, "fail", http.StatusInternalServerError)
			return
		}
		secondCalled = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	harJSON := strings.NewReader(`{"log":{"entries":[
		{"request":{"method":"GET","url":"` + srv.URL + `/first","headers":[],"postData":{}}},
		{"request":{"method":"GET","url":"` + srv.URL + `/second","headers":[],"postData":{}}}
	]}}`)

	scenario, _ := har.Load(harJSON, har.Config{})
	_, err := scenario(context.Background())
	if err == nil {
		t.Fatal("expected error from first entry")
	}
	if secondCalled {
		t.Fatal("second request must not run after first fails")
	}
}

func TestLoadAllFilteredReturnsError(t *testing.T) {
	harJSON := strings.NewReader(`{"log":{"entries":[
		{"request":{"method":"GET","url":"http://example.com/static","headers":[],"postData":{}}}
	]}}`)
	_, err := har.Load(harJSON, har.Config{
		Filter: func(_ har.Request) bool { return false },
	})
	if err == nil {
		t.Fatal("expected error when all entries are filtered")
	}
}

func TestLoadFileErrorsForMissingFile(t *testing.T) {
	_, err := har.LoadFile("/no/such/file.har", har.Config{})
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoadReturnsErrorForInvalidJSON(t *testing.T) {
	_, err := har.Load(strings.NewReader(`not json`), har.Config{})
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestLoadUsesCustomHTTPClient(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	harJSON := strings.NewReader(`{"log":{"entries":[
		{"request":{"method":"GET","url":"` + srv.URL + `","headers":[],"postData":{}}}
	]}}`)

	custom := &http.Client{}
	scenario, _ := har.Load(harJSON, har.Config{Client: custom})
	scenario(context.Background()) //nolint:errcheck
	if !called {
		t.Fatal("custom client should have been used")
	}
}

// compile-time check
var _ pulse.Scenario = (pulse.Scenario)(nil)
