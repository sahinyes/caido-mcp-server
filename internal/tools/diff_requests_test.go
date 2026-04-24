package tools

import (
	"testing"

	"github.com/c0tton-fluff/caido-mcp-server/internal/httputil"
)

func TestDiffHeaders_NoChanges(t *testing.T) {
	headers := []httputil.Header{
		{Name: "Content-Type", Value: "application/json"},
		{Name: "Accept", Value: "*/*"},
	}
	diffs := diffHeaders(headers, headers)
	if len(diffs) != 0 {
		t.Fatalf("expected no diffs, got %d: %+v", len(diffs), diffs)
	}
}

func TestDiffHeaders_AddedHeader(t *testing.T) {
	base := []httputil.Header{
		{Name: "Accept", Value: "*/*"},
	}
	comp := []httputil.Header{
		{Name: "Accept", Value: "*/*"},
		{Name: "Authorization", Value: "Bearer token"},
	}

	diffs := diffHeaders(base, comp)

	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d: %+v", len(diffs), diffs)
	}
	if diffs[0].Status != "added" {
		t.Fatalf("expected status 'added', got %q", diffs[0].Status)
	}
	if diffs[0].Name != "Authorization" {
		t.Fatalf("expected header 'Authorization', got %q", diffs[0].Name)
	}
	if diffs[0].CompVal != "Bearer token" {
		t.Fatalf("expected compValue 'Bearer token', got %q", diffs[0].CompVal)
	}
}

func TestDiffHeaders_RemovedHeader(t *testing.T) {
	base := []httputil.Header{
		{Name: "Accept", Value: "*/*"},
		{Name: "X-Custom", Value: "old"},
	}
	comp := []httputil.Header{
		{Name: "Accept", Value: "*/*"},
	}

	diffs := diffHeaders(base, comp)

	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d: %+v", len(diffs), diffs)
	}
	if diffs[0].Status != "removed" {
		t.Fatalf("expected status 'removed', got %q", diffs[0].Status)
	}
	if diffs[0].Name != "X-Custom" {
		t.Fatalf("expected header 'X-Custom', got %q", diffs[0].Name)
	}
	if diffs[0].BaseVal != "old" {
		t.Fatalf("expected baseValue 'old', got %q", diffs[0].BaseVal)
	}
}

func TestDiffHeaders_ChangedHeader(t *testing.T) {
	base := []httputil.Header{
		{Name: "Content-Type", Value: "text/plain"},
	}
	comp := []httputil.Header{
		{Name: "Content-Type", Value: "application/json"},
	}

	diffs := diffHeaders(base, comp)

	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d: %+v", len(diffs), diffs)
	}
	if diffs[0].Status != "changed" {
		t.Fatalf("expected status 'changed', got %q", diffs[0].Status)
	}
	if diffs[0].BaseVal != "text/plain" {
		t.Fatalf("expected baseValue 'text/plain', got %q", diffs[0].BaseVal)
	}
	if diffs[0].CompVal != "application/json" {
		t.Fatalf("expected compValue 'application/json', got %q", diffs[0].CompVal)
	}
}

func TestDiffHeaders_EmptySlices(t *testing.T) {
	diffs := diffHeaders(nil, nil)
	if diffs != nil {
		t.Fatalf("expected nil diffs for empty inputs, got %+v", diffs)
	}
}

func TestDiffHeaders_BaseEmptyCompHasHeaders(t *testing.T) {
	comp := []httputil.Header{
		{Name: "X-New", Value: "value"},
	}
	diffs := diffHeaders(nil, comp)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d", len(diffs))
	}
	if diffs[0].Status != "added" {
		t.Fatalf("expected 'added', got %q", diffs[0].Status)
	}
}

func TestDiffHeaders_MixedChanges(t *testing.T) {
	base := []httputil.Header{
		{Name: "Accept", Value: "*/*"},
		{Name: "X-Old", Value: "remove-me"},
		{Name: "Content-Type", Value: "text/plain"},
	}
	comp := []httputil.Header{
		{Name: "Accept", Value: "application/json"},
		{Name: "Content-Type", Value: "text/plain"},
		{Name: "X-New", Value: "added"},
	}

	diffs := diffHeaders(base, comp)

	statusMap := make(map[string]string)
	for _, d := range diffs {
		statusMap[d.Name] = d.Status
	}

	if statusMap["Accept"] != "changed" {
		t.Fatalf("Accept should be 'changed', got %q", statusMap["Accept"])
	}
	if statusMap["X-Old"] != "removed" {
		t.Fatalf("X-Old should be 'removed', got %q", statusMap["X-Old"])
	}
	if statusMap["X-New"] != "added" {
		t.Fatalf("X-New should be 'added', got %q", statusMap["X-New"])
	}
	if _, exists := statusMap["Content-Type"]; exists {
		t.Fatal("Content-Type should have no diff (same value)")
	}
}
