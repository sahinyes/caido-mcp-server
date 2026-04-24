package tools

import (
	"strings"
	"testing"

	"github.com/c0tton-fluff/caido-mcp-server/internal/httputil"
)

func TestShellQuote_PlainString(t *testing.T) {
	got := shellQuote("hello")
	if got != "'hello'" {
		t.Fatalf("got %q, want %q", got, "'hello'")
	}
}

func TestShellQuote_EmptyString(t *testing.T) {
	got := shellQuote("")
	if got != "''" {
		t.Fatalf("got %q, want %q", got, "''")
	}
}

func TestShellQuote_ContainsSingleQuote(t *testing.T) {
	got := shellQuote("it's here")
	want := "'it'\\''s here'"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestShellQuote_MultipleSingleQuotes(t *testing.T) {
	got := shellQuote("a'b'c")
	want := "'a'\\''b'\\''c'"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func makeRequest(firstLine string, headers []httputil.Header, body string) *httputil.ParsedMessage {
	return &httputil.ParsedMessage{
		FirstLine: firstLine,
		Headers:   headers,
		Body:      body,
	}
}

func TestBuildCurl_GET(t *testing.T) {
	parsed := makeRequest("GET /path HTTP/1.1", []httputil.Header{
		{Name: "Host", Value: "example.com"},
		{Name: "Accept", Value: "*/*"},
	}, "")

	got := buildCurl(parsed, "https")

	if strings.Contains(got, "-X") {
		t.Fatal("GET requests must not have -X flag")
	}
	if !strings.Contains(got, "'https://example.com/path'") {
		t.Fatalf("missing URL in output: %s", got)
	}
	if !strings.Contains(got, "-H 'Accept: */*'") {
		t.Fatalf("missing Accept header in output: %s", got)
	}
	if strings.Contains(got, "Host:") {
		t.Fatal("Host header must be skipped")
	}
}

func TestBuildCurl_POST_WithBody(t *testing.T) {
	parsed := makeRequest("POST /api HTTP/1.1", []httputil.Header{
		{Name: "Host", Value: "example.com"},
		{Name: "Content-Type", Value: "application/json"},
		{Name: "Content-Length", Value: "13"},
	}, `{"key":"val"}`)

	got := buildCurl(parsed, "https")

	if !strings.Contains(got, "-X 'POST'") {
		t.Fatalf("missing -X POST in output: %s", got)
	}
	if !strings.Contains(got, "-H 'Content-Type: application/json'") {
		t.Fatalf("missing Content-Type header: %s", got)
	}
	if strings.Contains(got, "Content-Length:") {
		t.Fatal("Content-Length header must be skipped")
	}
	if !strings.Contains(got, `-d '{"key":"val"}'`) {
		t.Fatalf("missing body in output: %s", got)
	}
}

func TestBuildCurl_HTTP_Scheme(t *testing.T) {
	parsed := makeRequest("GET / HTTP/1.1", []httputil.Header{
		{Name: "Host", Value: "example.com"},
	}, "")

	got := buildCurl(parsed, "http")

	if !strings.Contains(got, "http://example.com/") {
		t.Fatalf("expected http scheme, got: %s", got)
	}
}

func TestBuildCurl_SpecialCharsInHeader(t *testing.T) {
	parsed := makeRequest("GET / HTTP/1.1", []httputil.Header{
		{Name: "Host", Value: "example.com"},
		{Name: "Cookie", Value: "session=it's'here"},
	}, "")

	got := buildCurl(parsed, "https")

	// Single quotes in header values must be escaped (shellQuote produces '\'' for each ')
	if !strings.Contains(got, "'\\''") {
		t.Fatalf("single quotes in header not escaped: %s", got)
	}
}

func TestBuildCurl_NoBody(t *testing.T) {
	parsed := makeRequest("DELETE /resource/1 HTTP/1.1", []httputil.Header{
		{Name: "Host", Value: "example.com"},
	}, "")

	got := buildCurl(parsed, "https")

	if strings.Contains(got, "-d") {
		t.Fatalf("unexpected -d flag for empty body: %s", got)
	}
}
