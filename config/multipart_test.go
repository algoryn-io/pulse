package config

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// writeMultipartCase writes a data file plus a YAML config in the same temp dir
// and returns the config path.
func writeMultipartCase(t *testing.T, srvURL, yamlBody string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "avatar.png"), []byte("\x89PNG\r\n\x1a\nDATA"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	cfgPath := filepath.Join(dir, "cfg.yaml")
	if err := os.WriteFile(cfgPath, []byte(fmt.Sprintf(yamlBody, srvURL)), 0o644); err != nil {
		t.Fatalf("write cfg: %v", err)
	}
	return cfgPath
}

func TestMultipartUploadEndToEnd(t *testing.T) {
	var (
		mu       sync.Mutex
		gotCT    string
		gotField string
		gotFile  string
		gotName  string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		gotCT = r.Header.Get("Content-Type")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		gotField = r.FormValue("caption")
		if f, fh, err := r.FormFile("file"); err == nil {
			b := make([]byte, fh.Size)
			_, _ = f.Read(b)
			gotFile = string(b)
			gotName = fh.Filename
			_ = f.Close()
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfgPath := writeMultipartCase(t, srv.URL, ""+
		"phases:\n  - type: constant\n    duration: 1s\n    arrivalRate: 1\n"+
		"target:\n  method: POST\n  url: %s/upload\n"+
		"  multipart:\n"+
		"    fields:\n      caption: hello\n"+
		"    files:\n"+
		"      - field: file\n        path: avatar.png\n        contentType: image/png\n")

	test, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := test.Scenario(context.Background()); err != nil {
		t.Fatalf("scenario: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotField != "hello" {
		t.Errorf("caption = %q, want hello", gotField)
	}
	if gotName != "avatar.png" || gotFile != "\x89PNG\r\n\x1a\nDATA" {
		t.Errorf("file = %q (%s)", gotFile, gotName)
	}
	if gotCT == "" || gotCT[:len("multipart/form-data")] != "multipart/form-data" {
		t.Errorf("content-type = %q", gotCT)
	}
}

func TestMultipartDefaultsFileNameToBase(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(1 << 20)
		_, fh, _ := r.FormFile("f")
		if fh == nil || fh.Filename != "avatar.png" {
			http.Error(w, "bad filename", 400)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfgPath := writeMultipartCase(t, srv.URL, ""+
		"phases:\n  - type: constant\n    duration: 1s\n    arrivalRate: 1\n"+
		"target:\n  method: POST\n  url: %s/u\n"+
		"  multipart:\n    files:\n      - field: f\n        path: avatar.png\n")
	test, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if code, err := test.Scenario(context.Background()); err != nil || code != 200 {
		t.Fatalf("scenario: code=%d err=%v", code, err)
	}
}

func TestMultipartValidationErrors(t *testing.T) {
	base := "phases:\n  - type: constant\n    duration: 1s\n    arrivalRate: 1\n"
	cases := []struct {
		name    string
		yaml    string
		wantErr error
	}{
		{
			"with body",
			"target:\n  method: POST\n  url: http://x/u\n  body: hi\n  multipart:\n    fields:\n      a: b\n",
			errMultipartWithBody,
		},
		{
			"with feeder",
			"target:\n  method: POST\n  url: http://x/{{id}}\n  multipart:\n    fields:\n      a: b\n" +
				"feeder:\n  format: csv\n  path: x.csv\n",
			errMultipartWithFeeder,
		},
		{
			"distributed",
			"target:\n  method: POST\n  url: http://x/u\n  multipart:\n    fields:\n      a: b\n" +
				"workers: [\"127.0.0.1:9300\"]\n",
			errMultipartDistributed,
		},
		{
			"empty file field",
			"target:\n  method: POST\n  url: http://x/u\n  multipart:\n    files:\n      - path: a.png\n",
			errMultipartEmptyField,
		},
		{
			"empty file path",
			"target:\n  method: POST\n  url: http://x/u\n  multipart:\n    files:\n      - field: f\n",
			errMultipartEmptyPath,
		},
		{
			"empty multipart",
			"target:\n  method: POST\n  url: http://x/u\n  multipart: {}\n",
			errMultipartEmpty,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "cfg.yaml")
			if err := os.WriteFile(p, []byte(base+c.yaml), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			_, err := Load(p)
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("got %v, want %v", err, c.wantErr)
			}
		})
	}
}

func TestMultipartMissingFileRejected(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "cfg.yaml")
	yaml := "phases:\n  - type: constant\n    duration: 1s\n    arrivalRate: 1\n" +
		"target:\n  method: POST\n  url: http://x/u\n" +
		"  multipart:\n    files:\n      - field: f\n        path: nonexistent.bin\n"
	if err := os.WriteFile(p, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := Load(p)
	if err == nil || !strings.Contains(err.Error(), "multipart file") {
		t.Fatalf("expected a multipart file error, got %v", err)
	}
}
