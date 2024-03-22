package transport

import (
	"net/http"
)

// StatusResponseWriter is a wrapper around http.ResponseWriter that captures the
// status code and the number of bytes written.
type StatusResponseWriter struct {
	statusCode    int
	responseBytes int
	http.ResponseWriter
}

// NewStatusResponseWriter returns a new StatusResponseWriter.
func NewStatusResponseWriter(w http.ResponseWriter) *StatusResponseWriter {
	return &StatusResponseWriter{
		ResponseWriter: w,
	}
}

// Write writes the bytes to the ResponseWriter and captures the number of bytes written.
func (w *StatusResponseWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.responseBytes += n
	return n, err
}

// Flush flushes the ResponseWriter if it implements http.Flusher.
func (w *StatusResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// WriteHeader writes the header and captures the status code.
func (w *StatusResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

// Code returns the status code.
func (w *StatusResponseWriter) Code() int {
	code := w.statusCode
	if code == 0 {
		// When statusCode is 0 then WriteHeader was never called and we can assume that
		// the ResponseWriter wrote an http.StatusOK.
		code = http.StatusOK
	}
	return code
}

// ResponseBytes returns the number of bytes written.
func (w *StatusResponseWriter) ResponseBytes() int {
	return w.responseBytes
}

// StatusCodeClass returns the class of the status code.
func (w *StatusResponseWriter) StatusCodeClass() string {
	class := "XXX"
	switch w.Code() / 100 {
	case 1:
		class = "1XX"
	case 2:
		class = "2XX"
	case 3:
		class = "3XX"
	case 4:
		class = "4XX"
	case 5:
		class = "5XX"
	}
	return class
}
