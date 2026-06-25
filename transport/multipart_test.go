package transport

import (
	"bytes"
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildMultipartParsesBack(t *testing.T) {
	body, ct, err := BuildMultipart(
		map[string]string{"caption": "hello world", "user": "alice"},
		[]MultipartFile{{FieldName: "file", FileName: "a.txt", Content: []byte("file-bytes"), ContentType: "text/plain"}},
	)
	if err != nil {
		t.Fatalf("BuildMultipart: %v", err)
	}
	if !strings.HasPrefix(ct, "multipart/form-data; boundary=") {
		t.Fatalf("content-type = %q", ct)
	}

	_, params, err := mime.ParseMediaType(ct)
	if err != nil {
		t.Fatalf("parse media type: %v", err)
	}
	mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	gotFields := map[string]string{}
	var gotFile, gotFileCT string
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("next part: %v", err)
		}
		data, _ := io.ReadAll(part)
		if part.FileName() != "" {
			gotFile = string(data)
			gotFileCT = part.Header.Get("Content-Type")
		} else {
			gotFields[part.FormName()] = string(data)
		}
	}
	if gotFields["caption"] != "hello world" || gotFields["user"] != "alice" {
		t.Errorf("fields = %#v", gotFields)
	}
	if gotFile != "file-bytes" || gotFileCT != "text/plain" {
		t.Errorf("file = %q (%s)", gotFile, gotFileCT)
	}
}

func TestBuildMultipartDefaultsFileContentType(t *testing.T) {
	_, ct, err := BuildMultipart(nil, []MultipartFile{{FieldName: "f", FileName: "x", Content: []byte("y")}})
	if err != nil {
		t.Fatalf("BuildMultipart: %v", err)
	}
	if ct == "" {
		t.Fatal("expected a content type")
	}
}

func TestDoMultipartSendsForm(t *testing.T) {
	var (
		gotCT   string
		gotForm string
		gotName string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		gotForm = r.FormValue("caption")
		if f, fh, err := r.FormFile("file"); err == nil {
			gotName = fh.Filename
			_ = f.Close()
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	body, ct, err := BuildMultipart(
		map[string]string{"caption": "hi"},
		[]MultipartFile{{FieldName: "file", FileName: "doc.pdf", Content: []byte("%PDF-1.4")}},
	)
	if err != nil {
		t.Fatalf("BuildMultipart: %v", err)
	}

	code, err := NewHTTPClient().DoMultipart(context.Background(), http.MethodPost, srv.URL, body, ct)
	if err != nil {
		t.Fatalf("DoMultipart: %v", err)
	}
	if code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", code)
	}
	if !strings.HasPrefix(gotCT, "multipart/form-data") {
		t.Errorf("server Content-Type = %q", gotCT)
	}
	if gotForm != "hi" || gotName != "doc.pdf" {
		t.Errorf("server saw caption=%q file=%q", gotForm, gotName)
	}
}
