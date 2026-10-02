package replay

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/c0tton-fluff/caido-mcp-server/internal/auth"
	caido "github.com/caido-community/sdk-go"
)

// Live, opt-in tests. They talk to a real Caido instance and append real
// entries to a real project, so they are skipped unless CAIDO_LIVE_RACE=1 and
// CAIDO_URL are both set. They are NOT a substitute for the unit tests beside
// them: they exist because the defect they cover (concurrent sends on one
// shared session trading drafts) cannot be reproduced against a fake — it is a
// property of Caido's draft-then-start contract, where the request bytes sit in
// shared server-side state between two round trips.
func liveClient(t *testing.T) *caido.Client {
	t.Helper()
	url := os.Getenv("CAIDO_URL")
	if os.Getenv("CAIDO_LIVE_RACE") != "1" || url == "" {
		t.Skip("set CAIDO_LIVE_RACE=1 and CAIDO_URL to run live replay tests")
	}
	client, err := caido.NewClient(caido.Options{URL: url})
	if err != nil {
		t.Fatalf("client init: %v", err)
	}
	store, err := auth.NewTokenStore()
	if err != nil {
		t.Fatalf("token store: %v", err)
	}
	tok, err := store.Load()
	if err != nil || tok == nil {
		t.Fatalf("no stored token (run 'caido-mcp-server login'): %v", err)
	}
	client.SetAccessToken(tok.AccessToken)
	return client
}

func probeTarget() (caido.ReplayConnection, string) {
	host := os.Getenv("CAIDO_LIVE_PROBE_HOST")
	if host == "" {
		host = "100.107.7.115"
	}
	return caido.ReplayConnection{Host: host, Port: 9090, IsTLS: false}, host
}

func probeRaw(host string, marker string) string {
	return fmt.Sprintf(
		"GET /health HTTP/1.1\r\nHost: %s:9090\r\n"+
			"X-Caido-MCP-Probe: %s\r\nConnection: close\r\n\r\n",
		host, marker,
	)
}

// TestLiveConcurrentSendsKeepTheirOwnBytes is the direct reproduction of the
// defect the 0.57 port introduced: N concurrent sends on ONE shared session.
//
// Measured against 0.58.3 on 2026-10-01 BEFORE the per-session lock, with this
// test driving the unlocked GetSession -> UpdateEntryDraft -> StartTask ->
// PollForEntry sequence the three tool handlers used to run inline: 0 of 6
// probes correct - four were handed an entry holding another probe's request
// (all four pointing at the same entry 4) and two were refused with
// TaskInProgress, i.e. never reached the wire.
//
// Each probe carries its own marker header and asserts that the entry it is
// handed back carries THAT marker. A send whose draft was overwritten by
// another goroutine either never reaches the wire or comes back pointing at an
// entry holding someone else's request — both of which this test fails on.
// Entry ids must also be distinct: StartTask appends, so N sends on one session
// must produce N different entries.
func TestLiveConcurrentSendsKeepTheirOwnBytes(t *testing.T) {
	client := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	conn, host := probeTarget()
	sessionID, err := GetOrCreateSession(ctx, client, "")
	if err != nil {
		t.Fatalf("get or create session: %v", err)
	}
	t.Logf("shared session under test: %s", sessionID)

	const n = 6
	type outcome struct {
		marker  string
		entryID string
		raw     string
		err     error
	}
	results := make([]outcome, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			marker := fmt.Sprintf("probe-%d", i)
			results[i].marker = marker
			raw := base64.StdEncoding.EncodeToString(
				[]byte(probeRaw(host, marker)),
			)
			outcome, err := Send(ctx, client, SendOptions{
				SessionID:           sessionID,
				Conn:                conn,
				RawBase64:           raw,
				UpdateContentLength: true,
				PollTimeout:         20 * time.Second,
			})
			if err != nil {
				results[i].err = fmt.Errorf("send: %w", err)
				return
			}
			if outcome.PollErr != nil {
				results[i].err = fmt.Errorf("poll: %w", outcome.PollErr)
				return
			}
			entry := outcome.Entry
			results[i].entryID = entry.ID
			if entry.Request != nil {
				decoded, derr := base64.StdEncoding.DecodeString(
					entry.Request.Raw,
				)
				if derr == nil {
					results[i].raw = string(decoded)
				}
			}
		}(i)
	}
	wg.Wait()

	seen := map[string]string{}
	var fails int
	for _, r := range results {
		switch {
		case r.err != nil:
			t.Errorf("%s: %v", r.marker, r.err)
			fails++
		case !strings.Contains(r.raw, r.marker):
			t.Errorf("%s: entry %s holds someone else's request (payload "+
				"swap): %q", r.marker, r.entryID, firstLine(r.raw))
			fails++
		default:
			if other, dup := seen[r.entryID]; dup {
				t.Errorf("%s and %s were handed the SAME entry %s",
					other, r.marker, r.entryID)
				fails++
			}
			seen[r.entryID] = r.marker
		}
	}
	if fails == 0 {
		t.Logf("%d concurrent sends each kept their own bytes "+
			"across %d distinct entries", n, len(seen))
	}
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	if len(s) > 80 {
		return s[:80]
	}
	return s
}

// TestLiveBatchPairsEveryLabelWithItsOwnResponse is the direct test for the
// batch path's own version of the same failure.
//
// With 10 requests at concurrency 5 the pool is reused, and the old code
// released a session back to the pool the moment its poll window expired -
// while its task was still running. The next request drafted into that session,
// startReplayTask answered TaskInProgress (which the batch discarded), that
// request never reached the wire, and then the first request's entry became
// active and ITS response was written onto the second request's row. In a scan
// that reads as "the cross-site probe got 200".
func TestLiveBatchPairsEveryLabelWithItsOwnResponse(t *testing.T) {
	client := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	_, host := probeTarget()
	noTLS := false
	const n = 10
	requests := make([]BatchRequest, n)
	for i := range n {
		marker := fmt.Sprintf("batch-%d", i)
		requests[i] = BatchRequest{
			Label: marker,
			Raw:   probeRaw(host, marker),
			Host:  host,
			Port:  9090,
			TLS:   &noTLS,
		}
	}

	results := RunBatch(ctx, client, requests, 5, 256)
	if len(results) != n {
		t.Fatalf("got %d results for %d requests", len(results), n)
	}
	for _, r := range results {
		if r.Error != "" {
			t.Errorf("%s: %s", r.Label, r.Error)
			continue
		}
		if r.Request == nil {
			t.Errorf("%s: no request echoed back", r.Label)
			continue
		}
		var seen string
		for _, h := range r.Request.Headers {
			if strings.EqualFold(h.Name, "X-Caido-MCP-Probe") {
				seen = h.Value
			}
		}
		if seen != r.Label {
			t.Errorf(
				"%s: row carries probe %q - a response was paired with the "+
					"wrong request", r.Label, seen,
			)
		}
		if r.StatusCode == 0 {
			t.Errorf("%s: no status code", r.Label)
		}
	}
}
