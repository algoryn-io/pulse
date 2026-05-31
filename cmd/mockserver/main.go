package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"sync/atomic"
	"time"
)

const (
	modeHealthy     = "healthy"
	modeMixedErrors = "mixed-errors"
	modeSlow        = "slow"
)

func main() {
	mode := flag.String("mode", modeHealthy, "server mode: healthy, mixed-errors, slow")
	addr := flag.String("addr", "127.0.0.1:8080", "listen address")
	flag.Parse()

	handler, err := newHandler(*mode)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("mock server listening on %s (mode=%s)\n", *addr, *mode)
	server := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	log.Fatal(server.ListenAndServe())
}

func newHandler(mode string) (http.Handler, error) {
	switch mode {
	case modeHealthy:
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok\n"))
		}), nil
	case modeMixedErrors:
		var count atomic.Uint64
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n := count.Add(1)
			if n%2 == 0 {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("internal error\n"))
				return
			}

			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok\n"))
		}), nil
	case modeSlow:
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(120 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok\n"))
		}), nil
	default:
		return nil, fmt.Errorf("unsupported mode %q (expected %q, %q, or %q)", mode, modeHealthy, modeMixedErrors, modeSlow)
	}
}
