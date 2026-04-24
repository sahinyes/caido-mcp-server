package tools

import (
	"strings"
	"testing"

	"github.com/c0tton-fluff/caido-mcp-server/internal/httputil"
)

func parseTestRequest(raw string) *httputil.ParsedMessage {
	return httputil.ParseRaw([]byte(raw), true, true, 0, 0)
}

func TestApplyModifications_NoChanges(t *testing.T) {
	raw := "GET /path HTTP/1.1\r\nHost: example.com\r\nAccept: */*\r\n\r\n"
	parsed := parseTestRequest(raw)

	result := applyModifications(parsed, ReplayRequestInput{})

	if !strings.Contains(result, "GET /path HTTP/1.1") {
		t.Fatalf("missing original first line: %s", result)
	}
	if !strings.Contains(result, "Accept: */*") {
		t.Fatalf("missing original headers: %s", result)
	}
}

func TestApplyModifications_MethodOverride(t *testing.T) {
	raw := "GET /path HTTP/1.1\r\nHost: example.com\r\n\r\n"
	parsed := parseTestRequest(raw)

	result := applyModifications(parsed, ReplayRequestInput{Method: "post"})

	if !strings.HasPrefix(result, "POST /path HTTP/1.1") {
		t.Fatalf("expected POST method, got: %s", result)
	}
}

func TestApplyModifications_PathOverride(t *testing.T) {
	raw := "GET /original HTTP/1.1\r\nHost: example.com\r\n\r\n"
	parsed := parseTestRequest(raw)

	result := applyModifications(parsed, ReplayRequestInput{Path: "/new/path"})

	if !strings.HasPrefix(result, "GET /new/path HTTP/1.1") {
		t.Fatalf("expected new path, got: %s", result)
	}
}

func TestApplyModifications_SetHeader_Replace(t *testing.T) {
	raw := "GET / HTTP/1.1\r\nHost: example.com\r\nAuthorization: Bearer old\r\n\r\n"
	parsed := parseTestRequest(raw)

	body := ""
	result := applyModifications(parsed, ReplayRequestInput{
		SetHeaders: map[string]string{"Authorization": "Bearer new"},
		Body:       &body,
	})

	if !strings.Contains(result, "Authorization: Bearer new") {
		t.Fatalf("Authorization header not replaced: %s", result)
	}
	if strings.Contains(result, "Bearer old") {
		t.Fatalf("old Authorization value still present: %s", result)
	}
}

func TestApplyModifications_SetHeader_Add(t *testing.T) {
	raw := "GET / HTTP/1.1\r\nHost: example.com\r\n\r\n"
	parsed := parseTestRequest(raw)

	result := applyModifications(parsed, ReplayRequestInput{
		SetHeaders: map[string]string{"X-Custom": "value"},
	})

	if !strings.Contains(result, "X-Custom: value") {
		t.Fatalf("new header not added: %s", result)
	}
}

func TestApplyModifications_RemoveHeader(t *testing.T) {
	raw := "GET / HTTP/1.1\r\nHost: example.com\r\nX-Remove-Me: secret\r\n\r\n"
	parsed := parseTestRequest(raw)

	result := applyModifications(parsed, ReplayRequestInput{
		RemoveHeaders: []string{"X-Remove-Me"},
	})

	if strings.Contains(result, "X-Remove-Me") {
		t.Fatalf("removed header still present: %s", result)
	}
}

func TestApplyModifications_RemoveHeader_CaseInsensitive(t *testing.T) {
	raw := "GET / HTTP/1.1\r\nHost: example.com\r\nX-Token: abc\r\n\r\n"
	parsed := parseTestRequest(raw)

	result := applyModifications(parsed, ReplayRequestInput{
		RemoveHeaders: []string{"x-token"},
	})

	if strings.Contains(result, "X-Token") {
		t.Fatalf("case-insensitive remove failed, header still present: %s", result)
	}
}

func TestApplyModifications_BodyReplace(t *testing.T) {
	raw := "POST / HTTP/1.1\r\nHost: example.com\r\n\r\noriginal body"
	parsed := parseTestRequest(raw)

	newBody := `{"injected":true}`
	result := applyModifications(parsed, ReplayRequestInput{Body: &newBody})

	if !strings.HasSuffix(result, `{"injected":true}`) {
		t.Fatalf("body not replaced: %s", result)
	}
	if strings.Contains(result, "original body") {
		t.Fatalf("original body still present: %s", result)
	}
}

func TestApplyModifications_CRLFSeparator(t *testing.T) {
	raw := "GET / HTTP/1.1\r\nHost: example.com\r\n\r\n"
	parsed := parseTestRequest(raw)

	result := applyModifications(parsed, ReplayRequestInput{})

	if !strings.Contains(result, "\r\n\r\n") {
		t.Fatalf("missing CRLF header/body separator: %q", result)
	}
}
