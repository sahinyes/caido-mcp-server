package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	caido "github.com/caido-community/sdk-go"
)

// A GraphQL fake that serves ListFindings and nothing else.
//
// export_findings' inline path walks a cursor until it has what it was asked
// for, and every bound it carries (page limit, volume cap, cursor stall) and
// every claim it makes afterwards ("not found", "more exist") is a property of
// that walk. None of it was reachable from a test before 2026-10-02: the tools
// package had no server fake at all, so the whole paginating path was covered
// only by a live smoke run against whatever findings happened to exist.
type fakeFindings struct {
	mu sync.Mutex

	// findings is the whole corpus, oldest first; the walk pages through it.
	findings []fakeFinding
	// pageSize caps what one response returns regardless of `first`, standing
	// in for a server with its own idea of a page.
	pageSize int
	// stallCursor answers hasNextPage=true with the cursor the caller just
	// sent, which is the one shape that turns a bounded walk into a loop that
	// appends the same page until the page limit stops it.
	stallCursor bool
	// lieAboutNextPage answers hasNextPage=true on the last page, so "more
	// exist" can be told apart from "the walk ended".
	lieAboutNextPage bool

	// what the handler saw, for asserting the request rather than the answer
	calls     int
	reporters []string // one entry per call: the reporter filter, "" if none
	afters    []string
}

type fakeFinding struct {
	id       string
	title    string
	host     string
	path     string
	reporter string
}

func newFakeFindings(t *testing.T, n int, reporter string) (*fakeFindings, *caido.Client) {
	t.Helper()
	f := &fakeFindings{pageSize: 100}
	for i := 1; i <= n; i++ {
		f.findings = append(f.findings, fakeFinding{
			id:       strconv.Itoa(i),
			title:    fmt.Sprintf("finding-%d", i),
			host:     "example.test",
			path:     fmt.Sprintf("/p/%d", i),
			reporter: reporter,
		})
	}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)

	client, err := caido.NewClient(caido.Options{URL: srv.URL})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	client.SetAccessToken("test-token")
	return f, client
}

func (f *fakeFindings) stats() (calls int, reporters, afters []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, append([]string(nil), f.reporters...),
		append([]string(nil), f.afters...)
}

func (f *fakeFindings) serve(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OpName string          `json:"operationName"`
		Vars   json.RawMessage `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if body.OpName != "ListFindings" {
		writeGQL(w, nil, fmt.Errorf("fakeFindings: unexpected operation %q",
			body.OpName))
		return
	}
	var v struct {
		First  *int    `json:"first"`
		After  *string `json:"after"`
		Filter *struct {
			Reporter *string `json:"reporter"`
		} `json:"filter"`
	}
	if err := json.Unmarshal(body.Vars, &v); err != nil {
		writeGQL(w, nil, err)
		return
	}

	f.mu.Lock()
	f.calls++
	rep := ""
	if v.Filter != nil && v.Filter.Reporter != nil {
		rep = *v.Filter.Reporter
	}
	f.reporters = append(f.reporters, rep)
	after := ""
	if v.After != nil {
		after = *v.After
	}
	f.afters = append(f.afters, after)
	corpus := f.findings
	size := f.pageSize
	stall := f.stallCursor
	lie := f.lieAboutNextPage
	f.mu.Unlock()

	// The cursor is the index of the last item already delivered.
	start := 0
	if after != "" {
		n, err := strconv.Atoi(after)
		if err != nil {
			writeGQL(w, nil, fmt.Errorf("fakeFindings: bad cursor %q", after))
			return
		}
		start = n
	}
	if v.First != nil && *v.First < size {
		size = *v.First
	}
	end := start + size
	if end > len(corpus) {
		end = len(corpus)
	}

	edges := []any{}
	for i := start; i < end; i++ {
		fi := corpus[i]
		edges = append(edges, map[string]any{
			"cursor": strconv.Itoa(i + 1),
			"node": map[string]any{
				"id": fi.id, "title": fi.title, "description": "d",
				"host": fi.host, "path": fi.path, "reporter": fi.reporter,
				"hidden": false, "dedupeKey": nil,
				"createdAt": 1759000000000,
				"request":   map[string]any{"id": "r" + fi.id},
			},
		})
	}
	endCursor := strconv.Itoa(end)
	if stall && after != "" {
		endCursor = after // standing still
	}
	hasNext := end < len(corpus) || lie
	if stall {
		hasNext = true
	}
	writeGQL(w, map[string]any{"findings": map[string]any{
		"edges": edges,
		"pageInfo": map[string]any{
			"hasNextPage": hasNext, "hasPreviousPage": start > 0,
			"startCursor": strconv.Itoa(start + 1), "endCursor": endCursor,
		},
		"count": map[string]any{"value": len(corpus)},
	}}, nil)
}

func writeGQL(w http.ResponseWriter, data any, err error) {
	w.Header().Set("Content-Type", "application/json")
	out := map[string]any{}
	if err != nil {
		out["errors"] = []any{map[string]any{"message": err.Error()}}
	} else {
		out["data"] = data
	}
	_ = json.NewEncoder(w).Encode(out)
}
