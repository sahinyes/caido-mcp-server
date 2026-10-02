package tools

import (
	"reflect"
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

// --- ReplayRequestInput struct shape ---

func TestReplayRequestInput_HasHostPortTLS(t *testing.T) {
	typ := reflect.TypeOf(ReplayRequestInput{})
	required := []string{"Host", "Port", "TLS"}
	fieldSet := make(map[string]bool)
	for i := range typ.NumField() {
		fieldSet[typ.Field(i).Name] = true
	}
	for _, name := range required {
		if !fieldSet[name] {
			t.Errorf("ReplayRequestInput missing field %q", name)
		}
	}
}

func TestReplayRequestInput_HostPortTLS_JSONTags(t *testing.T) {
	typ := reflect.TypeOf(ReplayRequestInput{})
	want := map[string]string{
		"Host": "host,omitempty",
		"Port": "port,omitempty",
		"TLS":  "tls,omitempty",
	}
	for i := range typ.NumField() {
		f := typ.Field(i)
		if expected, ok := want[f.Name]; ok {
			got := f.Tag.Get("json")
			if got != expected {
				t.Errorf("field %s json tag: want %q, got %q", f.Name, expected, got)
			}
		}
	}
}

// --- applyModifications does NOT touch connection target ---

func TestApplyModifications_HostFieldIgnored(t *testing.T) {
	// Host/Port/TLS override applies to the TCP connection, not the raw bytes.
	// applyModifications must not alter the Host header in raw.
	raw := "GET / HTTP/1.1\r\nHost: original.example.com\r\n\r\n"
	parsed := parseTestRequest(raw)

	result := applyModifications(parsed, ReplayRequestInput{
		Host: "override.example.com",
	})

	if !strings.Contains(result, "Host: original.example.com") {
		t.Fatalf("applyModifications must preserve original Host header; got: %s", result)
	}
	if strings.Contains(result, "override.example.com") {
		t.Fatalf("applyModifications must not inject override host into raw; got: %s", result)
	}
}

func TestApplyModifications_SetHeadersOverridesHostInRaw(t *testing.T) {
	// Users who want to change the on-wire Host: header use setHeaders
	raw := "GET / HTTP/1.1\r\nHost: original.example.com\r\n\r\n"
	parsed := parseTestRequest(raw)

	result := applyModifications(parsed, ReplayRequestInput{
		SetHeaders: map[string]string{"Host": "spoofed.example.com"},
	})

	if strings.Contains(result, "original.example.com") {
		t.Fatalf("setHeaders Host replacement failed; original still present: %s", result)
	}
	if !strings.Contains(result, "spoofed.example.com") {
		t.Fatalf("setHeaders Host replacement failed; spoofed value missing: %s", result)
	}
}

func TestApplyModifications_AllOperationsCombined(t *testing.T) {
	raw := "GET /old HTTP/1.1\r\nHost: example.com\r\nX-Old: remove\r\nKeep: yes\r\n\r\nbody"
	parsed := parseTestRequest(raw)
	newBody := "newbody"

	result := applyModifications(parsed, ReplayRequestInput{
		Method:        "post",
		Path:          "/new",
		RemoveHeaders: []string{"X-Old"},
		SetHeaders:    map[string]string{"X-New": "added"},
		Body:          &newBody,
	})

	checks := []struct {
		desc    string
		present bool
		needle  string
	}{
		{"POST method", true, "POST /new HTTP/1.1"},
		{"new header", true, "X-New: added"},
		{"kept header", true, "Keep: yes"},
		{"new body", true, "newbody"},
		{"removed header absent", false, "X-Old"},
		{"original body absent", false, "body\r\n"},
	}
	for _, c := range checks {
		has := strings.Contains(result, c.needle)
		if c.present && !has {
			t.Errorf("[%s] expected %q in result: %s", c.desc, c.needle, result)
		}
		if !c.present && has {
			t.Errorf("[%s] unexpected %q in result: %s", c.desc, c.needle, result)
		}
	}
}

// --- benchmarks ---

func BenchmarkApplyModifications_NoChanges(b *testing.B) {
	raw := "GET /path HTTP/1.1\r\nHost: example.com\r\nAccept: */*\r\nContent-Length: 0\r\n\r\n"
	parsed := httputil.ParseRaw([]byte(raw), true, true, 0, 0)
	input := ReplayRequestInput{}
	b.ResetTimer()
	for b.Loop() {
		applyModifications(parsed, input)
	}
}

func BenchmarkApplyModifications_WithOverrides(b *testing.B) {
	raw := "GET /path HTTP/1.1\r\nHost: example.com\r\nAuthorization: Bearer old\r\nX-Remove: yes\r\n\r\nbody"
	parsed := httputil.ParseRaw([]byte(raw), true, true, 0, 0)
	newBody := `{"data":"new"}`
	input := ReplayRequestInput{
		Method:        "post",
		Path:          "/new/path",
		SetHeaders:    map[string]string{"Authorization": "Bearer new", "X-Added": "yes"},
		RemoveHeaders: []string{"X-Remove"},
		Body:          &newBody,
	}
	b.ResetTimer()
	for b.Loop() {
		applyModifications(parsed, input)
	}
}

// --- setHeaders is "add or replace", so repeats must collapse ---

func TestApplyModifications_OverrideCollapsesRepeatedHeader(t *testing.T) {
	raw := "GET / HTTP/1.1\r\nHost: example.com\r\n" +
		"Cookie: a=1\r\nCookie: b=2\r\n\r\n"
	parsed := parseTestRequest(raw)

	result := applyModifications(parsed, ReplayRequestInput{
		SetHeaders: map[string]string{"Cookie": "session=new"},
	})

	if got := strings.Count(result, "Cookie: session=new"); got != 1 {
		t.Fatalf("override written %d times, want 1:\n%s", got, result)
	}
	if strings.Contains(result, "a=1") || strings.Contains(result, "b=2") {
		t.Fatalf("original values survived the replace:\n%s", result)
	}
}

func TestApplyModifications_RepeatedHeaderWithoutOverrideIsPreserved(t *testing.T) {
	// The collapse applies to the OVERRIDE only: a request that legitimately
	// carries a header twice and is not overriding it must keep both.
	raw := "GET / HTTP/1.1\r\nHost: example.com\r\n" +
		"Set-Cookie: a=1\r\nSet-Cookie: b=2\r\n\r\n"
	parsed := parseTestRequest(raw)

	result := applyModifications(parsed, ReplayRequestInput{
		SetHeaders: map[string]string{"X-Probe": "1"},
	})

	if !strings.Contains(result, "Set-Cookie: a=1") ||
		!strings.Contains(result, "Set-Cookie: b=2") {
		t.Fatalf("a repeated header was collapsed:\n%s", result)
	}
}

// --- wantsRewrite decides verbatim-vs-rebuild ---

func TestWantsRewrite_ConnectionOverridesAreNotRewrites(t *testing.T) {
	tls := true
	in := ReplayRequestInput{
		ID: "1", Host: "other.example", Port: 8443, TLS: &tls,
		BodyLimit: 10, BodyOffset: 5, SessionID: "7",
	}
	if wantsRewrite(in) {
		t.Fatal("retargeting the same bytes must not rebuild the request")
	}
}

func TestWantsRewrite_EachByteChangingOverride(t *testing.T) {
	empty := ""
	cases := map[string]ReplayRequestInput{
		"method":        {Method: "POST"},
		"path":          {Path: "/x"},
		"body":          {Body: &empty},
		"setHeaders":    {SetHeaders: map[string]string{"A": "b"}},
		"removeHeaders": {RemoveHeaders: []string{"A"}},
	}
	for name, in := range cases {
		if !wantsRewrite(in) {
			t.Errorf("%s changes the bytes and must rebuild", name)
		}
	}
}
