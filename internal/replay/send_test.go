package replay

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	caido "github.com/caido-community/sdk-go"
)

func testConn() caido.ReplayConnection {
	return caido.ReplayConnection{Host: "example.test", Port: 80}
}

func sendProbe(
	ctx context.Context, client *caido.Client, sessionID, m string,
	poll time.Duration,
) (SendOutcome, error) {
	return Send(ctx, client, SendOptions{
		SessionID:           sessionID,
		Conn:                testConn(),
		RawBase64:           probeBytes(m),
		UpdateContentLength: true,
		PollTimeout:         poll,
	})
}

func answeredMarker(t *testing.T, out SendOutcome) string {
	t.Helper()
	if out.Entry == nil || out.Entry.Request == nil {
		t.Fatalf("no answered request on the entry (pollErr=%v)", out.PollErr)
	}
	return marker(out.Entry.Request.Raw)
}

// TestSend_SerialisesConcurrentSendsOnOneSession is the offline, DETERMINISTIC
// test for the defect the 0.57 port introduced.
//
// The live test fires six sends and hopes they overlap. This one forces the
// overlap: the fake parks the first send inside startReplayTask, exactly between
// its draft and its result, and then a second send is started on the same shared
// session. If the lock did not cover the whole sequence, the second send would
// write its draft over the first send's bytes right there - which is the
// measured 0-of-6 failure. The assertion is on the fake's own draft log, so it
// fails for the right reason rather than on a timing coincidence.
func TestSend_SerialisesConcurrentSendsOnOneSession(t *testing.T) {
	f, client := newFakeCaido(t)
	ctx := context.Background()

	parked := make(chan struct{})
	release := make(chan struct{})
	var relOnce sync.Once
	releaseAll := func() { relOnce.Do(func() { close(release) }) }
	// Never leave the parked send holding a connection: a t.Fatal before the
	// release would block httptest's Close and hide the real failure.
	t.Cleanup(releaseAll)

	var once sync.Once
	f.mu.Lock()
	f.beforeStartTask = func(string) {
		once.Do(func() {
			close(parked)
			<-release
		})
	}
	f.mu.Unlock()

	type res struct {
		out SendOutcome
		err error
	}
	aDone := make(chan res, 1)
	go func() {
		out, err := sendProbe(ctx, client, "", "A", 5*time.Second)
		aDone <- res{out, err}
	}()

	select {
	case <-parked:
	case <-time.After(5 * time.Second):
		t.Fatal("the first send never reached startReplayTask")
	}

	bDone := make(chan res, 1)
	bStarted := make(chan struct{})
	go func() {
		close(bStarted)
		out, err := sendProbe(ctx, client, "", "B", 5*time.Second)
		bDone <- res{out, err}
	}()
	<-bStarted
	// Long enough for an unlocked B to have done GetSession + GetEntry +
	// UpdateEntryDraft against a local httptest server many times over.
	time.Sleep(300 * time.Millisecond)

	drafts := f.snapshotDrafts()
	f.mu.Lock()
	ops := strings.Join(f.ops, ",")
	f.mu.Unlock()
	t.Logf("drafts=%v ops=%s", drafts, ops)
	if len(drafts) != 1 || !strings.HasSuffix(drafts[0], "=A") {
		t.Fatalf(
			"a second send wrote into the session while the first was mid-send: %v",
			drafts,
		)
	}

	releaseAll()
	a := <-aDone
	b := <-bDone
	if a.err != nil || b.err != nil {
		t.Fatalf("send errors: a=%v b=%v", a.err, b.err)
	}
	if got := answeredMarker(t, a.out); got != "A" {
		t.Errorf("send A was answered with %q", got)
	}
	if got := answeredMarker(t, b.out); got != "B" {
		t.Errorf("send B was answered with %q", got)
	}
	if a.out.EntryID == b.out.EntryID {
		t.Errorf("both sends were handed entry %s", a.out.EntryID)
	}
}

// TestSend_DifferentSessionsRunInParallel is the other half of the lock's
// contract: batch_send's whole design is one session per concurrent request, so
// serialising across sessions would be a throughput bug dressed as a fix.
func TestSend_DifferentSessionsRunInParallel(t *testing.T) {
	f, client := newFakeCaido(t)
	ctx := context.Background()

	other, _, err := NewSession(ctx, client)
	if err != nil {
		t.Fatalf("second session: %v", err)
	}

	parked := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	f.mu.Lock()
	f.beforeStartTask = func(sid string) {
		if sid == other {
			return
		}
		once.Do(func() {
			close(parked)
			<-release
		})
	}
	f.mu.Unlock()

	go func() { _, _ = sendProbe(ctx, client, "", "parked", 5*time.Second) }()
	select {
	case <-parked:
	case <-time.After(5 * time.Second):
		t.Fatal("the first send never reached startReplayTask")
	}
	defer close(release)

	done := make(chan error, 1)
	go func() {
		out, err := sendProbe(ctx, client, other, "free", 5*time.Second)
		if err == nil && out.Entry == nil {
			err = errors.New("no entry")
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the unrelated session failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a send on a DIFFERENT session was blocked by the parked one")
	}
}

// TestSend_DoesNotResendAfterAmbiguousStartTask pins the rule the second audit
// found missing: when startReplayTask fails at the transport level the request
// may already be on the wire, so nothing may send it again.
func TestSend_DoesNotResendAfterAmbiguousStartTask(t *testing.T) {
	f, client := newFakeCaido(t)
	ctx := context.Background()

	f.mu.Lock()
	f.startTaskErr = func(string) (string, bool) { return "", true }
	f.mu.Unlock()

	before := f.createdCount()
	_, err := sendProbe(ctx, client, "", "once", time.Second)
	if err == nil {
		t.Fatal("an ambiguous startReplayTask must be reported, not retried")
	}
	if !errors.Is(err, ErrSendAmbiguous) {
		t.Fatalf("error does not mark the send as ambiguous: %v", err)
	}
	if drafts := f.snapshotDrafts(); len(drafts) != 1 {
		t.Errorf("the request was drafted %d times, want 1: %v",
			len(drafts), drafts)
	}
	// The shared session was created, but no REPLACEMENT may have been made.
	if created := f.createdCount() - before; created != 1 {
		t.Errorf("%d sessions created, want 1 (a rotation happened)", created)
	}
}

// TestSend_RotatesSharedSessionWhenBusy is the other side of it: a payload
// refusal proves the task did NOT start, so replacing the shared session and
// sending once is correct.
func TestSend_RotatesSharedSessionWhenBusy(t *testing.T) {
	f, client := newFakeCaido(t)
	ctx := context.Background()

	first, err := GetOrCreateSession(ctx, client, "")
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	f.mu.Lock()
	f.startTaskErr = func(sid string) (string, bool) {
		if sid == first {
			return "TaskInProgressUserError", false
		}
		return "", false
	}
	f.mu.Unlock()

	out, err := sendProbe(ctx, client, "", "rotated", 5*time.Second)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if !out.Rotated || out.SessionID == first {
		t.Fatalf("expected a rotation away from %s, got %s (rotated=%v)",
			first, out.SessionID, out.Rotated)
	}
	if got := answeredMarker(t, out); got != "rotated" {
		t.Errorf("answered with %q", got)
	}
}

// TestSend_DoesNotRotateCallerNamedSession is B6: a sessionId the caller passed
// is never swapped behind its back.
func TestSend_DoesNotRotateCallerNamedSession(t *testing.T) {
	f, client := newFakeCaido(t)
	ctx := context.Background()

	mine, _, err := NewSession(ctx, client)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	f.mu.Lock()
	f.startTaskErr = func(string) (string, bool) {
		return "TaskInProgressUserError", false
	}
	f.mu.Unlock()

	before := f.createdCount()
	out, err := sendProbe(ctx, client, mine, "mine", time.Second)
	if err == nil {
		t.Fatal("a busy named session must be an error, not a silent swap")
	}
	if !errors.Is(err, ErrTaskInProgress) {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Rotated || f.createdCount() != before {
		t.Error("a replacement session was created for a named session")
	}
}

// TestSend_PollTimeoutReportsNoEntryID is B2: a timeout hands back no entry id
// at all rather than the session's current active entry, which may be the
// PREVIOUS send's request and response.
func TestSend_PollTimeoutReportsNoEntryID(t *testing.T) {
	f, client := newFakeCaido(t)
	ctx := context.Background()

	f.mu.Lock()
	f.noResponse = true
	f.mu.Unlock()

	out, err := sendProbe(ctx, client, "", "slow", 300*time.Millisecond)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if out.PollErr == nil {
		t.Fatal("expected a poll timeout")
	}
	if out.EntryID != "" || out.Entry != nil {
		t.Fatalf("a timed-out send reported entry %q", out.EntryID)
	}
	if out.SessionID == "" {
		t.Error("the session id is the one honest handle and must be reported")
	}
}

// TestSend_SurvivesTransientReads is B5: a blip is not an answer.
func TestSend_SurvivesTransientReads(t *testing.T) {
	f, client := newFakeCaido(t)
	ctx := context.Background()

	if _, err := GetOrCreateSession(ctx, client, ""); err != nil {
		t.Fatalf("session: %v", err)
	}
	// The failures have to land in the POLL, not in the pre-send reads: a
	// shared session that cannot be read is replaced, which is a different
	// path with its own test.
	var once sync.Once
	f.mu.Lock()
	f.beforeStartTask = func(string) {
		once.Do(func() {
			f.mu.Lock()
			f.failReads = pollTransientLimit - 1
			f.mu.Unlock()
		})
	}
	f.mu.Unlock()

	out, err := sendProbe(ctx, client, "", "blip", 5*time.Second)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if got := answeredMarker(t, out); got != "blip" {
		t.Errorf("answered with %q", got)
	}
}

func TestSend_GivesUpOnARunOfReadFailures(t *testing.T) {
	f, client := newFakeCaido(t)
	ctx := context.Background()

	if _, err := GetOrCreateSession(ctx, client, ""); err != nil {
		t.Fatalf("session: %v", err)
	}
	var once sync.Once
	f.mu.Lock()
	f.beforeStartTask = func(string) {
		once.Do(func() {
			f.mu.Lock()
			f.failReads = 500 // every read fails from here on
			f.mu.Unlock()
		})
	}
	f.mu.Unlock()

	out, err := sendProbe(ctx, client, "", "dead", 5*time.Second)
	if err != nil {
		t.Fatalf("the send itself succeeded, so this must be a poll error: %v", err)
	}
	if out.PollErr == nil {
		t.Fatal("a poll whose every read fails must report it")
	}
	if !strings.Contains(out.PollErr.Error(), "consecutive read failures") {
		t.Errorf("unexpected poll error: %v", out.PollErr)
	}
	if out.Entry != nil || out.EntryID != "" {
		t.Error("no entry may be reported when the poll never read one")
	}
}

// TestSend_RejectsPayloadWithNeitherTaskNorError: both halves are nullable, and
// "no task" means the task did not start.
func TestSend_RejectsPayloadWithNeitherTaskNorError(t *testing.T) {
	f, client := newFakeCaido(t)
	ctx := context.Background()

	f.mu.Lock()
	f.startTaskNoTask = true
	f.mu.Unlock()

	_, err := sendProbe(ctx, client, "", "null", time.Second)
	if err == nil {
		t.Fatal("a payload with no task must not read as a started task")
	}
	if !errors.Is(err, ErrStartTaskRefused) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestSend_AbandonsSharedSessionAfterProjectSwitch is the note-21 class in
// code: session ids are numbered PER PROJECT and the open project is global, so
// a cached id survives a switch and then names a different, live session.
func TestSend_AbandonsSharedSessionAfterProjectSwitch(t *testing.T) {
	f, client := newFakeCaido(t)
	ctx := context.Background()

	first, err := sendProbe(ctx, client, "", "before", 5*time.Second)
	if err != nil {
		t.Fatalf("first send: %v", err)
	}

	f.setProject("project-2")

	second, err := sendProbe(ctx, client, "", "after", 5*time.Second)
	if err != nil {
		t.Fatalf("second send: %v", err)
	}
	if second.SessionID == first.SessionID {
		t.Fatalf(
			"the cached session %s was reused after the project changed",
			first.SessionID,
		)
	}
	if got := answeredMarker(t, second); got != "after" {
		t.Errorf("answered with %q", got)
	}
}

// TestSend_IgnoresActivePointerMovedToAnOlderEntry: "the active entry moved" is
// not proof on its own. Another writer this process cannot lock - the operator's
// Caido UI, a second MCP client - can move the pointer to an EXISTING entry,
// and accepting that hands this caller someone else's request and response.
func TestSend_IgnoresAnEntryAnotherWriterPointedAt(t *testing.T) {
	f, client := newFakeCaido(t)
	ctx := context.Background()

	// Two completed sends, so the session holds a READY entry that is not the
	// active one.
	first, err := sendProbe(ctx, client, "", "old-1", 5*time.Second)
	if err != nil {
		t.Fatalf("first send: %v", err)
	}
	session := first.SessionID
	second, err := sendProbe(ctx, client, session, "old-2", 5*time.Second)
	if err != nil {
		t.Fatalf("second send: %v", err)
	}
	// The entry the foreign writer will select. MEASURED, never an id literal
	// and never an offset from another id - note 21's rule applied to the test.
	foreign := first.EntryID
	if foreign == "" || foreign == second.EntryID {
		t.Fatalf(
			"test setup: need a ready entry that is not the active one, got "+
				"foreign=%q active=%q", foreign, second.EntryID,
		)
	}

	// The third send gets no answer of its own, and the pointer is moved to
	// that older entry AFTER the send's own pre-send read - the only timing in
	// which the guard under test runs. afterReads=1 consumes exactly that read.
	f.mu.Lock()
	f.noResponse = true
	f.moveActiveTo = map[string]pointerMove{session: {to: foreign, afterReads: 1}}
	f.mu.Unlock()

	out, err := sendProbe(ctx, client, session, "mine", 600*time.Millisecond)
	if err != nil {
		t.Fatalf("third send: %v", err)
	}
	if out.Entry != nil {
		t.Fatalf(
			"a send was answered with entry %s, which another writer moved the "+
				"pointer to (marker %q)",
			out.Entry.ID, marker(out.Entry.Request.Raw),
		)
	}
	if out.PollErr == nil {
		t.Error("expected the poll to report that no answer arrived")
	}
	// Proof the move actually landed: without it the guard is never exercised
	// and this test would pass on a build with the guard deleted.
	f.mu.Lock()
	moved := len(f.moveActiveTo) == 0
	f.mu.Unlock()
	if !moved {
		t.Fatal(
			"the foreign pointer move never happened, so nothing was tested " +
				"(not enough reads before the poll timeout?)",
		)
	}
}

func TestSend_KeepsPollingPastTheRetryCountWhenItHasADeadline(t *testing.T) {
	// PollMaxRetries is a FALLBACK for a caller who brought no deadline. While
	// it was the only bound, batch_send's declared 15 s window per request was
	// dead text - it got the count's 8.75 s - so a target answering in 10 s was
	// reported as a timeout AND cost the batch a pool slot.
	//
	// Shrinking the count is what makes this provable in milliseconds instead
	// of nine seconds: the answer is arranged to arrive on a read the count
	// would never reach, including the one late read Send does after a poll
	// gives up.
	restore := PollMaxRetries
	PollMaxRetries = 2
	t.Cleanup(func() { PollMaxRetries = restore })

	f, client := newFakeCaido(t)
	f.mu.Lock()
	f.noResponse = true
	f.answerAfterReads = 5
	f.mu.Unlock()

	out, err := sendProbe(
		context.Background(), client, "", "late", 3*time.Second,
	)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if out.PollErr != nil {
		t.Fatalf(
			"the poll gave up at the retry count instead of honouring its "+
				"3s deadline: %v", out.PollErr,
		)
	}
	if out.Entry == nil {
		t.Fatal("no entry returned")
	}
	if got := marker(out.Entry.Request.Raw); got != "late" {
		t.Fatalf("answered with another send's bytes: marker %q", got)
	}
}
