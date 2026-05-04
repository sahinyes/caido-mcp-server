package replay

import (
	"context"
	"testing"
	"time"

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
	e := &gen.GetReplayEntryReplayEntry{Id: "1"}
	if entryIsReady(e) {
		t.Fatal("entry with no response and no error must not be ready")
	}
}

func TestEntryIsReady_HasResponse(t *testing.T) {
	e := &gen.GetReplayEntryReplayEntry{
		Id: "1",
		Request: &gen.GetReplayEntryReplayEntryRequest{
			Response: &gen.GetReplayEntryReplayEntryRequestResponse{},
		},
	}
	if !entryIsReady(e) {
		t.Fatal("entry with response must be ready")
	}
}

func TestEntryIsReady_HasError_RequestNil(t *testing.T) {
	// Engine error before request was recorded — must not loop to timeout
	e := &gen.GetReplayEntryReplayEntry{
		Id:    "1",
		Error: strPtr("connection refused"),
	}
	if !entryIsReady(e) {
		t.Fatal("entry with error and nil Request must be ready")
	}
}

func TestEntryIsReady_HasError_RequestNonNil_ResponseNil(t *testing.T) {
	// CF kill: request sent, response killed mid-flight
	e := &gen.GetReplayEntryReplayEntry{
		Id:      "1",
		Error:   strPtr("connection reset by peer"),
		Request: &gen.GetReplayEntryReplayEntryRequest{Id: "req1"},
	}
	if !entryIsReady(e) {
		t.Fatal("entry with error (nil response) must be ready")
	}
}

func TestEntryIsReady_EmptyError_NotReady(t *testing.T) {
	// Empty string error must not trigger early return — still in-flight
	e := &gen.GetReplayEntryReplayEntry{
		Id:    "1",
		Error: strPtr(""),
	}
	if entryIsReady(e) {
		t.Fatal("entry with empty-string error must not be ready")
	}
}

func TestEntryIsReady_BothResponseAndError(t *testing.T) {
	// Partial response + error simultaneously — must surface immediately
	e := &gen.GetReplayEntryReplayEntry{
		Id:    "1",
		Error: strPtr("upstream error"),
		Request: &gen.GetReplayEntryReplayEntryRequest{
			Response: &gen.GetReplayEntryReplayEntryRequestResponse{},
		},
	}
	if !entryIsReady(e) {
		t.Fatal("entry with both response and error must be ready")
	}
}

func TestEntryIsReady_RequestNonNil_ResponseNil_NoError_NotReady(t *testing.T) {
	// Request present but response still pending — must continue polling
	e := &gen.GetReplayEntryReplayEntry{
		Id:      "1",
		Request: &gen.GetReplayEntryReplayEntryRequest{Id: "req1"},
	}
	if entryIsReady(e) {
		t.Fatal("in-flight entry (request without response or error) must not be ready")
	}
}

func BenchmarkEntryIsReady_HasResponse(b *testing.B) {
	e := &gen.GetReplayEntryReplayEntry{
		Id: "bench",
		Request: &gen.GetReplayEntryReplayEntryRequest{
			Response: &gen.GetReplayEntryReplayEntryRequestResponse{},
		},
	}
	b.ResetTimer()
	for b.Loop() {
		entryIsReady(e)
	}
}

func BenchmarkEntryIsReady_HasError(b *testing.B) {
	e := &gen.GetReplayEntryReplayEntry{
		Id:    "bench",
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
