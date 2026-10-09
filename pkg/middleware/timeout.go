package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	"github.com/herlenashavi/go-api/pkg/problem"
)

// Timeout bounds how long next may run. On expiry it writes a 503
// problem+json response and returns — the handler goroutine is left running
// (Go has no way to force-cancel it) but its eventual writes are discarded,
// never reaching the client.
//
// Unlike wrapping next.ServeHTTP in a bare "go func(){ next.ServeHTTP(...) }
// ()", this recovers panics *inside* that goroutine itself: an unrecovered
// panic in a goroutine terminates the whole process, and an outer Recover
// middleware's recover() cannot see across a goroutine boundary. Mount
// Recover outside Timeout anyway, for panics in middleware that runs
// before Timeout spawns its goroutine.
func Timeout(d time.Duration, logger *slog.Logger, debugMode bool) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()

			tw := &timeoutWriter{header: make(http.Header)}
			done := make(chan struct{})

			go func() {
				defer close(done)
				defer func() {
					if rec := recover(); rec != nil {
						err := fmt.Errorf("panic: %v", rec)
						logger.ErrorContext(ctx, "unhandled panic", "error", err, "stack", string(debug.Stack()))
						tw.writeFallback(problem.Internal(err, debugMode))
					}
				}()
				next.ServeHTTP(tw, r.WithContext(ctx))
			}()

			select {
			case <-done:
				tw.commit(w)
			case <-ctx.Done():
				tw.markTimedOut()
				problem.Write(w, r, timeoutProblem())
			}
		})
	}
}

func timeoutProblem() *problem.Problem {
	return problem.New(http.StatusServiceUnavailable, "timeout", "Request Timeout",
		"The request took too long to process and was aborted.")
}

// timeoutWriter buffers everything the handler writes so it can either be
// committed to the real ResponseWriter (handler finished in time) or
// silently dropped (timed out first) — never partially written to the real
// connection from a goroutine that's still running after we've already
// responded on the main path.
type timeoutWriter struct {
	mu        sync.Mutex
	header    http.Header
	buf       bytes.Buffer
	status    int
	timedOut  bool
	committed bool
}

func (tw *timeoutWriter) Header() http.Header {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	return tw.header
}

func (tw *timeoutWriter) WriteHeader(status int) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.timedOut || tw.status != 0 {
		return
	}
	tw.status = status
}

func (tw *timeoutWriter) Write(b []byte) (int, error) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.timedOut {
		return 0, http.ErrHandlerTimeout
	}
	if tw.status == 0 {
		tw.status = http.StatusOK
	}
	return tw.buf.Write(b)
}

func (tw *timeoutWriter) markTimedOut() {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	tw.timedOut = true
}

// writeFallback is used by the panic recovery path to stage a response
// body/status as if the handler had written it normally.
func (tw *timeoutWriter) writeFallback(p *problem.Problem) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.timedOut {
		return
	}
	tw.header.Set("Content-Type", "application/problem+json; charset=utf-8")
	tw.status = p.Status
	_ = json.NewEncoder(&tw.buf).Encode(p)
}

func (tw *timeoutWriter) commit(w http.ResponseWriter) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.committed || tw.timedOut {
		return
	}
	tw.committed = true
	for k, v := range tw.header {
		w.Header()[k] = v
	}
	status := tw.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(tw.buf.Bytes())
}
