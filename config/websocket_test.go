package config

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/net/websocket"
)

func wsEcho(t *testing.T) (string, func()) {
	t.Helper()
	srv := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		_, _ = io.Copy(ws, ws)
	}))
	return "ws" + strings.TrimPrefix(srv.URL, "http"), srv.Close
}

func TestWebSocketTargetRoundtrip(t *testing.T) {
	url, stop := wsEcho(t)
	defer stop()

	dir := t.TempDir()
	p := filepath.Join(dir, "cfg.yaml")
	yaml := "phases:\n  - type: constant\n    duration: 1s\n    arrivalRate: 1\n" +
		"target:\n  url: " + url + "\n  message: ping\n"
	if err := os.WriteFile(p, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	test, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	code, err := test.Scenario(context.Background())
	if err != nil || code != 200 {
		t.Fatalf("scenario: code=%d err=%v", code, err)
	}
}

func TestWebSocketTargetSendOnly(t *testing.T) {
	url, stop := wsEcho(t)
	defer stop()

	dir := t.TempDir()
	p := filepath.Join(dir, "cfg.yaml")
	yaml := "phases:\n  - type: constant\n    duration: 1s\n    arrivalRate: 1\n" +
		"target:\n  url: " + url + "\n  message: ping\n  expectReply: false\n"
	if err := os.WriteFile(p, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	test, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if code, err := test.Scenario(context.Background()); err != nil || code != 200 {
		t.Fatalf("send-only scenario: code=%d err=%v", code, err)
	}
}

func TestWebSocketDialErrorInScenario(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "cfg.yaml")
	yaml := "phases:\n  - type: constant\n    duration: 1s\n    arrivalRate: 1\n" +
		"target:\n  url: ws://127.0.0.1:1/nope\n  message: ping\n"
	if err := os.WriteFile(p, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	test, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := test.Scenario(context.Background()); err == nil {
		t.Fatal("expected a dial error")
	}
}

func TestWebSocketValidationErrors(t *testing.T) {
	base := "phases:\n  - type: constant\n    duration: 1s\n    arrivalRate: 1\n"
	cases := []struct {
		name    string
		yaml    string
		wantErr error
	}{
		{"no message", "target:\n  url: ws://h/x\n", errWSMessageRequired},
		{"with body", "target:\n  url: ws://h/x\n  message: m\n  body: b\n", errWSUnsupportedField},
		{"with method", "target:\n  url: ws://h/x\n  message: m\n  method: GET\n", errWSUnsupportedField},
		{"with query", "target:\n  url: ws://h/x\n  message: m\n  query:\n    a: b\n", errWSUnsupportedField},
		{"with checks", "target:\n  url: ws://h/x\n  message: m\n  checks:\n    status: 200\n", errWSUnsupportedField},
		{"with feeder", "target:\n  url: ws://h/x\n  message: m\nfeeder:\n  format: csv\n  path: x.csv\n", errWSFeederUnsupported},
		{"distributed", "target:\n  url: ws://h/x\n  message: m\nworkers: [\"127.0.0.1:9300\"]\n", errWSDistributed},
		{"ws fields on http", "target:\n  method: GET\n  url: http://h/x\n  message: m\n", errWSOnlyFields},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "cfg.yaml")
			if err := os.WriteFile(p, []byte(base+c.yaml), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			if _, err := Load(p); !errors.Is(err, c.wantErr) {
				t.Fatalf("got %v, want %v", err, c.wantErr)
			}
		})
	}
}
