package replay

import (
	"context"
	"encoding/base64"
	"errors"
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

	// pollTransientLimit is how many CONSECUTIVE failed reads end a wait.
	//
	// A single refused read is not evidence that the send failed: the task is
	// running on Caido's side either way, so giving up on the first blip
	// reports a request that actually succeeded as failed — and in batch_send,
	// where every request has its own 15 s window, it also strands the session
	// with a task still on it. Only a RUN of failures means the instance is
	// genuinely unreachable.
	pollTransientLimit = 3

	// lateReadTimeout bounds the one last read done after a poll gives up.
	lateReadTimeout = 2 * time.Second

	// sendOverallTimeout bounds a whole send when the caller brought no
	// deadline.
	//
	// It is not a nicety. Everything in Send runs while holding the session's
	// lock, and the SDK's http.Client has NO Timeout of its own, so a Caido that
	// accepts a connection and never answers would hold that lock forever - and
	// an MCP tool call frequently arrives with no deadline at all. A bounded
	// holder is also what makes waiting for the lock bounded.
	sendOverallTimeout = 60 * time.Second

	// DefaultSendPollTimeout is how long a single tool call waits for its
	// answer. It used to be implicit in PollMaxRetries; it is explicit now,
	// because the retry count is a FALLBACK cap and not a deadline.
	DefaultSendPollTimeout = 10 * time.Second
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
	// ErrTaskInProgress is startReplayTask refusing because the session is
	// already running a task.
	ErrTaskInProgress = errors.New(
		"replay session is already running a task",
	)
	// ErrStartTaskRefused is any other startReplayTask payload refusal.
	ErrStartTaskRefused = errors.New("caido refused to start the replay task")
	// ErrSendAmbiguous marks a startReplayTask that failed at the TRANSPORT
	// level: the mutation may already have been delivered and executed, so the
	// request may already be on the wire. Nothing may resend it.
	ErrSendAmbiguous = errors.New(
		"startReplayTask failed in flight; the request may already have been sent",
	)
)

var (
	defaultSessionID      string
	defaultSessionProject string
	sessionMu             sync.Mutex
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
// caches a shared one FOR THE PROJECT THAT IS OPEN NOW.
//
// The open project is part of the cache key, and that is the whole point. Caido
// numbers replay sessions PER PROJECT, and which project is open is global
// instance state shared with the operator's browser and every other client - so
// a cached bare id survives a project switch and then names a DIFFERENT, live
// session in the new project. The next default send would draft over it: the
// operator's request replaced by ours, no error anywhere. That is the same
// confusion that destroyed nine of the operator's replay sessions on
// 2026-10-01, one layer down.
//
// A project that cannot be read counts as a mismatch. Litter (one unused
// session) is a cheaper mistake than writing into the wrong project.
func GetOrCreateSession(
	ctx context.Context, client *caido.Client, inputID string,
) (string, error) {
	if inputID != "" {
		return inputID, nil
	}
	project := currentProjectID(ctx, client)

	sessionMu.Lock()
	defer sessionMu.Unlock()
	if defaultSessionID != "" && project != "" &&
		project == defaultSessionProject {
		return defaultSessionID, nil
	}
	id, _, err := NewSession(ctx, client)
	if err != nil {
		return "", fmt.Errorf("create replay session: %w", err)
	}
	defaultSessionID = id
	defaultSessionProject = project
	return defaultSessionID, nil
}

// currentProjectID returns the open project's id, or "" if it cannot be read.
func currentProjectID(ctx context.Context, client *caido.Client) string {
	resp, err := client.Projects.GetCurrent(ctx)
	if err != nil || resp == nil || resp.CurrentProject == nil {
		return ""
	}
	return resp.CurrentProject.Project.Id
}

// ResetDefaultSession replaces the cached shared session, keeping the project
// it belongs to: a rotation happens inside the project that was just verified.
func ResetDefaultSession(newID string) {
	sessionMu.Lock()
	defaultSessionID = newID
	sessionMu.Unlock()
}

// sharedSessionIs reports whether id is still the cached shared session.
func sharedSessionIs(id string) bool {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	return defaultSessionID == id
}

// Per-session serialisation.
//
// Caido 0.57 replaced the one atomic
// startReplayTask(sessionId, {connection, raw, settings}) mutation with
// draft-then-start, so a send is now THREE round trips and the request bytes
// live in shared, writable server-side state (the session's active entry)
// between them. MCP tool handlers run CONCURRENTLY — the Go SDK calls
// jsonrpc2.Async(ctx) for every tools/call except initialize
// (mcp/server.go) — and every send that names no session shares one
// process-wide session, so two concurrent sends interleave:
//
//	A: GetSession -> active E0        B: GetSession -> active E0
//	A: draft(E0, bytesA)              B: draft(E0, bytesB)   <- overwrites A
//	A: StartTask                      B: StartTask -> TaskInProgress
//
// A's bytes never reach the wire, B's go out under A's name, and both poll the
// same entry. Measured against 0.58.3 on 2026-10-01 with six concurrent sends
// on one session: four were handed an entry holding another probe's request,
// two were refused outright, 0 of 6 correct. SendState's in-place guard cannot
// see this — it was built for SEQUENTIAL reuse of a pooled session.
//
// The lock is held across the whole sequence INCLUDING the poll, because "my
// entry" is defined by the active entry moving and a concurrent send moves it.
// Locks are per session id, so sends on different sessions stay parallel and
// batch_send's pool keeps its concurrency. Entries are reference-counted and
// dropped when nobody holds or waits for them: a long-lived server must not
// accumulate one mutex per session it has ever touched, and a waiter must not
// be handed a different mutex than the holder has.
type sessionLockEntry struct {
	mu   sync.Mutex
	refs int
}

var (
	sessionLocksMu sync.Mutex
	sessionLocks   = map[string]*sessionLockEntry{}
)

// lockSession blocks until sessionID is free and returns its release function,
// which is safe to call more than once.
func lockSession(sessionID string) func() {
	sessionLocksMu.Lock()
	e := sessionLocks[sessionID]
	if e == nil {
		e = &sessionLockEntry{}
		sessionLocks[sessionID] = e
	}
	e.refs++
	sessionLocksMu.Unlock()

	e.mu.Lock()

	var once sync.Once
	return func() {
		once.Do(func() {
			e.mu.Unlock()
			sessionLocksMu.Lock()
			e.refs--
			if e.refs == 0 {
				delete(sessionLocks, sessionID)
			}
			sessionLocksMu.Unlock()
		})
	}
}

// IsTaskInProgress reports whether startReplayTask refused because the session
// is already running a task.
func IsTaskInProgress(resp *gen.StartReplayTaskResponse) bool {
	return caido.IsTaskInProgress(resp)
}

// taskPayloadError turns startReplayTask's payload error into a Go error.
//
// startReplayTask answers with a payload carrying an OPTIONAL error, so a
// refusal arrives with err == nil. Five variants exist — TaskInProgress,
// UnknownId, PermissionDenied, Cloud and Other — and only the first was ever
// read. The other four meant the task never started, nothing was sent, and the
// single visible symptom was "timed out waiting for response" 8.75 s later,
// with the real reason (a session deleted under us, a permission refusal)
// discarded at the point it was known.
func taskPayloadError(resp *gen.StartReplayTaskResponse) error {
	if resp == nil {
		return nil
	}
	errPtr := resp.StartReplayTask.GetError()
	if errPtr == nil || *errPtr == nil {
		// Both halves of the payload are nullable in the schema, and the
		// operation selects `task { id }`, so a started task always comes back
		// with one. No task and no error means the task did NOT start - reading
		// that as success buys an 8.75 s wait on a session where nothing is
		// running, and then a message telling the caller the task may still be
		// running. start_automate.go guards the same shape for Automate.
		if resp.StartReplayTask.Task == nil {
			return fmt.Errorf(
				"%w: payload carried neither a task nor an error",
				ErrStartTaskRefused,
			)
		}
		return nil
	}
	v := *errPtr
	name := "unknown error"
	if tn := v.GetTypename(); tn != nil && *tn != "" {
		name = *tn
	}
	switch e := v.(type) {
	case *gen.StartReplayTaskStartReplayTaskStartReplayTaskPayloadErrorTaskInProgressUserError:
		return ErrTaskInProgress
	case *gen.StartReplayTaskStartReplayTaskStartReplayTaskPayloadErrorOtherUserError:
		return fmt.Errorf(
			"%w: %s (code %s)", ErrStartTaskRefused, name, e.GetCode(),
		)
	default:
		return fmt.Errorf("%w: %s", ErrStartTaskRefused, name)
	}
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

	// preEntries is the set of entry ids the session had BEFORE the send, as
	// far as it could be read. A moved active entry only proves this send's
	// answer if it moved to an entry that did not exist yet: another writer
	// this process cannot lock - the operator's Caido UI, a second MCP client,
	// the CLI - can select an EXISTING entry and move the pointer without
	// sending anything, and "it moved" alone would then hand this caller
	// someone else's request and response. Caido returns the first 100 entries,
	// so on a very long session this degrades back to the positional rule
	// rather than becoming wrong.
	preEntries map[string]bool

	// strict is set when the pre-send baseline could NOT be read. With no
	// baseline, "its response is not the one we saw before" degenerates into
	// "any ready entry counts", which hands back the previous request's
	// response as this send's. An unreadable baseline therefore demands the
	// stronger evidence instead: the active entry must have moved. That is
	// the shape every send takes in practice — StartTask was measured to
	// APPEND — so the cost is a timeout in the one case where an in-place
	// send coincides with an unreadable entry, not a wrong answer.
	strict bool
}

// SendOptions describes one send through Caido's Replay engine.
type SendOptions struct {
	// SessionID is the caller's session, or "" to use the shared one.
	SessionID string
	Conn      caido.ReplayConnection
	RawBase64 string
	// UpdateContentLength and ConnectionClose are SESSION settings in 0.57+,
	// not per-task input.
	UpdateContentLength bool
	ConnectionClose     bool
	// PollTimeout bounds the wait for the answer. Zero leaves it to ctx.
	PollTimeout time.Duration
}

// SendOutcome is what a completed send knows.
type SendOutcome struct {
	// SessionID is the session the request was actually sent on, which is not
	// the requested one when Rotated is set.
	SessionID string
	Rotated   bool
	// Entry is this send's entry, or nil when the answer did not arrive.
	Entry *caido.ReplayEntry
	// EntryID is set only when Entry is, i.e. only when the entry is provably
	// this send's. A poll that gives up reports no id at all rather than the
	// session's current active entry, which may be the PREVIOUS send's
	// request and response.
	EntryID string
	// PollErr says why Entry is nil. The task may still be running.
	PollErr error
}

// Send performs one complete replay send: resolve the session, serialise
// everything that touches it, write the draft, start the task, wait for the
// answer.
//
// This is the only way to send: see the sessionLockEntry comment for why the
// three round trips have to be atomic per session, and why a lower-level entry
// point would be a defect waiting to be reintroduced.
func Send(
	ctx context.Context, client *caido.Client, opts SendOptions,
) (SendOutcome, error) {
	explicit := opts.SessionID != ""

	// See sendOverallTimeout: a lock holder must be bounded, and an MCP tool
	// call often arrives with no deadline at all.
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, sendOverallTimeout)
		defer cancel()
	}

	// Resolve, lock, then CHECK THE RESOLUTION STILL HOLDS. While a waiter is
	// queued on the shared session's lock, the holder may have rotated it away
	// precisely because it was unusable - so the id captured before the wait can
	// be the one already proven dead, and every waiter would repeat the same
	// failure and leave one orphaned session behind. One re-resolve is enough:
	// the second attempt is taken on whatever the holder installed.
	var (
		sessionID string
		unlock    func()
	)
	for attempt := 0; ; attempt++ {
		var err error
		sessionID, err = GetOrCreateSession(ctx, client, opts.SessionID)
		if err != nil {
			return SendOutcome{}, err
		}
		unlock = lockSession(sessionID)
		if explicit || attempt > 0 || sharedSessionIs(sessionID) {
			break
		}
		unlock()
	}
	out := SendOutcome{SessionID: sessionID}
	defer func() { unlock() }()

	resp, st, err := sendRaw(ctx, client, sessionID, opts)
	if err == nil {
		err = taskPayloadError(resp)
	}
	if err != nil {
		// A session the CALLER named is not silently swapped: the whole point
		// of passing sessionId is that the request goes to that session, and
		// "sent somewhere else, reported as success" is worse than a failure.
		if explicit {
			return out, err
		}
		// Nor is a send retried when it is not known whether it already
		// happened. startReplayTask is a mutation; if its REPLY was lost, Caido
		// may have started the task and the request may be on the wire. Sending
		// it again on a fresh session would put it there twice - the exact
		// duplicate the vendored retryTransport refuses to create one layer
		// down, re-created one layer up. Every other sendRaw failure happens
		// before startReplayTask, so rotating on those is safe.
		if errors.Is(err, ErrSendAmbiguous) {
			return out, err
		}
		// The shared session is unusable for this send: busy, deleted under
		// us (switching project invalidates every cached id), or the write
		// failed. Replace it. The old lock goes first — the new session has
		// its own, and holding two is pointless.
		unlock()
		newID, _, createErr := NewSession(ctx, client)
		if createErr != nil {
			unlock = func() {}
			return out, fmt.Errorf(
				"%w (replacing the shared session also failed: %v)",
				err, createErr,
			)
		}
		ResetDefaultSession(newID)
		sessionID = newID
		out.SessionID = newID
		out.Rotated = true
		unlock = lockSession(newID)

		resp, st, err = sendRaw(ctx, client, sessionID, opts)
		if err == nil {
			err = taskPayloadError(resp)
		}
		if err != nil {
			return out, fmt.Errorf("retry on a fresh session: %w", err)
		}
	}

	pollCtx := ctx
	if opts.PollTimeout > 0 {
		var cancel context.CancelFunc
		pollCtx, cancel = context.WithTimeout(ctx, opts.PollTimeout)
		defer cancel()
	}

	entry, pollErr := PollForEntry(pollCtx, client, st)
	if pollErr == nil {
		out.Entry = entry
		out.EntryID = entry.ID
		return out, nil
	}

	// One last read, still under this session's lock, so nothing else can have
	// moved the active entry meanwhile. Anything that satisfies the send's own
	// guard IS this send's answer and merely arrived a tick late; anything else
	// is not reported as an entry at all.
	//
	// Skipped when the CALLER cancelled: a caller that went away does not want
	// two more seconds of work done in its name. A caller whose deadline simply
	// ran out is exactly who this read is for, which is why it runs on a
	// detached context.
	if !errors.Is(ctx.Err(), context.Canceled) {
		lateCtx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx), lateReadTimeout,
		)
		defer cancel()
		if e, activeID, rerr := readActive(
			lateCtx, client, sessionID,
		); rerr == nil && answered(e, st, activeID) {
			out.Entry = e
			out.EntryID = e.ID
			return out, nil
		}
	}

	out.PollErr = pollErr
	return out, nil
}

// sendRaw writes the draft and starts the task. The caller must hold
// sessionID's lock.
func sendRaw(
	ctx context.Context,
	client *caido.Client,
	sessionID string,
	opts SendOptions,
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
	if len(sess.Entries) > 0 {
		st.preEntries = make(map[string]bool, len(sess.Entries))
		for _, e := range sess.Entries {
			st.preEntries[e.ID] = true
		}
	}

	// The baseline is best effort for SENDING — a session that cannot be read
	// back still sends — but not for INTERPRETING the answer: see
	// SendState.strict.
	prev, perr := client.Replay.GetEntry(ctx, st.EntryID, HTTPKind)
	switch {
	case perr != nil || prev == nil:
		st.strict = true
	default:
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
	if !settingsMatch(
		sess.Settings, opts.ConnectionClose, opts.UpdateContentLength,
	) {
		if err := client.Replay.UpdateSessionSettings(
			ctx, sessionID, opts.ConnectionClose, opts.UpdateContentLength,
		); err != nil {
			return nil, st, fmt.Errorf("update session settings: %w", err)
		}
	}
	if err := client.Replay.UpdateEntryDraft(
		ctx, st.EntryID, opts.Conn, opts.RawBase64, nil,
	); err != nil {
		return nil, st, fmt.Errorf("update entry draft: %w", err)
	}

	resp, err := client.Replay.StartTask(ctx, sessionID)
	if err != nil {
		// Marked, not wrapped away: the caller has to be able to tell this
		// failure (outcome unknown, may already be on the wire) from the four
		// above it (nothing was sent).
		return resp, st, fmt.Errorf("%w: %v", ErrSendAmbiguous, err)
	}
	return resp, st, nil
}

// readActive reads a session's active entry. A session with no active entry
// yet is not an error: StartTask appends, so there is a moment with nothing to
// look at.
func readActive(
	ctx context.Context, client *caido.Client, sessionID string,
) (*caido.ReplayEntry, string, error) {
	sess, err := client.Replay.GetSession(ctx, sessionID)
	if err != nil {
		return nil, "", err
	}
	if sess == nil || sess.ActiveEntryID == "" {
		return nil, "", nil
	}
	e, err := client.Replay.GetEntry(ctx, sess.ActiveEntryID, HTTPKind)
	if err != nil {
		return nil, sess.ActiveEntryID, err
	}
	return e, sess.ActiveEntryID, nil
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
		// See SendState.preEntries: moved is not enough, it has to be new.
		return !st.preEntries[activeID]
	}
	if st.strict {
		return false
	}
	if e.Request != nil && e.Request.Response != nil {
		return e.Request.Response.ID != st.PrevResponseID
	}
	return e.Error != nil && *e.Error != "" && *e.Error != st.PrevError
}

// PollForEntry waits for the session's active entry to carry this send's
// response or error. The caller must hold st.SessionID's lock: the guard in
// answered reads the active entry, and a concurrent send moves it.
func PollForEntry(
	ctx context.Context, client *caido.Client, st SendState,
) (*caido.ReplayEntry, error) {
	if st.SessionID == "" {
		return nil, errors.New("poll: no session to poll")
	}
	_, hasDeadline := ctx.Deadline()
	interval := pollInitInterval
	transient := 0
	var lastErr error
	for attempt := 0; ; attempt++ {
		// PollMaxRetries is a FALLBACK cap, not a schedule. Its backoff sums to
		// 8.75 s, so while it was the only bound, every deadline longer than
		// that was dead text: batch_send declared a 15 s window per request and
		// got 8.75 s, and a target answering in 10 s was reported as a timeout
		// AND cost the batch a pool slot. With a deadline the deadline decides;
		// the count still bounds a caller who brought neither.
		if !hasDeadline && attempt >= PollMaxRetries {
			break
		}
		e, activeID, err := readActive(ctx, client, st.SessionID)
		if err != nil {
			transient++
			lastErr = err
			if transient >= pollTransientLimit {
				return nil, fmt.Errorf(
					"poll: %d consecutive read failures: %w", transient, err,
				)
			}
		} else {
			transient = 0
			if answered(e, st, activeID) {
				return e, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, pollEndedErr(ctx, lastErr)
		case <-time.After(interval):
		}
		interval = min(interval*2, pollMaxInterval)
	}
	if lastErr != nil {
		return nil, fmt.Errorf(
			"timed out waiting for response (last read failed: %v)", lastErr,
		)
	}
	return nil, errors.New("timed out waiting for response")
}

// pollEndedErr names why a poll stopped. A deadline that ran out is a timeout
// and says so; a caller that cancelled gets context.Canceled, because the two
// mean different things to everything upstream.
func pollEndedErr(ctx context.Context, lastErr error) error {
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ctx.Err()
	}
	if lastErr != nil {
		return fmt.Errorf(
			"timed out waiting for response (last read failed: %v)", lastErr,
		)
	}
	return errors.New("timed out waiting for response")
}
