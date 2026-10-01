package replay

import (
	"context"
	"strings"
	"testing"
	"time"

	caido "github.com/caido-community/sdk-go"
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
