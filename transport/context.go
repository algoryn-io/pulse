package transport

import "context"

type responseStatusKey struct{}

// ContextWithResponseStatus attaches a pointer the HTTP client sets to the
// response status code when a response is received. If no response is
// received, the value is left unchanged (typically 0).
func ContextWithResponseStatus(parent context.Context, statusCode *int) context.Context {
	return context.WithValue(parent, responseStatusKey{}, statusCode)
}
