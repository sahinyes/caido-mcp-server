package replay

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	caido "github.com/caido-community/sdk-go"
	gen "github.com/caido-community/sdk-go/graphql"
)

func strPtr(s string) *string { return &s }

func TestGetOrCreateSession_ReturnsInputID(t *testing.T) {
	ctx := context.Background()
	id, err := GetOrCreateSession(ctx, nil, "user-provided-id")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "user-provided-id" {
		t.Fatalf("expected %q, got %q", "user-provided-id", id)
	}
}

func TestResetDefaultSession_UpdatesCache(t *testing.T) {
	ResetDefaultSession("abc")
	t.Cleanup(func() { ResetDefaultSession("") })

	sessionMu.Lock()
	got := defaultSessionID
	sessionMu.Unlock()

	if got != "abc" {
		t.Fatalf("expected cached session %q, got %q", "abc", got)
	}
}

func TestGetOrCreateSession_ReturnsCachedSession(t *testing.T) {
	ResetDefaultSession("abc")
	t.Cleanup(func() { ResetDefaultSession("") })

	ctx := context.Background()
	id, err := GetOrCreateSession(ctx, nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "abc" {
		t.Fatalf("expected %q, got %q", "abc", id)
	}
}

// --- entryIsReady ---

func TestEntryIsReady_NilEntry(t *testing.T) {
	if entryIsReady(nil) {
		t.Fatal("nil entry must not be ready")
	}
}

func TestEntryIsReady_NoResponseNoError(t *testing.T) {
	e := &caido.ReplayEntry{ID: "1"}
	if entryIsReady(e) {
		t.Fatal("entry with no response and no error must not be ready")
	}
}

func TestEntryIsReady_HasResponse(t *testing.T) {
	e := &caido.ReplayEntry{
		ID: "1",
		Request: &caido.ReplayRequest{
			Response: &caido.ReplayResponse{},
		},
	}
	if !entryIsReady(e) {
		t.Fatal("entry with response must be ready")
	}
}

func TestEntryIsReady_HasError_RequestNil(t *testing.T) {
	// Engine error before request was recorded — must not loop to timeout
	e := &caido.ReplayEntry{
		ID:    "1",
		Error: strPtr("connection refused"),
	}
	if !entryIsReady(e) {
		t.Fatal("entry with error and nil Request must be ready")
	}
}

func TestEntryIsReady_HasError_RequestNonNil_ResponseNil(t *testing.T) {
	// CF kill: request sent, response killed mid-flight
	e := &caido.ReplayEntry{
		ID:      "1",
		Error:   strPtr("connection reset by peer"),
		Request: &caido.ReplayRequest{ID: "req1"},
	}
	if !entryIsReady(e) {
		t.Fatal("entry with error (nil response) must be ready")
	}
}

func TestEntryIsReady_EmptyError_NotReady(t *testing.T) {
	// Empty string error must not trigger early return — still in-flight
	e := &caido.ReplayEntry{
		ID:    "1",
		Error: strPtr(""),
	}
	if entryIsReady(e) {
		t.Fatal("entry with empty-string error must not be ready")
	}
}

func TestEntryIsReady_BothResponseAndError(t *testing.T) {
	// Partial response + error simultaneously — must surface immediately
	e := &caido.ReplayEntry{
		ID:    "1",
		Error: strPtr("upstream error"),
		Request: &caido.ReplayRequest{
			Response: &caido.ReplayResponse{},
		},
	}
	if !entryIsReady(e) {
		t.Fatal("entry with both response and error must be ready")
	}
}

func TestEntryIsReady_RequestNonNil_ResponseNil_NoError_NotReady(t *testing.T) {
	// Request present but response still pending — must continue polling
	e := &caido.ReplayEntry{
		ID:      "1",
		Request: &caido.ReplayRequest{ID: "req1"},
	}
	if entryIsReady(e) {
		t.Fatal("in-flight entry (request without response or error) must not be ready")
	}
}

func BenchmarkEntryIsReady_HasResponse(b *testing.B) {
	e := &caido.ReplayEntry{
		ID: "bench",
		Request: &caido.ReplayRequest{
			Response: &caido.ReplayResponse{},
		},
	}
	b.ResetTimer()
	for b.Loop() {
		entryIsReady(e)
	}
}

func BenchmarkEntryIsReady_HasError(b *testing.B) {
	e := &caido.ReplayEntry{
		ID:    "bench",
		Error: strPtr("connection reset"),
	}
	b.ResetTimer()
	for b.Loop() {
		entryIsReady(e)
	}
}

func TestConstants(t *testing.T) {
	if pollInitInterval != 50*time.Millisecond {
		t.Fatalf(
			"expected pollInitInterval 50ms, got %v",
			pollInitInterval,
		)
	}
	if pollMaxInterval != 500*time.Millisecond {
		t.Fatalf(
			"expected pollMaxInterval 500ms, got %v",
			pollMaxInterval,
		)
	}
	if PollMaxRetries != 20 {
		t.Fatalf(
			"expected PollMaxRetries 20, got %d", PollMaxRetries,
		)
	}
}

// --- answered ---
//
// Caido 0.57's draft-then-start flow does not say whether a send appends a new
// entry or executes the active one in place, so answered() has to accept both.
// These cases pin the in-place half, which is the dangerous one: a reused entry
// still carries the PREVIOUS response, and calling that an answer would report
// the wrong response as this send's.

func respEntry(id, respID string) *caido.ReplayEntry {
	return &caido.ReplayEntry{
		ID: id,
		Request: &caido.ReplayRequest{
			Response: &caido.ReplayResponse{ID: respID},
		},
	}
}

func TestAnswered_NewActiveEntry(t *testing.T) {
	st := SendState{SessionID: "s", EntryID: "e1"}
	if !answered(respEntry("e2", "r9"), st, "e2") {
		t.Fatal("a different active entry can only be this send")
	}
}

func TestAnswered_SameEntry_StaleResponse(t *testing.T) {
	st := SendState{SessionID: "s", EntryID: "e1", PrevResponseID: "r1"}
	if answered(respEntry("e1", "r1"), st, "e1") {
		t.Fatal("the response that was already there is not an answer")
	}
}

func TestAnswered_SameEntry_FreshResponse(t *testing.T) {
	st := SendState{SessionID: "s", EntryID: "e1", PrevResponseID: "r1"}
	if !answered(respEntry("e1", "r2"), st, "e1") {
		t.Fatal("a new response id on the same entry is this send's answer")
	}
}

func TestAnswered_SameEntry_FirstResponseEver(t *testing.T) {
	st := SendState{SessionID: "s", EntryID: "e1"}
	if !answered(respEntry("e1", "r1"), st, "e1") {
		t.Fatal("first response on a fresh entry must count")
	}
}

func TestAnswered_SameEntry_StaleError(t *testing.T) {
	st := SendState{
		SessionID: "s", EntryID: "e1", PrevError: "connection refused",
	}
	e := &caido.ReplayEntry{ID: "e1", Error: strPtr("connection refused")}
	if answered(e, st, "e1") {
		t.Fatal("the error that was already there is not an answer")
	}
}

func TestAnswered_SameEntry_FreshError(t *testing.T) {
	st := SendState{
		SessionID: "s", EntryID: "e1", PrevError: "connection refused",
	}
	e := &caido.ReplayEntry{ID: "e1", Error: strPtr("connection reset")}
	if !answered(e, st, "e1") {
		t.Fatal("a different error on the same entry is this send's answer")
	}
}

func TestAnswered_InFlight(t *testing.T) {
	st := SendState{SessionID: "s", EntryID: "e1"}
	e := &caido.ReplayEntry{
		ID: "e1", Request: &caido.ReplayRequest{ID: "req1"},
	}
	if answered(e, st, "e1") {
		t.Fatal("request without response or error is still in flight")
	}
}

func TestHTTPKind_IsHTTP(t *testing.T) {
	if string(HTTPKind) != "HTTP" {
		t.Fatalf("expected HTTP, got %q", string(HTTPKind))
	}
}

// --- settingsMatch ---
//
// 0.57 moved updateContentLength/connectionClose from the per-task input to the
// session, so enforcing them is a persistent write to state the operator may
// own. These cases pin the rule that a send which changes nothing writes
// nothing — and that an unreadable session still gets written once rather than
// being sent with the wrong settings.

func TestSettingsMatch_Equal(t *testing.T) {
	s := &caido.ReplaySessionSettings{
		ConnectionClose: false, UpdateContentLength: true,
	}
	if !settingsMatch(s, false, true) {
		t.Fatal("identical settings must not be rewritten")
	}
}

func TestSettingsMatch_DiffersOnContentLength(t *testing.T) {
	s := &caido.ReplaySessionSettings{
		ConnectionClose: false, UpdateContentLength: false,
	}
	if settingsMatch(s, false, true) {
		t.Fatal("updateContentLength must be enforced when it differs")
	}
}

func TestSettingsMatch_DiffersOnConnectionClose(t *testing.T) {
	s := &caido.ReplaySessionSettings{
		ConnectionClose: true, UpdateContentLength: true,
	}
	if settingsMatch(s, false, true) {
		t.Fatal("connectionClose must be enforced when it differs")
	}
}

func TestSettingsMatch_NilIsMismatch(t *testing.T) {
	if settingsMatch(nil, false, true) {
		t.Fatal("unknown settings must be written, not assumed correct")
	}
}

func TestPlaceholderIdentifiesItself(t *testing.T) {
	if !strings.Contains(placeholderRaw, "X-Caido-MCP-Placeholder") {
		t.Fatal("the seed entry must be recognisable in the Caido UI")
	}
	if placeholderPort != 1 {
		t.Fatalf("placeholder port must stay 1, got %d", placeholderPort)
	}
}

// --- answered, with no readable baseline (strict) ---
//
// When the pre-send read of the entry fails there is no PrevResponseID to
// compare against, and "its response is not the one we saw before" silently
// becomes "any ready entry counts" - which hands back the previous request's
// response as this send's. strict demands the stronger evidence instead.

func TestAnswered_Strict_InPlaceIsNotAnAnswer(t *testing.T) {
	st := SendState{SessionID: "s", EntryID: "e1", strict: true}
	if answered(respEntry("e1", "r9"), st, "e1") {
		t.Fatal("with no baseline, an unchanged active entry must not count")
	}
}

func TestAnswered_Strict_ChangedActiveEntryStillCounts(t *testing.T) {
	st := SendState{SessionID: "s", EntryID: "e1", strict: true}
	if !answered(respEntry("e2", "r9"), st, "e2") {
		t.Fatal("a changed active entry can only be this send")
	}
}

func TestAnswered_NonStrict_FirstResponseOnEntryCounts(t *testing.T) {
	// The mirror case: the baseline WAS read and the entry simply had no
	// response yet. That is not the same as an unreadable baseline.
	st := SendState{SessionID: "s", EntryID: "e1"}
	if !answered(respEntry("e1", "r1"), st, "e1") {
		t.Fatal("a read baseline that was empty must still allow in-place")
	}
}

// --- per-session locking ---

func TestLockSession_SerialisesSameSession(t *testing.T) {
	const goroutines = 8
	var (
		mu       sync.Mutex
		inFlight int
		maxSeen  int
	)
	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release := lockSession("same")
			defer release()

			mu.Lock()
			inFlight++
			if inFlight > maxSeen {
				maxSeen = inFlight
			}
			mu.Unlock()

			time.Sleep(time.Millisecond)

			mu.Lock()
			inFlight--
			mu.Unlock()
		}()
	}
	wg.Wait()
	if maxSeen != 1 {
		t.Fatalf(
			"two sends held one session at once (max in flight %d)", maxSeen,
		)
	}
}

func TestLockSession_DifferentSessionsRunInParallel(t *testing.T) {
	// batch_send's whole design is one session per concurrent request, so the
	// lock must not serialise across sessions. Each goroutine waits for the
	// other to be holding its own lock; if the locks were shared this
	// deadlocks and the test fails on the timeout instead of hanging.
	aHeld := make(chan struct{})
	bHeld := make(chan struct{})
	done := make(chan struct{})

	go func() {
		release := lockSession("a")
		defer release()
		close(aHeld)
		<-bHeld
		done <- struct{}{}
	}()
	go func() {
		release := lockSession("b")
		defer release()
		close(bHeld)
		<-aHeld
		done <- struct{}{}
	}()

	for range 2 {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("sends on different sessions were serialised")
		}
	}
}

func TestLockSession_ReleaseIsIdempotentAndForgetsTheEntry(t *testing.T) {
	release := lockSession("transient")

	sessionLocksMu.Lock()
	_, present := sessionLocks["transient"]
	sessionLocksMu.Unlock()
	if !present {
		t.Fatal("a held lock must be in the map")
	}

	release()
	release() // a double release must not unlock someone else's turn

	sessionLocksMu.Lock()
	_, present = sessionLocks["transient"]
	sessionLocksMu.Unlock()
	if present {
		t.Fatal("an unheld lock must be dropped, or the map grows forever")
	}
}

func TestLockSession_WaiterKeepsTheEntryAlive(t *testing.T) {
	first := lockSession("shared")

	waiting := make(chan struct{})
	got := make(chan func(), 1)
	go func() {
		close(waiting)
		got <- lockSession("shared")
	}()
	<-waiting

	// Give the waiter time to register its reference, then prove that
	// releasing the holder does not delete an entry someone is queued on -
	// which would hand the next arrival a DIFFERENT mutex for the same
	// session, i.e. no mutual exclusion at all.
	time.Sleep(50 * time.Millisecond)
	first()

	select {
	case release := <-got:
		release()
	case <-time.After(5 * time.Second):
		t.Fatal("the queued waiter never acquired the lock")
	}

	sessionLocksMu.Lock()
	_, present := sessionLocks["shared"]
	sessionLocksMu.Unlock()
	if present {
		t.Fatal("the entry must be gone once nobody holds or waits")
	}
}

// --- startReplayTask payload errors ---
//
// The mutation answers with a payload whose error is OPTIONAL, so four of the
// five variants used to arrive as err == nil: the task never started, nothing
// was sent, and the only symptom was "timed out waiting for response" 8.75 s
// later with the real reason discarded where it was known.

func taskResp(
	e gen.StartReplayTaskStartReplayTaskStartReplayTaskPayloadErrorStartReplayTaskError,
) *gen.StartReplayTaskResponse {
	resp := &gen.StartReplayTaskResponse{}
	if e != nil {
		resp.StartReplayTask.Error = &e
	}
	return resp
}

func TestTaskPayloadError_NilAndEmptyAreNotErrors(t *testing.T) {
	if err := taskPayloadError(nil); err != nil {
		t.Fatalf("nil response: %v", err)
	}
	if err := taskPayloadError(taskResp(nil)); err != nil {
		t.Fatalf("no payload error: %v", err)
	}
}

func TestTaskPayloadError_AllFiveVariantsSurface(t *testing.T) {
	cases := []struct {
		name string
		err  gen.StartReplayTaskStartReplayTaskStartReplayTaskPayloadErrorStartReplayTaskError
		want string
		busy bool
	}{
		{
			name: "TaskInProgressUserError",
			err: &gen.StartReplayTaskStartReplayTaskStartReplayTaskPayloadErrorTaskInProgressUserError{
				Typename: strPtr("TaskInProgressUserError"),
			},
			want: "already running a task",
			busy: true,
		},
		{
			name: "UnknownIdUserError",
			err: &gen.StartReplayTaskStartReplayTaskStartReplayTaskPayloadErrorUnknownIdUserError{
				Typename: strPtr("UnknownIdUserError"),
			},
			want: "UnknownIdUserError",
		},
		{
			name: "PermissionDeniedUserError",
			err: &gen.StartReplayTaskStartReplayTaskStartReplayTaskPayloadErrorPermissionDeniedUserError{
				Typename: strPtr("PermissionDeniedUserError"),
			},
			want: "PermissionDeniedUserError",
		},
		{
			name: "CloudUserError",
			err: &gen.StartReplayTaskStartReplayTaskStartReplayTaskPayloadErrorCloudUserError{
				Typename: strPtr("CloudUserError"),
			},
			want: "CloudUserError",
		},
		{
			name: "OtherUserError",
			err: &gen.StartReplayTaskStartReplayTaskStartReplayTaskPayloadErrorOtherUserError{
				Typename: strPtr("OtherUserError"),
				Code:     "out_of_scope",
			},
			want: "out_of_scope",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := taskPayloadError(taskResp(tc.err))
			if err == nil {
				t.Fatal("payload refusal reported as success")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
			if tc.busy != errors.Is(err, ErrTaskInProgress) {
				t.Fatalf(
					"ErrTaskInProgress=%v for %s", !tc.busy, tc.name,
				)
			}
			if !tc.busy && !errors.Is(err, ErrStartTaskRefused) {
				t.Fatalf("%s must wrap ErrStartTaskRefused", tc.name)
			}
		})
	}
}

func TestTaskPayloadError_MissingTypenameStillReports(t *testing.T) {
	err := taskPayloadError(taskResp(
		&gen.StartReplayTaskStartReplayTaskStartReplayTaskPayloadErrorCloudUserError{},
	))
	if err == nil {
		t.Fatal("a refusal with no __typename must still be an error")
	}
	if !strings.Contains(err.Error(), "unknown error") {
		t.Fatalf("unexpected message: %v", err)
	}
}
