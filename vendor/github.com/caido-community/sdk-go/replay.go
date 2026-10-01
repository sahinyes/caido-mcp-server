package caido

import (
	"context"
	"fmt"

	gen "github.com/caido-community/sdk-go/graphql"
)

// ReplaySDK provides operations on replay sessions and entries.
//
// Caido 0.57.0 reshaped the replay GraphQL surface: ReplaySession and
// ReplayEntry became interfaces (HTTP and WS variants) and the
// startReplayTask mutation no longer carries the request. To send a
// request you now update an entry's draft (UpdateEntryDraft) and then
// start the task (StartTask), or seed a fresh session with a request via
// CreateSession's RequestSource. This SDK unwraps the HTTP variants into
// the stable domain structs below so callers do not deal with the wire
// interface types directly.
type ReplaySDK struct {
	client *Client
}

// ReplayConnection identifies the target of a replay entry.
type ReplayConnection struct {
	Host  string
	Port  int
	IsTLS bool
}

// ReplayResponse is the response captured on a replay entry's request.
type ReplayResponse struct {
	ID            string
	StatusCode    int
	RoundtripTime int
	Length        int
	Raw           string // base64-encoded
}

// ReplayRequest is the request stored on a replay entry.
type ReplayRequest struct {
	ID       string
	Method   string
	Host     string
	Port     int
	Path     string
	Query    string
	IsTLS    bool
	Raw      string // base64-encoded
	Response *ReplayResponse
}

// ReplayEntry is an HTTP replay entry in its stable domain form.
type ReplayEntry struct {
	ID         string
	Error      *string
	Connection ReplayConnection
	Raw        string // base64-encoded
	Request    *ReplayRequest
}

// ReplaySessionCollectionRef is a lightweight collection reference.
type ReplaySessionCollectionRef struct {
	ID   string
	Name string
}

// ReplaySessionSettings holds HTTP replay session settings.
type ReplaySessionSettings struct {
	ConnectionClose     bool
	UpdateContentLength bool
}

// ReplaySession is an HTTP replay session in its stable domain form.
type ReplaySession struct {
	ID            string
	Name          string
	ActiveEntryID string // empty when the session has no active entry
	Collection    ReplaySessionCollectionRef
	Settings      *ReplaySessionSettings
	Entries       []ReplayEntry // populated by GetSession (entry summaries)
}

// ListSessionsOptions configures session listing.
type ListSessionsOptions struct {
	First  *int
	Last   *int
	After  *string
	Before *string
}

// ListSessions returns paginated replay sessions (raw wire response).
func (s *ReplaySDK) ListSessions(
	ctx context.Context, opts *ListSessionsOptions,
) (*gen.ListReplaySessionsResponse, error) {
	var o ListSessionsOptions
	if opts != nil {
		o = *opts
	}
	return gen.ListReplaySessions(
		ctx, s.client.GraphQL,
		o.First, o.Last, o.After, o.Before,
	)
}

// SessionSummary is a lightweight session listing entry.
type SessionSummary struct {
	ID            string
	Name          string
	ActiveEntryID string
}

// ListSessionSummaries returns replay sessions as flat domain summaries,
// unwrapping the HTTP/WS interface variants.
func (s *ReplaySDK) ListSessionSummaries(
	ctx context.Context, opts *ListSessionsOptions,
) ([]SessionSummary, error) {
	resp, err := s.ListSessions(ctx, opts)
	if err != nil {
		return nil, err
	}
	out := make([]SessionSummary, 0, len(resp.ReplaySessions.Edges))
	for _, edge := range resp.ReplaySessions.Edges {
		node := edge.Node
		if node == nil {
			continue
		}
		summary := SessionSummary{ID: node.GetId(), Name: node.GetName()}
		if httpNode, ok := node.(*gen.ListReplaySessionsReplaySessionsReplaySessionConnectionEdgesReplaySessionEdgeNodeReplaySessionHttp); ok {
			if httpNode.ActiveEntry != nil {
				summary.ActiveEntryID = (*httpNode.ActiveEntry).GetId()
			}
		}
		out = append(out, summary)
	}
	return out, nil
}

// GetSession returns a single replay session in stable domain form.
// Returns nil when the session does not exist.
func (s *ReplaySDK) GetSession(
	ctx context.Context, id string,
) (*ReplaySession, error) {
	resp, err := gen.GetReplaySession(ctx, s.client.GraphQL, id)
	if err != nil {
		return nil, err
	}
	if resp.ReplaySession == nil {
		return nil, nil
	}
	sess := *resp.ReplaySession
	httpSess, ok := sess.(*gen.GetReplaySessionReplaySessionReplaySessionHttp)
	if !ok {
		// Non-HTTP (WS) session: return minimal info.
		return &ReplaySession{
			ID:   sess.GetId(),
			Name: sess.GetName(),
		}, nil
	}
	out := &ReplaySession{
		ID:   httpSess.Id,
		Name: httpSess.Name,
		Collection: ReplaySessionCollectionRef{
			ID:   httpSess.Collection.Id,
			Name: httpSess.Collection.Name,
		},
	}
	if httpSess.ActiveEntry != nil {
		out.ActiveEntryID = (*httpSess.ActiveEntry).GetId()
	}
	if httpSess.Settings != nil {
		out.Settings = &ReplaySessionSettings{
			ConnectionClose:     httpSess.Settings.ConnectionClose,
			UpdateContentLength: httpSess.Settings.UpdateContentLength,
		}
	}
	for _, edge := range httpSess.Entries.Edges {
		httpEntry, ok := edge.Node.(*gen.GetReplaySessionReplaySessionReplaySessionHttpEntriesReplayEntryConnectionEdgesReplayEntryEdgeNodeReplayEntryHttp)
		if !ok {
			continue
		}
		out.Entries = append(out.Entries, ReplayEntry{
			ID:  httpEntry.Id,
			Raw: httpEntry.Raw,
			Connection: ReplayConnection{
				Host:  httpEntry.Connection.Host,
				Port:  httpEntry.Connection.Port,
				IsTLS: httpEntry.Connection.IsTLS,
			},
		})
	}
	return out, nil
}

// GetEntry returns a single replay entry in stable domain form.
// kind selects the session kind (defaults to HTTP when empty).
func (s *ReplaySDK) GetEntry(
	ctx context.Context, id string, kind gen.ReplaySessionKind,
) (*ReplayEntry, error) {
	if kind == "" {
		kind = gen.ReplaySessionKindHttp
	}
	resp, err := gen.GetReplayEntry(ctx, s.client.GraphQL, id, kind)
	if err != nil {
		return nil, err
	}
	if resp.ReplayEntry == nil {
		return nil, nil
	}
	httpEntry, ok := (*resp.ReplayEntry).(*gen.GetReplayEntryReplayEntryReplayEntryHttp)
	if !ok {
		return nil, fmt.Errorf("replay entry %s is not an HTTP entry", id)
	}
	return mapHTTPEntry(httpEntry), nil
}

func mapHTTPEntry(
	e *gen.GetReplayEntryReplayEntryReplayEntryHttp,
) *ReplayEntry {
	out := &ReplayEntry{
		ID:    e.Id,
		Error: e.Error,
		Raw:   e.Raw,
		Connection: ReplayConnection{
			Host:  e.Connection.Host,
			Port:  e.Connection.Port,
			IsTLS: e.Connection.IsTLS,
		},
	}
	if e.Request != nil {
		req := &ReplayRequest{
			ID:     e.Request.Id,
			Method: e.Request.Method,
			Host:   e.Request.Host,
			Port:   e.Request.Port,
			Path:   e.Request.Path,
			Query:  e.Request.Query,
			IsTLS:  e.Request.IsTls,
			Raw:    e.Request.Raw,
		}
		if e.Request.Response != nil {
			req.Response = &ReplayResponse{
				ID:            e.Request.Response.Id,
				StatusCode:    e.Request.Response.StatusCode,
				RoundtripTime: e.Request.Response.RoundtripTime,
				Length:        e.Request.Response.Length,
				Raw:           e.Request.Response.Raw,
			}
		}
		out.Request = req
	}
	return out
}

// ListCollections returns paginated replay session collections.
func (s *ReplaySDK) ListCollections(
	ctx context.Context, opts *ListSessionsOptions,
) (*gen.ListReplaySessionCollectionsResponse, error) {
	var o ListSessionsOptions
	if opts != nil {
		o = *opts
	}
	return gen.ListReplaySessionCollections(
		ctx, s.client.GraphQL,
		o.First, o.Last, o.After, o.Before,
	)
}

// CreateSession creates a new replay session and returns its ID and the
// ID of its active entry (empty when the session was created without a
// request source and therefore has no entry yet).
func (s *ReplaySDK) CreateSession(
	ctx context.Context, input *gen.CreateReplaySessionInput,
) (sessionID, activeEntryID string, err error) {
	if input == nil {
		input = &gen.CreateReplaySessionInput{}
	}
	if input.Kind == "" {
		input.Kind = gen.ReplaySessionKindHttp
	}
	resp, err := gen.CreateReplaySession(ctx, s.client.GraphQL, *input)
	if err != nil {
		return "", "", err
	}
	if resp.CreateReplaySession.Session == nil {
		return "", "", fmt.Errorf("create replay session returned no session")
	}
	session := *resp.CreateReplaySession.Session
	sessionID = session.GetId()
	if httpSess, ok := session.(*gen.CreateReplaySessionCreateReplaySessionCreateReplaySessionPayloadSessionReplaySessionHttp); ok {
		if httpSess.ActiveEntry != nil {
			activeEntryID = (*httpSess.ActiveEntry).GetId()
		}
	}
	return sessionID, activeEntryID, nil
}

// NewRawRequestSource builds a RequestSourceInput that seeds a new
// session with a raw HTTP request (base64-encoded), creating its first
// entry. Use this with CreateSession so the session has an entry to send.
func NewRawRequestSource(
	conn ReplayConnection, rawBase64 string,
) *gen.RequestSourceInput {
	var sni *string
	return &gen.RequestSourceInput{
		Raw: &gen.RequestRawInput{
			ConnectionInfo: gen.ConnectionInfoInput{
				Host:  conn.Host,
				Port:  conn.Port,
				IsTLS: conn.IsTLS,
				SNI:   sni,
			},
			Raw: rawBase64,
		},
	}
}

// CreateSessionWithRaw creates a replay session seeded with a raw HTTP
// request (base64-encoded), so the session has an active entry ready to
// send. Returns the session ID and the seeded entry's ID.
func (s *ReplaySDK) CreateSessionWithRaw(
	ctx context.Context, conn ReplayConnection, rawBase64 string,
) (sessionID, activeEntryID string, err error) {
	return s.CreateSession(ctx, &gen.CreateReplaySessionInput{
		RequestSource: NewRawRequestSource(conn, rawBase64),
	})
}

// CreateCollection creates a new replay session collection.
func (s *ReplaySDK) CreateCollection(
	ctx context.Context, input *gen.CreateReplaySessionCollectionInput,
) (*gen.CreateReplaySessionCollectionResponse, error) {
	return gen.CreateReplaySessionCollection(ctx, s.client.GraphQL, *input)
}

// RenameSession renames a replay session.
func (s *ReplaySDK) RenameSession(
	ctx context.Context, id, name string,
) (*gen.RenameReplaySessionResponse, error) {
	return gen.RenameReplaySession(ctx, s.client.GraphQL, id, name)
}

// RenameCollection renames a replay session collection.
func (s *ReplaySDK) RenameCollection(
	ctx context.Context, id, name string,
) (*gen.RenameReplaySessionCollectionResponse, error) {
	return gen.RenameReplaySessionCollection(
		ctx, s.client.GraphQL, id, name,
	)
}

// DeleteSessions deletes replay sessions by IDs.
func (s *ReplaySDK) DeleteSessions(
	ctx context.Context, ids []string,
) (*gen.DeleteReplaySessionsResponse, error) {
	return gen.DeleteReplaySessions(ctx, s.client.GraphQL, ids)
}

// DeleteCollection deletes a replay session collection.
func (s *ReplaySDK) DeleteCollection(
	ctx context.Context, id string,
) (*gen.DeleteReplaySessionCollectionResponse, error) {
	return gen.DeleteReplaySessionCollection(ctx, s.client.GraphQL, id)
}

// MoveSession moves a session to a different collection.
func (s *ReplaySDK) MoveSession(
	ctx context.Context, id, collectionID string,
) (*gen.MoveReplaySessionResponse, error) {
	return gen.MoveReplaySession(ctx, s.client.GraphQL, id, collectionID)
}

// UpdateEntryDraft sets the draft (connection + raw request) on an
// existing replay entry. The next StartTask on the entry's session sends
// this draft. rawBase64 is base64-encoded and used for both raw and
// editorState.
func (s *ReplaySDK) UpdateEntryDraft(
	ctx context.Context,
	entryID string,
	conn ReplayConnection,
	rawBase64 string,
	placeholders []gen.ReplayPlaceholderInput,
) error {
	if placeholders == nil {
		placeholders = []gen.ReplayPlaceholderInput{}
	}
	var sni *string
	input := gen.UpdateReplayEntryDraftInput{
		Http: &gen.UpdateReplayEntryHttpDraftInput{
			Connection: gen.ConnectionInfoInput{
				Host:  conn.Host,
				Port:  conn.Port,
				IsTLS: conn.IsTLS,
				SNI:   sni,
			},
			EditorState: rawBase64,
			Raw:         rawBase64,
			Settings: gen.ReplayEntryHttpSettingsInput{
				Placeholders: placeholders,
			},
		},
	}
	_, err := gen.UpdateReplayEntryDraft(
		ctx, s.client.GraphQL, entryID, input,
	)
	return err
}

// UpdateSessionSettings updates the HTTP settings of a replay session.
func (s *ReplaySDK) UpdateSessionSettings(
	ctx context.Context,
	sessionID string,
	connectionClose, updateContentLength bool,
) error {
	input := gen.ReplaySessionSettingsInput{
		Http: &gen.ReplaySessionHttpSettingsInput{
			ConnectionClose:     connectionClose,
			UpdateContentLength: updateContentLength,
		},
	}
	_, err := gen.UpdateReplaySessionSettings(
		ctx, s.client.GraphQL, sessionID, input,
	)
	return err
}

// StartTask starts a replay task on the session, sending the active
// entry's current draft. Returns the StartReplayTask response so callers
// can inspect task/error (e.g. TaskInProgressUserError).
func (s *ReplaySDK) StartTask(
	ctx context.Context, sessionID string,
) (*gen.StartReplayTaskResponse, error) {
	return gen.StartReplayTask(ctx, s.client.GraphQL, sessionID)
}

// IsTaskInProgress reports whether a StartReplayTask response carried a
// TaskInProgressUserError (the session is busy with another task).
func IsTaskInProgress(resp *gen.StartReplayTaskResponse) bool {
	if resp == nil {
		return false
	}
	errPtr := resp.StartReplayTask.GetError()
	if errPtr == nil {
		return false
	}
	_, ok := (*errPtr).(*gen.StartReplayTaskStartReplayTaskStartReplayTaskPayloadErrorTaskInProgressUserError)
	return ok
}
