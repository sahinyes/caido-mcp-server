package replay

import (
	"context"
	"strings"
	"testing"
	"time"
)

func batchProbes(n int) []BatchRequest {
	noTLS := false
	reqs := make([]BatchRequest, n)
	for i := range n {
		m := "row-" + string(rune('a'+i))
		reqs[i] = BatchRequest{
			Label: m,
			Raw: "GET /health HTTP/1.1\r\nHost: example.test\r\n" +
				"X-Caido-MCP-Probe: " + m + "\r\n\r\n",
			Host: "example.test",
			Port: 80,
			TLS:  &noTLS,
		}
	}
	return reqs
}

// fastPolls shortens the per-request poll window for tests that have to let it
// expire. Without it each such request costs 15 s and the suite stops being run.
func fastPolls(t *testing.T) {
	t.Helper()
	was := batchPollTimeout
	batchPollTimeout = 400 * time.Millisecond
	t.Cleanup(func() { batchPollTimeout = was })
}

func rowMarker(t *testing.T, r BatchResult) string {
	t.Helper()
	if r.Request == nil {
		return "<no request>"
	}
	for _, h := range r.Request.Headers {
		if strings.EqualFold(h.Name, "X-Caido-MCP-Probe") {
			return h.Value
		}
	}
	return "<unmarked>"
}

// TestRunBatch_PairsEveryLabelWithItsOwnResponse is B1 offline: no row may be
// handed another row's response.
func TestRunBatch_PairsEveryLabelWithItsOwnResponse(t *testing.T) {
	_, client := newFakeCaido(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	reqs := batchProbes(8)
	results := RunBatch(ctx, client, reqs, 3, 256)
	if len(results) != len(reqs) {
		t.Fatalf("got %d rows for %d requests", len(results), len(reqs))
	}
	for i, r := range results {
		if r.Error != "" {
			t.Errorf("%s: %s", r.Label, r.Error)
			continue
		}
		if r.Label != reqs[i].Label {
			t.Errorf("row %d carries label %q, want %q",
				i, r.Label, reqs[i].Label)
		}
		if got := rowMarker(t, r); got != r.Label {
			t.Errorf("row %q carries request %q - a response was paired with "+
				"the wrong request", r.Label, got)
		}
		if r.StatusCode != 200 {
			t.Errorf("%s: status %d", r.Label, r.StatusCode)
		}
	}
}

// TestRunBatch_RetiresASessionWhoseTaskMayStillRun is the other half of B1: a
// session whose poll window expired is not handed to the next request, because
// its task may still be running on it.
func TestRunBatch_RetiresASessionWhoseTaskMayStillRun(t *testing.T) {
	f, client := newFakeCaido(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	fastPolls(t)
	// Nothing ever answers, so every request exhausts its poll window.
	f.mu.Lock()
	f.noResponse = true
	f.mu.Unlock()

	before := f.createdCount()
	results := RunBatch(ctx, client, batchProbes(4), 2, 256)

	for _, r := range results {
		if r.Error == "" {
			t.Errorf("%s: expected a poll error, got status %d",
				r.Label, r.StatusCode)
		}
		if r.StatusCode != 0 {
			t.Errorf("%s: reported status %d for a request that never answered",
				r.Label, r.StatusCode)
		}
	}
	// 2 pooled sessions + one replacement per retired session.
	if created := f.createdCount() - before; created <= 2 {
		t.Errorf(
			"%d sessions created: a session whose task may still be running "+
				"was returned to the pool instead of being retired", created,
		)
	}
}

// TestRunBatch_DoesNotHangWhenThePoolDrains is the HIGH finding from the second
// audit. Every retired session fails to be replaced, so the pool collapses -
// and because an MCP tool call often has no deadline, the old code's Acquire
// would have waited forever and RunBatch would never have returned.
func TestRunBatch_DoesNotHangWhenThePoolDrains(t *testing.T) {
	f, client := newFakeCaido(t)

	fastPolls(t)
	f.mu.Lock()
	f.noResponse = true // force every request to retire its session
	f.mu.Unlock()

	// Deliberately NO deadline: that is the case that used to hang.
	ctx := context.Background()

	pool, err := NewSessionPool(ctx, client, 2)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(func() { pool.Cleanup(context.Background()) })

	// From here on no replacement can be created.
	f.mu.Lock()
	f.failCreate = true
	f.mu.Unlock()

	done := make(chan []BatchResult, 1)
	go func() {
		results := make([]BatchResult, 0, 6)
		for _, br := range batchProbes(6) {
			results = append(results, executeSingle(ctx, client, pool, br, 256))
		}
		done <- results
	}()

	select {
	case results := <-done:
		drained := 0
		for _, r := range results {
			if strings.Contains(r.Error, ErrPoolDrained.Error()) {
				drained++
			}
			if r.Error == "" {
				t.Errorf("%s reported success against a pool that drained",
					r.Label)
			}
		}
		if drained == 0 {
			t.Error("no row reported the drained pool by name")
		}
	case <-time.After(60 * time.Second):
		t.Fatal("a batch whose pool drained never returned")
	}
}

// TestSessionPool_CleanupDeletesEveryCreatedSession covers C2 including the
// replacements: a pool that retires sessions must still delete them.
func TestSessionPool_CleanupDeletesEveryCreatedSession(t *testing.T) {
	f, client := newFakeCaido(t)
	ctx := context.Background()

	pool, err := NewSessionPool(ctx, client, 3)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	id, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	pool.Retire(ctx, id)

	pool.Cleanup(ctx)

	f.mu.Lock()
	remaining := len(f.sessions)
	f.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("%d sessions left behind in Caido", remaining)
	}
}

// TestSessionPool_PartialCreateFailureLeavesNothingBehind is C2 proper.
func TestSessionPool_PartialCreateFailureLeavesNothingBehind(t *testing.T) {
	f, client := newFakeCaido(t)
	ctx := context.Background()

	// Let two creates succeed, then refuse: NewSessionPool must clean up the
	// ones it already made instead of orphaning them in the operator's Replay
	// list.
	var calls int
	f.mu.Lock()
	f.sessions = map[string]*fakeSession{}
	f.mu.Unlock()
	for i := 0; i < 2; i++ {
		if _, _, err := NewSession(ctx, client); err != nil {
			t.Fatalf("warmup create: %v", err)
		}
		calls++
	}
	f.mu.Lock()
	f.failCreate = true
	warm := len(f.sessions)
	f.mu.Unlock()

	if _, err := NewSessionPool(ctx, client, 4); err == nil {
		t.Fatal("a pool that cannot be filled must fail")
	}
	f.mu.Lock()
	left := len(f.sessions)
	f.mu.Unlock()
	if left != warm {
		t.Fatalf("the failed pool left %d extra sessions behind", left-warm)
	}
}
