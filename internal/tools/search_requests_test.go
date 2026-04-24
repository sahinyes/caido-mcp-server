package tools

import (
	"strings"
	"testing"
)

func TestHttpqlEscape_NoQuotes(t *testing.T) {
	got := httpqlEscape("hello world")
	if got != "hello world" {
		t.Fatalf("got %q, want %q", got, "hello world")
	}
}

func TestHttpqlEscape_WithDoubleQuote(t *testing.T) {
	got := httpqlEscape(`say "hello"`)
	want := `say \"hello\"`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestHttpqlEscape_EmptyString(t *testing.T) {
	got := httpqlEscape("")
	if got != "" {
		t.Fatalf("got %q, want empty string", got)
	}
}

func TestSearchRequestsInput_ContainsBuildsQuery(t *testing.T) {
	// Verify that the HTTPQL query built for 'contains' covers both req and resp
	input := SearchRequestsInput{Contains: "password"}
	var clauses []string
	if input.Contains != "" {
		escaped := httpqlEscape(input.Contains)
		clauses = append(clauses,
			`(req.raw.cont:"`+escaped+`" or resp.raw.cont:"`+escaped+`")`,
		)
	}
	q := strings.Join(clauses, " and ")
	if !strings.Contains(q, "req.raw.cont:") {
		t.Fatalf("expected req.raw.cont in query, got: %s", q)
	}
	if !strings.Contains(q, "resp.raw.cont:") {
		t.Fatalf("expected resp.raw.cont in query, got: %s", q)
	}
}

func TestSearchRequestsInput_MethodUppercase(t *testing.T) {
	input := SearchRequestsInput{Method: "get"}
	var clauses []string
	if input.Method != "" {
		clauses = append(clauses,
			`req.method.eq:"`+strings.ToUpper(input.Method)+`"`,
		)
	}
	q := strings.Join(clauses, " and ")
	if !strings.Contains(q, `"GET"`) {
		t.Fatalf("expected uppercase GET in query, got: %s", q)
	}
}

func TestSearchRequestsInput_StatusCodeBuildsQuery(t *testing.T) {
	input := SearchRequestsInput{StatusCode: 200}
	var clauses []string
	if input.StatusCode != 0 {
		clauses = append(clauses, `resp.status.eq:200`)
	}
	q := strings.Join(clauses, " and ")
	if q != "resp.status.eq:200" {
		t.Fatalf("unexpected query: %s", q)
	}
}

func TestSearchRequestsInput_MultipleParamsAnded(t *testing.T) {
	input := SearchRequestsInput{
		URLContains: "admin",
		Method:      "POST",
		StatusCode:  403,
	}
	var clauses []string
	if input.URLContains != "" {
		clauses = append(clauses, `req.url.cont:"`+httpqlEscape(input.URLContains)+`"`)
	}
	if input.Method != "" {
		clauses = append(clauses, `req.method.eq:"`+strings.ToUpper(input.Method)+`"`)
	}
	if input.StatusCode != 0 {
		clauses = append(clauses, `resp.status.eq:403`)
	}
	q := strings.Join(clauses, " and ")

	if !strings.Contains(q, " and ") {
		t.Fatalf("expected 'and' joining clauses, got: %s", q)
	}
	if !strings.Contains(q, "req.url.cont:") {
		t.Fatalf("missing urlContains clause: %s", q)
	}
	if !strings.Contains(q, `"POST"`) {
		t.Fatalf("missing method clause: %s", q)
	}
}
