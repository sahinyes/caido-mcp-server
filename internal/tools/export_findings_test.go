package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// callExport drives the tool handler the way the MCP server does.
func callExport(t *testing.T, in ExportFindingsInput, n int, reporter string,
	tune func(*fakeFindings)) (*fakeFindings, ExportFindingsOutput) {
	t.Helper()
	f, client := newFakeFindings(t, n, reporter)
	if tune != nil {
		tune(f)
	}
	_, out, err := exportFindingsHandler(client)(
		context.Background(), nil, in,
	)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	return f, out
}

func decodeExport(t *testing.T, content string) []exportedFinding {
	t.Helper()
	var got []exportedFinding
	if err := json.Unmarshal([]byte(content), &got); err != nil {
		t.Fatalf("decode %q: %v", content, err)
	}
	return got
}

// The defect this guards: the first version read ONE page of 100 and filtered
// client-side, so anything on page 2 came back as an empty, well-formed export.
func TestExportFindings_FollowsPaginationPastTheFirstPage(t *testing.T) {
	f, out := callExport(t,
		ExportFindingsInput{Reporter: "me", Format: "json"}, 250, "me", nil)

	got := decodeExport(t, out.Content)
	if len(got) != 250 {
		t.Fatalf("got %d findings, want all 250", len(got))
	}
	if got[0].ID != "1" || got[249].ID != "250" {
		t.Fatalf("walk lost its order: first=%q last=%q",
			got[0].ID, got[249].ID)
	}
	if out.Warning != "" {
		t.Fatalf("complete walk should warn about nothing, got %q", out.Warning)
	}
	calls, reporters, afters := f.stats()
	if calls != 3 {
		t.Fatalf("got %d pages, want 3 for 250 findings at 100/page", calls)
	}
	// The cursor has to ADVANCE; a walk that re-sends "" would also return 250
	// rows by reading page 1 three times.
	if afters[0] != "" || afters[1] != "100" || afters[2] != "200" {
		t.Fatalf("cursor did not advance: %q", afters)
	}
	for i, r := range reporters {
		if r != "me" {
			t.Fatalf("page %d lost the reporter filter: %q", i, r)
		}
	}
	// One assertion on the millisecond->RFC3339 conversion, which nothing else
	// covers and which an agent reads as the finding's age.
	if want := time.UnixMilli(1759000000000).Format(time.RFC3339); got[0].CreatedAt != want {
		t.Fatalf("createdAt = %q, want %q", got[0].CreatedAt, want)
	}
}

// ids and reporter used to be AND-ed, which made one tool answer two questions
// and then report findings that exist as "not found".
func TestExportFindings_IdsWinOverReporter(t *testing.T) {
	f, out := callExport(t, ExportFindingsInput{
		IDs: []string{"3", "7"}, Reporter: "someone-else", Format: "json",
	}, 250, "me", nil)

	got := decodeExport(t, out.Content)
	if len(got) != 2 || got[0].ID != "3" || got[1].ID != "7" {
		t.Fatalf("got %+v, want exactly findings 3 and 7", got)
	}
	if out.Warning != "" {
		t.Fatalf("both ids were found, got warning %q", out.Warning)
	}
	_, reporters, _ := f.stats()
	for i, r := range reporters {
		if r != "" {
			t.Fatalf("page %d sent reporter %q alongside ids", i, r)
		}
	}
}

// Every requested id in hand ends the walk; otherwise an ids export reads the
// whole findings list to answer a two-id question.
func TestExportFindings_StopsAsSoonAsEveryIdIsFound(t *testing.T) {
	f, out := callExport(t,
		ExportFindingsInput{IDs: []string{"5"}, Format: "json"}, 250, "me", nil)

	if got := decodeExport(t, out.Content); len(got) != 1 {
		t.Fatalf("got %d findings, want 1", len(got))
	}
	if calls, _, _ := f.stats(); calls != 1 {
		t.Fatalf("read %d pages for an id on page 1, want 1", calls)
	}
}

// hasNextPage=true with an unchanged cursor: without the guard the walk appends
// the same page exportMaxPages times and reports the page limit instead.
func TestExportFindings_StopsWhenTheCursorStandsStill(t *testing.T) {
	f, out := callExport(t,
		ExportFindingsInput{Reporter: "me", Format: "json"}, 60, "me",
		func(f *fakeFindings) { f.pageSize = 1; f.stallCursor = true })

	got := decodeExport(t, out.Content)
	if len(got) != 2 {
		t.Fatalf("got %d findings, want 2 (one page, then the repeat is "+
			"detected before a third read)", len(got))
	}
	if calls, _, _ := f.stats(); calls != 2 {
		t.Fatalf("read %d pages, want 2", calls)
	}
	if !strings.Contains(out.Warning, "same cursor twice") {
		t.Fatalf("warning %q does not name the stall", out.Warning)
	}
	if strings.Contains(out.Warning, "pages of") {
		t.Fatalf("a stall must not be reported as the page limit: %q",
			out.Warning)
	}
}

// The volume cap is a real stop and has to be reported as one.
func TestExportFindings_ReportsTheVolumeCap(t *testing.T) {
	_, out := callExport(t,
		ExportFindingsInput{Reporter: "me", Format: "json"}, 600, "me", nil)

	if got := decodeExport(t, out.Content); len(got) != exportMaxFindings {
		t.Fatalf("got %d findings, want the cap %d", len(got), exportMaxFindings)
	}
	if !strings.Contains(out.Warning, "more exist") {
		t.Fatalf("warning %q does not say more findings exist", out.Warning)
	}
}

// The mirror of the test above, and the reason the hasNextPage check sits
// BEFORE the cap check: a result set that ends at exactly the cap is complete,
// and claiming "more exist" there contradicts what the server just said.
func TestExportFindings_DoesNotCallAnExactlyFullResultCapped(t *testing.T) {
	_, out := callExport(t,
		ExportFindingsInput{Reporter: "me", Format: "json"},
		exportMaxFindings, "me", nil)

	if got := decodeExport(t, out.Content); len(got) != exportMaxFindings {
		t.Fatalf("got %d findings, want %d", len(got), exportMaxFindings)
	}
	if out.Warning != "" {
		t.Fatalf("a complete result that happens to fill the cap must not "+
			"warn, got %q", out.Warning)
	}
}

// A repeated id must not make the numerator exceed the total ("2 of 1").
func TestExportFindings_DeduplicatesMissingIds(t *testing.T) {
	_, out := callExport(t, ExportFindingsInput{
		IDs: []string{"1", "999", "999"}, Format: "json",
	}, 10, "me", nil)

	if !strings.Contains(out.Warning, "1 of 2 requested findings") {
		t.Fatalf("warning %q is not counted against the deduplicated id set",
			out.Warning)
	}
	if n := strings.Count(out.Warning, "999"); n != 1 {
		t.Fatalf("id 999 listed %d times in %q", n, out.Warning)
	}
	// The walk ended on its own, so "not found" is a claim it is entitled to.
	if !strings.Contains(out.Warning, "were not found:") {
		t.Fatalf("a complete walk should make the unqualified claim: %q",
			out.Warning)
	}
}

// json.MarshalIndent encodes a nil slice as the four characters "null", which
// is not an empty export.
func TestExportFindings_EmptyResultIsAnEmptyJSONList(t *testing.T) {
	_, out := callExport(t,
		ExportFindingsInput{IDs: []string{"999"}, Format: "json"}, 10, "me", nil)

	if out.Content != "[]" {
		t.Fatalf("content = %q, want %q", out.Content, "[]")
	}
}

// The page limit is reported, AND it downgrades "were not found" to a claim
// about the pages that were actually read.
func TestExportFindings_ReportsThePageLimitAndQualifiesNotFound(t *testing.T) {
	f, out := callExport(t,
		ExportFindingsInput{IDs: []string{"999"}, Format: "json"}, 60, "me",
		func(f *fakeFindings) { f.pageSize = 1 })

	if calls, _, _ := f.stats(); calls != exportMaxPages {
		t.Fatalf("read %d pages, want the limit %d", calls, exportMaxPages)
	}
	if !strings.Contains(out.Warning, "stopped after at most") {
		t.Fatalf("warning %q does not report the page limit", out.Warning)
	}
	if !strings.Contains(out.Warning, "not found in the pages that were read") {
		t.Fatalf("a truncated walk must not claim the finding does not "+
			"exist: %q", out.Warning)
	}
}

// The non-json formats share the same walk; one test keeps them honest about
// the row count and about carrying the warning out.
func TestExportFindings_CsvAndMarkdownCarryEveryRowAndTheWarning(t *testing.T) {
	_, csv := callExport(t,
		ExportFindingsInput{Reporter: "me", Format: "csv"}, 250, "me", nil)
	rows := strings.Count(strings.TrimSuffix(csv.Content, "\n"), "\n")
	if rows != 250 {
		t.Fatalf("csv has %d data rows, want 250", rows)
	}

	_, md := callExport(t, ExportFindingsInput{
		IDs: []string{"1", "999"}, Format: "markdown",
	}, 10, "me", nil)
	if n := strings.Count(md.Content, "\n## "); n != 1 {
		t.Fatalf("markdown has %d finding sections, want 1", n)
	}
	if !strings.Contains(md.Warning, "999") {
		t.Fatalf("markdown dropped the warning: %q", md.Warning)
	}
}
