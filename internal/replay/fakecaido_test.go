package replay

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	caido "github.com/caido-community/sdk-go"
)

// A fake Caido GraphQL endpoint, enough of one to drive the whole replay
// send sequence offline.
//
// It exists because the central claim of the 2026-10-01 fix - that the
// draft-then-start-then-poll sequence is atomic per session - had only a live,
// opt-in test. A live test needs a real Caido, writes into a real project, and
// cannot FORCE the interleaving it is looking for: it fires N sends and hopes
// they overlap. This fake can park a send exactly between its draft and its
// start and then ask whether anybody else got in, which turns "we did not
// observe the race today" into "the race cannot happen".
//
// It models what was MEASURED of 0.58.3, not what the schema allows:
// startReplayTask executes the session's active entry and APPENDS a new one
// carrying the request and its response, and the active pointer follows.
type fakeCaido struct {
	mu       sync.Mutex
	project  string
	sessions map[string]*fakeSession
	entries  map[string]*fakeEntry
	nextS    int
	nextE    int
	nextR    int

	// draftOrder records every draft write as "entryID=marker", in order. It is
	// how a test sees one send overwrite another's bytes.
	draftOrder []string
	// ops records operation names in order.
	ops []string
	// created counts createReplaySession calls, i.e. rotations and pool fills.
	created int

	// Knobs. Each is consulted under mu and may be nil.
	//
	// beforeStartTask runs OUTSIDE mu, immediately before a task starts, so a
	// test can park one send there and let another try to interleave.
	beforeStartTask func(sessionID string)
	// startTaskErr returns ("payload variant", transportError) for a session.
	startTaskErr func(sessionID string) (string, bool)
	// startTaskNoTask makes the payload carry neither a task nor an error.
	startTaskNoTask bool
	// noResponse leaves the appended entry without a response, so the poll
	// never completes.
	noResponse bool
	// failReads fails the next N session/entry reads, counting down.
	failReads int
	// failCreate makes createReplaySession fail.
	failCreate bool
	// moveActiveTo redirects a session's active pointer to an EXISTING entry
	// before the next read, standing in for another writer (the Caido UI, a
	// second client) that this process cannot lock.
	moveActiveTo map[string]string
}

type fakeSession struct {
	id        string
	name      string
	activeID  string
	entryIDs  []string
	connClose bool
	updateCL  bool
	running   bool
}

type fakeEntry struct {
	id      string
	raw     string // base64
	host    string
	port    int
	tls     bool
	respID  string
	hasResp bool
	errText string
	sentRaw string // base64 of what went on the wire
}

func newFakeCaido(t *testing.T) (*fakeCaido, *caido.Client) {
	t.Helper()
	f := &fakeCaido{
		project:  "project-1",
		sessions: map[string]*fakeSession{},
		entries:  map[string]*fakeEntry{},
	}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)

	client, err := caido.NewClient(caido.Options{URL: srv.URL})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	client.SetAccessToken("test-token")

	// The shared session cache is process-global; every test starts clean.
	ResetDefaultSession("")
	sessionMu.Lock()
	defaultSessionProject = ""
	sessionMu.Unlock()
	t.Cleanup(func() {
		ResetDefaultSession("")
		sessionMu.Lock()
		defaultSessionProject = ""
		sessionMu.Unlock()
	})
	return f, client
}

// marker extracts the X-Caido-MCP-Probe value from a base64 request, for
// readable assertions.
func marker(rawB64 string) string {
	b, err := base64.StdEncoding.DecodeString(rawB64)
	if err != nil {
		return "<undecodable>"
	}
	for _, line := range strings.Split(string(b), "\r\n") {
		if strings.HasPrefix(strings.ToLower(line), "x-caido-mcp-probe:") {
			return strings.TrimSpace(line[len("x-caido-mcp-probe:"):])
		}
	}
	return "<unmarked>"
}

func probeBytes(m string) string {
	return base64.StdEncoding.EncodeToString([]byte(
		"GET /health HTTP/1.1\r\nHost: example.test\r\n" +
			"X-Caido-MCP-Probe: " + m + "\r\n\r\n",
	))
}

func (f *fakeCaido) snapshotDrafts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.draftOrder))
	copy(out, f.draftOrder)
	return out
}

func (f *fakeCaido) createdCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.created
}

func (f *fakeCaido) setProject(p string) {
	f.mu.Lock()
	f.project = p
	f.mu.Unlock()
}

func (f *fakeCaido) serve(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OpName string          `json:"operationName"`
		Vars   json.RawMessage `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	f.mu.Lock()
	f.ops = append(f.ops, body.OpName)
	f.mu.Unlock()

	var data any
	var err error
	switch body.OpName {
	case "GetCurrentProject":
		data, err = f.currentProject()
	case "CreateReplaySession":
		data, err = f.createSession(body.Vars)
	case "GetReplaySession":
		data, err = f.getSession(body.Vars)
	case "GetReplayEntry":
		data, err = f.getEntry(body.Vars)
	case "UpdateReplayEntryDraft":
		data, err = f.updateDraft(body.Vars)
	case "UpdateReplaySessionSettings":
		data, err = f.updateSettings(body.Vars)
	case "RenameReplaySession":
		data, err = f.renameSession(body.Vars)
	case "StartReplayTask":
		data, err = f.startTask(body.Vars)
	case "DeleteReplaySessions":
		data, err = f.deleteSessions(body.Vars)
	default:
		err = fmt.Errorf("fakeCaido: unhandled operation %q", body.OpName)
	}

	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"errors": []map[string]any{{"message": err.Error()}},
		})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func (f *fakeCaido) currentProject() (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.project == "" {
		return map[string]any{"currentProject": nil}, nil
	}
	return map[string]any{"currentProject": map[string]any{
		"project": map[string]any{
			"id": f.project, "name": f.project, "path": "/tmp/" + f.project,
			"size": 0, "status": "READY", "temporary": false,
			"createdAt": "2026-10-02T00:00:00Z",
			"updatedAt": "2026-10-02T00:00:00Z",
			"version":   "0.58.3",
		},
		"readOnly": false,
	}}, nil
}

func (f *fakeCaido) entryNode(e *fakeEntry) map[string]any {
	return map[string]any{
		"__typename": "ReplayEntryHttp",
		"id":         e.id,
		"raw":        e.raw,
		"connection": map[string]any{
			"host": e.host, "port": e.port, "isTLS": e.tls,
		},
	}
}

func (f *fakeCaido) createSession(vars json.RawMessage) (any, error) {
	var v struct {
		Input struct {
			Kind          string `json:"kind"`
			RequestSource *struct {
				Raw *struct {
					Raw            string `json:"raw"`
					ConnectionInfo struct {
						Host  string `json:"host"`
						Port  int    `json:"port"`
						IsTLS bool   `json:"isTLS"`
					} `json:"connectionInfo"`
				} `json:"raw"`
			} `json:"requestSource"`
		} `json:"input"`
	}
	if err := json.Unmarshal(vars, &v); err != nil {
		return nil, err
	}
	if v.Input.Kind != "HTTP" {
		return nil, fmt.Errorf(
			`field "kind" of type "ReplaySessionKind!" is required but not provided`,
		)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.created++
	if f.failCreate {
		return nil, fmt.Errorf("fakeCaido: create refused")
	}
	f.nextS++
	f.nextE++
	sid := fmt.Sprintf("%d", f.nextS)
	eid := fmt.Sprintf("%d", f.nextE)
	e := &fakeEntry{id: eid}
	if rs := v.Input.RequestSource; rs != nil && rs.Raw != nil {
		e.raw = rs.Raw.Raw
		e.host = rs.Raw.ConnectionInfo.Host
		e.port = rs.Raw.ConnectionInfo.Port
		e.tls = rs.Raw.ConnectionInfo.IsTLS
	}
	f.entries[eid] = e
	f.sessions[sid] = &fakeSession{
		id: sid, name: sid, activeID: eid, entryIDs: []string{eid},
		updateCL: false,
	}
	return map[string]any{"createReplaySession": map[string]any{
		"error": nil,
		"session": map[string]any{
			"__typename":  "ReplaySessionHttp",
			"id":          sid,
			"name":        sid,
			"activeEntry": map[string]any{"__typename": "ReplayEntryHttp", "id": eid},
		},
	}}, nil
}

func (f *fakeCaido) getSession(vars json.RawMessage) (any, error) {
	var v struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(vars, &v); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failReads > 0 {
		f.failReads--
		return nil, fmt.Errorf("fakeCaido: transient read failure")
	}
	s := f.sessions[v.ID]
	if s == nil {
		return map[string]any{"replaySession": nil}, nil
	}
	if to, ok := f.moveActiveTo[s.id]; ok {
		s.activeID = to
		delete(f.moveActiveTo, s.id)
	}
	edges := []any{}
	for _, eid := range s.entryIDs {
		if e := f.entries[eid]; e != nil {
			edges = append(edges, map[string]any{"node": f.entryNode(e)})
		}
	}
	return map[string]any{"replaySession": map[string]any{
		"__typename":  "ReplaySessionHttp",
		"id":          s.id,
		"name":        s.name,
		"activeEntry": map[string]any{"__typename": "ReplayEntryHttp", "id": s.activeID},
		"collection":  map[string]any{"id": "c1", "name": "default"},
		"settings": map[string]any{
			"connectionClose": s.connClose, "updateContentLength": s.updateCL,
		},
		"entries": map[string]any{
			"edges":    edges,
			"pageInfo": map[string]any{"hasNextPage": false, "endCursor": nil},
		},
	}}, nil
}

func (f *fakeCaido) getEntry(vars json.RawMessage) (any, error) {
	var v struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(vars, &v); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failReads > 0 {
		f.failReads--
		return nil, fmt.Errorf("fakeCaido: transient read failure")
	}
	e := f.entries[v.ID]
	if e == nil {
		return map[string]any{"replayEntry": nil}, nil
	}
	node := map[string]any{
		"__typename": "ReplayEntryHttp",
		"id":         e.id,
		"error":      nil,
		"createdAt":  0,
		"raw":        e.raw,
		"connection": map[string]any{
			"host": e.host, "port": e.port, "isTLS": e.tls,
		},
		"settings": map[string]any{"placeholders": []any{}},
		"request":  nil,
	}
	if e.errText != "" {
		node["error"] = e.errText
	}
	if e.hasResp {
		node["request"] = map[string]any{
			"id": e.id, "method": "GET", "host": e.host, "port": e.port,
			"path": "/health", "query": "", "isTls": e.tls,
			"raw": e.sentRaw, "createdAt": 0,
			"response": map[string]any{
				"id": e.respID, "statusCode": 200,
				"raw":           base64.StdEncoding.EncodeToString([]byte("HTTP/1.1 200 OK\r\n\r\n")),
				"roundtripTime": 1, "length": 19,
			},
		}
	}
	return map[string]any{"replayEntry": node}, nil
}

func (f *fakeCaido) updateDraft(vars json.RawMessage) (any, error) {
	var v struct {
		ID    string `json:"id"`
		Input struct {
			HTTP *struct {
				Raw        *string `json:"raw"`
				Connection *struct {
					Host  string `json:"host"`
					Port  int    `json:"port"`
					IsTLS bool   `json:"isTLS"`
				} `json:"connection"`
			} `json:"http"`
		} `json:"input"`
	}
	if err := json.Unmarshal(vars, &v); err != nil {
		return nil, err
	}
	if v.Input.HTTP == nil {
		return nil, fmt.Errorf("fakeCaido: draft input carried no http variant")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	e := f.entries[v.ID]
	if e == nil {
		return nil, fmt.Errorf("fakeCaido: unknown entry %q", v.ID)
	}
	if v.Input.HTTP.Raw != nil {
		e.raw = *v.Input.HTTP.Raw
	}
	if c := v.Input.HTTP.Connection; c != nil {
		e.host, e.port, e.tls = c.Host, c.Port, c.IsTLS
	}
	f.draftOrder = append(f.draftOrder, e.id+"="+marker(e.raw))
	return map[string]any{"updateReplayEntryDraft": map[string]any{
		"entry": map[string]any{"__typename": "ReplayEntryHttp", "id": e.id},
	}}, nil
}

func (f *fakeCaido) updateSettings(vars json.RawMessage) (any, error) {
	var v struct {
		ID    string `json:"id"`
		Input struct {
			ConnectionClose     *bool `json:"connectionClose"`
			UpdateContentLength *bool `json:"updateContentLength"`
		} `json:"input"`
	}
	if err := json.Unmarshal(vars, &v); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.sessions[v.ID]
	if s == nil {
		return nil, fmt.Errorf("fakeCaido: unknown session %q", v.ID)
	}
	if v.Input.ConnectionClose != nil {
		s.connClose = *v.Input.ConnectionClose
	}
	if v.Input.UpdateContentLength != nil {
		s.updateCL = *v.Input.UpdateContentLength
	}
	return map[string]any{"updateReplaySessionSettings": map[string]any{
		"session": map[string]any{"__typename": "ReplaySessionHttp", "id": s.id},
	}}, nil
}

func (f *fakeCaido) renameSession(vars json.RawMessage) (any, error) {
	var v struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(vars, &v); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.sessions[v.ID]
	if s == nil {
		return nil, fmt.Errorf("fakeCaido: unknown session %q", v.ID)
	}
	s.name = v.Name
	return map[string]any{"renameReplaySession": map[string]any{
		"session": map[string]any{
			"__typename": "ReplaySessionHttp", "id": s.id, "name": s.name,
		},
	}}, nil
}

func (f *fakeCaido) startTask(vars json.RawMessage) (any, error) {
	var v struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(vars, &v); err != nil {
		return nil, err
	}

	f.mu.Lock()
	hook := f.beforeStartTask
	errHook := f.startTaskErr
	f.mu.Unlock()
	if hook != nil {
		hook(v.SessionID)
	}

	if errHook != nil {
		if variant, transport := errHook(v.SessionID); variant != "" || transport {
			if transport {
				// A transport failure AFTER the mutation may have been applied:
				// the server answers nothing at all.
				return nil, fmt.Errorf("fakeCaido: connection reset by peer")
			}
			return map[string]any{"startReplayTask": map[string]any{
				"task":  nil,
				"error": map[string]any{"__typename": variant},
			}}, nil
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startTaskNoTask {
		return map[string]any{"startReplayTask": map[string]any{
			"task": nil, "error": nil,
		}}, nil
	}
	s := f.sessions[v.SessionID]
	if s == nil {
		return map[string]any{"startReplayTask": map[string]any{
			"task":  nil,
			"error": map[string]any{"__typename": "UnknownIdUserError"},
		}}, nil
	}
	active := f.entries[s.activeID]
	if active == nil {
		return nil, fmt.Errorf("fakeCaido: session has no active entry")
	}

	// What 0.58.3 was measured to do: execute the active entry and APPEND a new
	// one carrying the request and its response; the active pointer follows.
	f.nextE++
	f.nextR++
	nid := fmt.Sprintf("%d", f.nextE)
	ne := &fakeEntry{
		id: nid, raw: active.raw, host: active.host, port: active.port,
		tls: active.tls, sentRaw: active.raw,
	}
	if !f.noResponse {
		ne.hasResp = true
		ne.respID = fmt.Sprintf("r%d", f.nextR)
	}
	f.entries[nid] = ne
	s.entryIDs = append(s.entryIDs, nid)
	s.activeID = nid
	return map[string]any{"startReplayTask": map[string]any{
		"task":  map[string]any{"id": fmt.Sprintf("t%d", f.nextR)},
		"error": nil,
	}}, nil
}

func (f *fakeCaido) deleteSessions(vars json.RawMessage) (any, error) {
	var v struct {
		IDs []string `json:"ids"`
	}
	if err := json.Unmarshal(vars, &v); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	deleted := []string{}
	for _, id := range v.IDs {
		if _, ok := f.sessions[id]; ok {
			delete(f.sessions, id)
			deleted = append(deleted, id)
		}
	}
	return map[string]any{"deleteReplaySessions": map[string]any{
		"deletedIds": deleted,
	}}, nil
}
