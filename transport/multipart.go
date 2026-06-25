package transport

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"sort"
	"strings"
)

// MultipartFile is one file part of a multipart/form-data body.
type MultipartFile struct {
	// FieldName is the form field name (the part's name= attribute).
	FieldName string
	// FileName is the uploaded file name (the part's filename= attribute).
	FileName string
	// Content is the raw file bytes.
	Content []byte
	// ContentType is the part's Content-Type. Defaults to
	// "application/octet-stream" when empty.
	ContentType string
}

// BuildMultipart assembles a multipart/form-data body from text fields and file
// parts, returning the encoded body and the Content-Type header (which includes
// the generated boundary). Text fields are written in sorted key order so the
// output is deterministic for a given input. Pass the returned values to
// HTTPClient.DoMultipart.
//
//	body, ct, err := transport.BuildMultipart(
//	    map[string]string{"caption": "hi"},
//	    []transport.MultipartFile{{FieldName: "file", FileName: "a.png", Content: png}},
//	)
//	scenario := func(ctx context.Context) (int, error) {
//	    return client.DoMultipart(ctx, "POST", uploadURL, body, ct)
//	}
func BuildMultipart(fields map[string]string, files []MultipartFile) ([]byte, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := w.WriteField(k, fields[k]); err != nil {
			return nil, "", fmt.Errorf("pulse: multipart field %q: %w", k, err)
		}
	}

	for _, f := range files {
		ct := f.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", fmt.Sprintf(
			`form-data; name=%q; filename=%q`, escapeQuotes(f.FieldName), escapeQuotes(f.FileName)))
		h.Set("Content-Type", ct)
		part, err := w.CreatePart(h)
		if err != nil {
			return nil, "", fmt.Errorf("pulse: multipart file %q: %w", f.FieldName, err)
		}
		if _, err := part.Write(f.Content); err != nil {
			return nil, "", fmt.Errorf("pulse: multipart write %q: %w", f.FieldName, err)
		}
	}

	if err := w.Close(); err != nil {
		return nil, "", fmt.Errorf("pulse: multipart close: %w", err)
	}
	return buf.Bytes(), w.FormDataContentType(), nil
}

// DoMultipart performs an HTTP request with a pre-built multipart body and the
// matching Content-Type header (from BuildMultipart). It otherwise behaves like
// Do, returning the status code and recording TTFB and byte metrics.
func (c *HTTPClient) DoMultipart(ctx context.Context, method, url string, body []byte, contentType string) (int, error) {
	return c.do(ctx, method, url, bytes.NewReader(body), contentType)
}

// escapeQuotes mirrors mime/multipart's internal quoting so custom parts match
// the standard library's Content-Disposition formatting.
func escapeQuotes(s string) string {
	return strings.NewReplacer("\\", "\\\\", `"`, "\\\"").Replace(s)
}
