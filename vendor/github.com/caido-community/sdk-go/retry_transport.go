package caido

import (
	"errors"
	"io"
	"net"
	"net/http"
	"syscall"
	"time"
)

const retryBackoff = 200 * time.Millisecond

// retryTransport retries connection-level errors once. Designed to absorb
// transient TCP failures after macOS sleep/wake (Caido daemon reconnect
// window). Sits between authTransport and http.DefaultTransport so that
// token refresh happens once per logical request, not per attempt.
type retryTransport struct {
	base http.RoundTripper
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err == nil || !isTransientNetErr(err) {
		return resp, err
	}

	// Drop stale idle connections so the retry uses a fresh TCP dial.
	if closer, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}

	// Body replay safety: if body was consumed and is not replayable, bubble
	// the original error — better to fail fast than to send an empty body.
	if req.GetBody == nil && req.Body != nil {
		return resp, err
	}
	if req.GetBody != nil {
		body, gErr := req.GetBody()
		if gErr != nil {
			return resp, err
		}
		req.Body = body
	}

	select {
	case <-req.Context().Done():
		return nil, req.Context().Err()
	case <-time.After(retryBackoff):
	}

	return t.base.RoundTrip(req)
}

// isTransientNetErr reports whether err is a connection-level failure that is
// safe to retry (dial failed, idle connection rejected, broken pipe). HTTP
// 4xx/5xx responses do NOT reach this path — they arrive as a non-nil
// *http.Response with a nil error from RoundTrip.
func isTransientNetErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		switch opErr.Op {
		case "dial", "read", "write":
			return true
		}
	}
	return false
}
