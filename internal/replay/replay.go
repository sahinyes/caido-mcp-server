package replay

import (
	"context"
	"encoding/base64"
	"fmt"
	"sync"
	"time"

	caido "github.com/caido-community/sdk-go"
	gen "github.com/caido-community/sdk-go/graphql"
)

const (
	pollInitInterval = 50 * time.Millisecond
	pollMaxInterval  = 500 * time.Millisecond
	PollMaxRetries   = 20
)

// HTTPKind is the only replay session kind this server creates.
//
// Caido 0.57 made `kind` a REQUIRED argument on createReplaySession, because
// Replay gained WS sessions. Omitting it does not degrade, it fails the whole
// mutation — and since every send path creates a session first, that one
// missing argument is what broke send_request, replay_request, batch_send and
// create_replay_session at once against 0.58.3.
const HTTPKind = gen.ReplaySessionKindHttp

var (
	defaultSessionID string
	sessionMu        sync.Mutex
)

// A replay session cannot be created empty and then filled: Caido has no
// mutation that adds an entry to an existing session (the MutationRoot offers
// only createReplaySession, updateReplayEntryDraft, clearReplayEntryDraft,
// setActiveReplaySessionEntry and startReplayTask), and startReplayTask needs an
// entry to run. So a session created without a `requestSource` has no active
// entry, nothing can be drafted into it, and it stays unusable for its whole
// life — measured against 0.58.3, where every send failed with "replay session
// N has no active entry to draft into" and the fallback session failed the same
// way, because it was created the same way. Every creation path therefore seeds
// an entry, with the real request where one is in hand and with this inert
// placeholder where it is not. Port 1 is deliberate: if anything ever did start
// a task on an unused placeholder, it would fail at once against nothing.
const (
	placeholderRaw = "GET / HTTP/1.1\r\nHost: localhost\r\n" +
		"X-Caido-MCP-Placeholder: unsent\r\n\r\n"
	placeholderHost = "127.0.0.1"
	placeholderPort = 1
)

func placeholderConn() caido.ReplayConnection {
	return caido.ReplayConnection{
		Host: placeholderHost, Port: placeholderPort, IsTLS: false,
	}
}

// PlaceholderSource is the seed for sessions created before a request exists.
func PlaceholderSource() *gen.RequestSourceInput {
	return caido.NewRawRequestSource(
		placeholderConn(),
		base64.StdEncoding.EncodeToString([]byte(placeholderRaw)),
	)
}

// NewSessionFromRaw creates an HTTP replay session already holding rawBase64 as
// its first entry, and returns both ids.
func NewSessionFromRaw(
	ctx context.Context,
	client *caido.Client,
	conn caido.ReplayConnection,
	rawBase64 string,
) (sessionID, entryID string, err error) {
	return client.Replay.CreateSession(ctx, &gen.CreateReplaySessionInput{
		Kind:          HTTPKind,
		RequestSource: caido.NewRawRequestSource(conn, rawBase64),
	})
}

// NewSession creates an HTTP replay session seeded with the placeholder, for
// callers that do not have the request yet.
func NewSession(
	ctx context.Context, client *caido.Client,
) (sessionID, entryID string, err error) {
	return NewSessionFromRaw(
		ctx, client, placeholderConn(),
		base64.StdEncoding.EncodeToString([]byte(placeholderRaw)),
	)
}

// GetOrCreateSession returns the caller's session id, or lazily creates and
// caches a shared one.
func GetOrCreateSession(
	ctx context.Context, client *caido.Client, inputID string,
) (string, error) {
	if inputID != "" {
		return inputID, nil
	}
	sessionMu.Lock()
	defer sessionMu.Unlock()
	if defaultSessionID != "" {
		return defaultSessionID, nil
	}
	id, _, err := NewSession(ctx, client)
	if err != nil {
		return "", fmt.Errorf("create replay session: %w", err)
	}
	defaultSessionID = id
	return defaultSessionID, nil
}

func ResetDefaultSession(newID string) {
	sessionMu.Lock()
	defaultSessionID = newID
	sessionMu.Unlock()
}

// IsTaskInProgress reports whether startReplayTask refused because the session
// is already running a task.
func IsTaskInProgress(resp *gen.StartReplayTaskResponse) bool {
	return caido.IsTaskInProgress(resp)
}

// SendState is the pre-send snapshot PollForEntry needs in order to tell this
// send's answer from one that was already sitting on the entry.
//
// It exists because Caido 0.57 replaced the single
// startReplayTask(sessionId, {connection, raw, settings}) mutation with
// draft-then-start, and the schema does not say whether starting a task
// executes the active entry IN PLACE or appends a new one. ReplayTask.replayEntry
// would name it outright, but the SDK's operation selects only `task { id }`,
// so both shapes are handled here rather than guessed at: a changed active
// entry can only be this send, and an unchanged one counts only when its
// response (or its error) is not the one we saw before sending. Without that
// second guard an in-place send would return the PREVIOUS response instantly,
// which is indistinguishable from a very fast reply — and batch_send reuses
// pooled sessions, so it is the common case there, not an edge case.
type SendState struct {
	SessionID      string
	EntryID        string // the entry the draft was written to
	PrevResponseID string // response already on that entry, if any
	PrevError      string // error already on that entry, if any
}

// SendRaw replaces the SDK's removed ReplaySDK.SendRequest: the raw request is
// written to the session's active entry as a draft, then a task is started on
// the session. The two per-request settings the old mutation carried
// (UpdateContentLength, ConnectionClose) now live on the session.
func SendRaw(
	ctx context.Context,
	client *caido.Client,
	sessionID string,
	conn caido.ReplayConnection,
	rawBase64 string,
	updateContentLength, connectionClose bool,
) (*gen.StartReplayTaskResponse, SendState, error) {
	st := SendState{SessionID: sessionID}

	sess, err := client.Replay.GetSession(ctx, sessionID)
	if err != nil {
		return nil, st, fmt.Errorf("resolve active entry: %w", err)
	}
	if sess == nil || sess.ActiveEntryID == "" {
		return nil, st, fmt.Errorf(
			"replay session %s has no active entry to draft into", sessionID,
		)
	}
	st.EntryID = sess.ActiveEntryID

	// Best effort: a session that cannot be read back still sends, it just
	// loses the in-place guard, so this error is not fatal.
	if prev, perr := client.Replay.GetEntry(
		ctx, st.EntryID, HTTPKind,
	); perr == nil && prev != nil {
		if prev.Request != nil && prev.Request.Response != nil {
			st.PrevResponseID = prev.Request.Response.ID
		}
		if prev.Error != nil {
			st.PrevError = *prev.Error
		}
	}

	// UpdateContentLength and ConnectionClose used to ride on the per-task
	// input; in 0.57+ the only place they exist is the SESSION. This server
	// enforces updateContentLength because its callers hand it raw request
	// TEXT and a stale Content-Length breaks the request — but enforcing it is
	// a persistent write to a session the operator may own, so it happens only
	// when the session does not already say what the send needs. A send that
	// changes nothing therefore mutates nothing.
	if !settingsMatch(sess.Settings, connectionClose, updateContentLength) {
		if err := client.Replay.UpdateSessionSettings(
			ctx, sessionID, connectionClose, updateContentLength,
		); err != nil {
			return nil, st, fmt.Errorf("update session settings: %w", err)
		}
	}
	if err := client.Replay.UpdateEntryDraft(
		ctx, st.EntryID, conn, rawBase64, nil,
	); err != nil {
		return nil, st, fmt.Errorf("update entry draft: %w", err)
	}

	resp, err := client.Replay.StartTask(ctx, sessionID)
	return resp, st, err
}

// ActiveEntryID returns a session's active entry id, or "" if it cannot be
// read. Used on the poll-failure path, where a best-effort id still lets the
// caller point at get_replay_entry.
func ActiveEntryID(
	ctx context.Context, client *caido.Client, sessionID string,
) string {
	sess, err := client.Replay.GetSession(ctx, sessionID)
	if err != nil || sess == nil {
		return ""
	}
	return sess.ActiveEntryID
}

// settingsMatch reports whether a session already carries the settings a send
// needs. Unknown settings (a session read that returned none) count as a
// mismatch: writing once is cheaper than sending with the wrong ones.
func settingsMatch(
	s *caido.ReplaySessionSettings, connectionClose, updateContentLength bool,
) bool {
	if s == nil {
		return false
	}
	return s.ConnectionClose == connectionClose &&
		s.UpdateContentLength == updateContentLength
}

func entryIsReady(e *caido.ReplayEntry) bool {
	if e == nil {
		return false
	}
	if e.Error != nil && *e.Error != "" {
		return true
	}
	return e.Request != nil && e.Request.Response != nil
}

// answered reports whether e carries the result of the send described by st.
func answered(e *caido.ReplayEntry, st SendState, activeID string) bool {
	if !entryIsReady(e) {
		return false
	}
	if activeID != st.EntryID {
		return true
	}
	if e.Request != nil && e.Request.Response != nil {
		return e.Request.Response.ID != st.PrevResponseID
	}
	return e.Error != nil && *e.Error != "" && *e.Error != st.PrevError
}

// PollForEntry waits for the session's active entry to carry this send's
// response or error.
func PollForEntry(
	ctx context.Context, client *caido.Client, st SendState,
) (*caido.ReplayEntry, error) {
	if st.SessionID == "" {
		return nil, fmt.Errorf("poll: no session to poll")
	}
	interval := pollInitInterval
	for range PollMaxRetries {
		sess, err := client.Replay.GetSession(ctx, st.SessionID)
		if err != nil {
			return nil, fmt.Errorf("poll session: %w", err)
		}
		if sess != nil && sess.ActiveEntryID != "" {
			e, err := client.Replay.GetEntry(
				ctx, sess.ActiveEntryID, HTTPKind,
			)
			if err != nil {
				return nil, fmt.Errorf("poll entry: %w", err)
			}
			if answered(e, st, sess.ActiveEntryID) {
				return e, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
		interval = min(interval*2, pollMaxInterval)
	}
	return nil, fmt.Errorf("timed out waiting for response")
}
