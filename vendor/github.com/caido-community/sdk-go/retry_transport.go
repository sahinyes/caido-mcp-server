package caido

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"
)

const retryBackoff = 200 * time.Millisecond

// maxMutationSniff bounds how much of a request body is parsed to decide
// whether it is a mutation. A body too large to parse is treated as a mutation,
// which costs a read its retry and never costs a mutation its safety.
const maxMutationSniff = 4 << 20

// retryTransport retries connection-level errors once. Designed to absorb
// transient TCP failures after macOS sleep/wake (Caido daemon reconnect
// window). Sits between authTransport and http.DefaultTransport so that
// token refresh happens once per logical request, not per attempt.
type retryTransport struct {
	base http.RoundTripper
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	transient, mayHaveReachedServer := classifyTransientNetErr(err)
	if err == nil || !transient {
		return resp, err
	}

	// Drop stale idle connections. This happens BEFORE the mutation check on
	// purpose: the pooled connection that just failed is dead whether or not
	// this particular request is allowed a second attempt, and leaving it in
	// the pool hands it to the NEXT request - which, after a sleep/wake, is
	// usually a read that could have healed itself. Purging is pool hygiene,
	// not part of the retry.
	if closer, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}

	// A GraphQL MUTATION is not retried once the bytes may already have left:
	// "connection reset while reading the response" and "request delivered,
	// then executed, then the reply lost" are the same error here. Replaying it
	// is a second startReplayTask, a second createFinding, a second
	// deleteFindings - silent duplicate side effects in exchange for hiding one
	// error from one call.
	//
	// Dial failures keep their retry even for mutations, because nothing was
	// sent: that is also the case the sleep/wake self-heal was written for,
	// where Caido's daemon has not finished coming back. (net/http already
	// retries its own nothing-was-written case on a reused connection, so what
	// reaches this transport otherwise is the ambiguous half.)
	if mayHaveReachedServer && isGraphQLMutation(req) {
		return resp, err
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
	transient, _ := classifyTransientNetErr(err)
	return transient
}

// classifyTransientNetErr splits transient failures by WHEN they happened:
// mayHaveReachedServer is false only when the connection was never
// established, which is the one case where replaying a mutation cannot
// duplicate its effect.
func classifyTransientNetErr(err error) (transient, mayHaveReachedServer bool) {
	if err == nil {
		return false, false
	}
	var opErr *net.OpError
	hasOpErr := errors.As(err, &opErr)

	// Dial: nothing was written, whatever the underlying errno.
	if hasOpErr && opErr.Op == "dial" {
		return true, false
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true, false
	}
	if errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) {
		return true, true
	}
	if hasOpErr {
		switch opErr.Op {
		case "read", "write":
			return true, true
		}
	}
	return false, false
}

// isGraphQLMutation reports whether req carries a GraphQL mutation. Anything it
// cannot read it calls a mutation: the cost of being wrong that way is one
// un-retried read, and the cost of being wrong the other way is a duplicated
// side effect.
func isGraphQLMutation(req *http.Request) bool {
	if req == nil {
		return true
	}
	// No body at all is not a GraphQL operation.
	if req.Body == nil && req.GetBody == nil {
		return false
	}
	if req.GetBody == nil {
		return true
	}
	body, err := req.GetBody()
	if err != nil {
		return true
	}
	defer body.Close()

	var payload struct {
		Query string `json:"query"`
	}
	dec := json.NewDecoder(io.LimitReader(body, maxMutationSniff))
	if err := dec.Decode(&payload); err != nil {
		return true
	}
	return strings.HasPrefix(
		strings.TrimLeft(payload.Query, " \t\r\n"), "mutation",
	)
}
