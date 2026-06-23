package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// cookieEchoServer sets a session cookie on /login and, on /whoami, reports
// whether the request carried that cookie.
func cookieEchoServer() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "abc123", Path: "/"})
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/whoami", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("session"); err == nil && c.Value == "abc123" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	})
	return httptest.NewServer(mux)
}

func TestSessionPersistsCookiesWithinSession(t *testing.T) {
	srv := cookieEchoServer()
	defer srv.Close()

	base := NewHTTPClientWith(HTTPClientConfig{})
	s := base.Session()

	if code, err := s.Do(context.Background(), http.MethodPost, srv.URL+"/login", nil); err != nil || code != http.StatusOK {
		t.Fatalf("login: code=%d err=%v", code, err)
	}
	resp, err := s.DoWithResponse(context.Background(), http.MethodGet, srv.URL+"/whoami", nil)
	if err != nil {
		t.Fatalf("whoami: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected cookie to be resent within the session (200), got %d", resp.StatusCode)
	}
}

func TestSessionsAreIsolated(t *testing.T) {
	srv := cookieEchoServer()
	defer srv.Close()

	base := NewHTTPClientWith(HTTPClientConfig{})

	// Session 1 logs in.
	s1 := base.Session()
	if _, err := s1.Do(context.Background(), http.MethodPost, srv.URL+"/login", nil); err != nil {
		t.Fatalf("s1 login: %v", err)
	}

	// Session 2 never logged in: it must not see session 1's cookie.
	s2 := base.Session()
	resp, err := s2.DoWithResponse(context.Background(), http.MethodGet, srv.URL+"/whoami", nil)
	if err != nil {
		t.Fatalf("s2 whoami: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected isolated session without cookie to be unauthorized (401), got %d", resp.StatusCode)
	}
}

func TestSessionSharesTransport(t *testing.T) {
	base := NewHTTPClientWith(HTTPClientConfig{})
	s := base.Session()
	if s.client.Transport != base.client.Transport {
		t.Fatal("Session() should reuse the base client's transport")
	}
	if s.client.Jar == nil {
		t.Fatal("Session() should attach a cookie jar")
	}
	if base.client.Jar != nil {
		t.Fatal("base client should not have a jar unless configured")
	}
}

func TestSessionConcurrentUseIsSafe(t *testing.T) {
	srv := cookieEchoServer()
	defer srv.Close()

	base := NewHTTPClientWith(HTTPClientConfig{})
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := base.Session()
			_, _ = s.Do(context.Background(), http.MethodPost, srv.URL+"/login", nil)
			_, _ = s.Do(context.Background(), http.MethodGet, srv.URL+"/whoami", nil)
		}()
	}
	wg.Wait()
}
