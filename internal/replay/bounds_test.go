package replay

import (
	"context"
	"testing"
	"time"

	caido "github.com/caido-community/sdk-go"
)

// hangFake returns a fake that accepts the named operations and never answers
// them, with the release wired so the server can be shut down even if the test
// fails because nothing bounded the wait.
func hangFake(t *testing.T, ops ...string) (*fakeCaido, *caido.Client) {
	t.Helper()
	f, client := newFakeCaido(t)
	release := make(chan struct{})
	// Registered AFTER newFakeCaido's srv.Close, so it runs BEFORE it (LIFO).
	t.Cleanup(func() { close(release) })
	f.mu.Lock()
	f.hangOn = map[string]bool{}
	for _, op := range ops {
		f.hangOn[op] = true
	}
	f.hangRelease = release
	f.mu.Unlock()
	return f, client
}

// returnsWithin runs fn and reports whether it returned inside d. It never
// waits longer than d, so a missing bound fails the test in d rather than
// hanging the binary until the package timeout.
func returnsWithin(d time.Duration, fn func()) bool {
	done := make(chan struct{})
	go func() { defer close(done); fn() }()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

// A Caido that accepts the connection and never answers is the case the SDK
// has no defence against: its http.Client has no Timeout. Everything in Send
// runs while holding the session's lock, and an MCP tool call frequently
// arrives with no deadline at all, so without sendOverallTimeout that lock is
// held forever and every later send on the session queues behind it.
//
// The hang is placed on the draft write deliberately: it is inside the locked
// region and before the poll, so neither PollTimeout nor PollMaxRetries can be
// what ends this wait.
func TestSend_BoundsItselfWhenTheCallerBroughtNoDeadline(t *testing.T) {
	was := sendOverallTimeout
	sendOverallTimeout = 400 * time.Millisecond
	t.Cleanup(func() { sendOverallTimeout = was })

	_, client := hangFake(t, "UpdateReplayEntryDraft")

	var err error
	start := time.Now()
	ok := returnsWithin(6*time.Second, func() {
		_, err = sendProbe(
			context.Background(), client, "", "hang", 2*time.Second,
		)
	})
	if !ok {
		t.Fatal(
			"Send never returned against a hung Caido: nothing bounded it, " +
				"and it is holding the session lock",
		)
	}
	if err == nil {
		t.Fatal("a hung server produced a successful send")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf(
			"Send took %v; that is not the %v bound it was given",
			elapsed, sendOverallTimeout,
		)
	}
}

// A caller that DID bring a deadline keeps it: the bound above must not
// replace a shorter one, or a batch's per-request window becomes dead text the
// way PollMaxRetries once made batch_send's 15 s window dead text.
func TestSend_KeepsTheCallersDeadlineRatherThanTheOverallBound(t *testing.T) {
	was := sendOverallTimeout
	sendOverallTimeout = 30 * time.Second
	t.Cleanup(func() { sendOverallTimeout = was })

	_, client := hangFake(t, "UpdateReplayEntryDraft")

	ctx, cancel := context.WithTimeout(
		context.Background(), 400*time.Millisecond,
	)
	defer cancel()

	var err error
	start := time.Now()
	ok := returnsWithin(6*time.Second, func() {
		_, err = sendProbe(ctx, client, "", "hang", 2*time.Second)
	})
	if !ok {
		t.Fatal("Send outlived the caller's own 400ms deadline")
	}
	if err == nil {
		t.Fatal("a hung server produced a successful send")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf(
			"Send took %v: the caller's 400ms deadline was replaced by the "+
				"overall bound", elapsed,
		)
	}
}

// Cleanup runs on a context detached from the caller's - the batch is over and
// its deadline usually with it - so RunBatch's last round trip is bounded by
// cleanupTimeout and by nothing else. Without it a hung Caido holds RunBatch
// open forever with every result already in hand.
func TestPoolCleanup_BoundsItselfAgainstAHungCaido(t *testing.T) {
	was := cleanupTimeout
	cleanupTimeout = 400 * time.Millisecond
	t.Cleanup(func() { cleanupTimeout = was })

	f, client := hangFake(t)
	pool, err := NewSessionPool(context.Background(), client, 2)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	// Hang only AFTER the pool is built, so the creates succeed and there is
	// something to delete.
	f.mu.Lock()
	f.hangOn["DeleteReplaySessions"] = true
	f.mu.Unlock()

	start := time.Now()
	ok := returnsWithin(6*time.Second, func() {
		pool.Cleanup(context.Background())
	})
	if !ok {
		t.Fatal(
			"Cleanup never returned against a hung Caido: RunBatch would " +
				"hold open forever with every result already in hand",
		)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf(
			"Cleanup took %v; that is not the %v bound it was given",
			elapsed, cleanupTimeout,
		)
	}
}
