package replay

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/c0tton-fluff/caido-mcp-server/internal/httputil"
	caido "github.com/caido-community/sdk-go"
)

// BatchRequest is a single request in a batch.
type BatchRequest struct {
	Label string
	Raw   string // raw HTTP, will be CRLF-normalized
	Host  string // override Host header
	Port  int    // override port
	TLS   *bool  // override TLS (default true)
}

// BatchResult is the result of a single request in a batch.
type BatchResult struct {
	Label       string                  `json:"label"`
	StatusCode  int                     `json:"statusCode,omitempty"`
	RoundtripMs int                     `json:"roundtripMs,omitempty"`
	Request     *httputil.ParsedMessage `json:"request,omitempty"`
	Response    *httputil.ParsedMessage `json:"response,omitempty"`
	Error       string                  `json:"error,omitempty"`
}

// batchPollTimeout is how long one batched request waits for its answer.
const batchPollTimeout = 15 * time.Second

// RunBatch sends N requests in parallel through Caido's Replay API.
// It creates a session pool, dispatches each request to its own
// session, polls for results, and returns them in order.
//
// Acquire is governed by ctx, not by a timeout of its own: a batch of 20 slow
// requests at concurrency 2 legitimately waits a long time for a session, and
// the caller's deadline is the only thing that knows how long is too long.
func RunBatch(
	ctx context.Context,
	client *caido.Client,
	requests []BatchRequest,
	concurrency int,
	bodyLimit int,
) []BatchResult {
	if concurrency < 1 {
		concurrency = 5
	}
	if concurrency > 20 {
		concurrency = 20
	}
	if bodyLimit <= 0 {
		bodyLimit = httputil.DefaultBodyLimit
	}

	n := len(requests)
	if n == 0 {
		return nil
	}

	// Cap concurrency to request count.
	poolSize := min(concurrency, n)

	// Create session pool. If this fails, return all errors.
	pool, err := NewSessionPool(ctx, client, poolSize)
	if err != nil {
		results := make([]BatchResult, n)
		for i := range results {
			results[i] = BatchResult{
				Label: requests[i].Label,
				Error: fmt.Sprintf("session pool: %v", err),
			}
		}
		return results
	}
	defer pool.Cleanup(context.WithoutCancel(ctx))

	results := make([]BatchResult, n)
	var wg sync.WaitGroup

	for i, req := range requests {
		wg.Add(1)
		go func(idx int, br BatchRequest) {
			defer wg.Done()
			results[idx] = executeSingle(
				ctx, client, pool, br, bodyLimit,
			)
		}(i, req)
	}

	wg.Wait()
	return results
}

func executeSingle(
	ctx context.Context,
	client *caido.Client,
	pool *SessionPool,
	br BatchRequest,
	bodyLimit int,
) BatchResult {
	result := BatchResult{Label: br.Label}

	// Acquire a session from the pool.
	sessionID, err := pool.Acquire(ctx)
	if err != nil {
		result.Error = fmt.Sprintf("acquire session: %v", err)
		return result
	}

	// A session is only returned to the pool once this request is PROVABLY
	// finished with it. Anything else retires it.
	//
	// The old code released unconditionally, and that is a wrong-answer bug,
	// not a leak: with 10 requests at concurrency 5, request A exhausts its
	// poll window while its task is still running, releases the session, B
	// acquires the same session, drafts into it, StartTask answers
	// TaskInProgress (which the batch discarded), B never reaches the wire —
	// and then A's entry becomes active and A's 200 is written onto B's row.
	// In a scan that reads "the cross-site probe got 200". Retiring replaces
	// the session so the pool keeps its width; if the replacement cannot be
	// created the pool shrinks, which makes later requests fail to acquire
	// instead of inheriting a busy session.
	retire := true
	defer func() {
		if retire {
			pool.Retire(ctx, sessionID)
			return
		}
		pool.Release(sessionID)
	}()

	// Normalize raw request.
	raw := httputil.NormalizeCRLF(br.Raw)

	// Resolve host.
	host := br.Host
	if host == "" {
		host = httputil.ParseHostHeader(br.Raw)
	}
	if host == "" {
		result.Error = "host required (provide in input or Host header)"
		retire = false
		return result
	}

	port := br.Port
	if h, p, splitErr := net.SplitHostPort(host); splitErr == nil {
		host = h
		if port == 0 {
			if pv, convErr := strconv.Atoi(p); convErr == nil {
				port = pv
			}
		}
	}

	useTLS := true
	if br.TLS != nil {
		useTLS = *br.TLS
	}
	if port == 0 {
		if useTLS {
			port = 443
		} else {
			port = 80
		}
	}

	rawB64 := base64.StdEncoding.EncodeToString([]byte(raw))
	conn := caido.ReplayConnection{Host: host, Port: port, IsTLS: useTLS}

	// The pooled session is named explicitly, so Send never rotates away from
	// it: a batch row must report the session it was dispatched to.
	outcome, err := Send(ctx, client, SendOptions{
		SessionID:           sessionID,
		Conn:                conn,
		RawBase64:           rawB64,
		UpdateContentLength: true,
		PollTimeout:         batchPollTimeout,
	})
	if err != nil {
		result.Error = fmt.Sprintf("send: %v", err)
		return result
	}
	if outcome.PollErr != nil {
		result.Error = fmt.Sprintf("poll: %v", outcome.PollErr)
		return result
	}

	entry := outcome.Entry
	retire = false

	if entry.Error != nil && *entry.Error != "" {
		result.Error = *entry.Error
	}

	if entry.Request != nil {
		result.Request = httputil.ParseBase64(
			entry.Request.Raw, true, false, 0, 0,
		)
		if entry.Request.Response != nil {
			resp := entry.Request.Response
			result.StatusCode = resp.StatusCode
			result.RoundtripMs = resp.RoundtripTime
			result.Response = httputil.ParseBase64(
				resp.Raw, true, true, 0, bodyLimit,
			)
		}
	}

	return result
}
