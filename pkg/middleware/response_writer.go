package middleware

import "net/http"

// statusWriter wraps http.ResponseWriter to capture the status code and byte
// count written, for access logging and metrics. It assumes 200 if the
// handler never calls WriteHeader explicitly (matching net/http's own
// default), and is safe to call Write on multiple times.
type statusWriter struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func newStatusWriter(w http.ResponseWriter) *statusWriter {
	return &statusWriter{ResponseWriter: w, status: http.StatusOK}
}

func (w *statusWriter) WriteHeader(status int) {
	if !w.wroteHeader {
		w.status = status
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// Unwrap exposes the underlying ResponseWriter so http.ResponseController
// (and anything else doing an interface check, e.g. http.Flusher) still
// works through this wrapper on Go 1.20+.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
