package transport

import "fmt"

// HTTPStatusError indicates the server returned an HTTP status code >= 400.
type HTTPStatusError struct {
	StatusCode int
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("http status error: %d", e.StatusCode)
}
