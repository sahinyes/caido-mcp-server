package tools

import "testing"

func TestNormaliseURL_WithScheme(t *testing.T) {
	got := normaliseURL("https://example.com/path")
	want := "example.com/path"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestNormaliseURL_RootPath(t *testing.T) {
	got := normaliseURL("https://example.com/")
	want := "example.com"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestNormaliseURL_NoPath(t *testing.T) {
	got := normaliseURL("https://example.com")
	want := "example.com"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestNormaliseURL_WithPort(t *testing.T) {
	got := normaliseURL("https://example.com:8443/api")
	want := "example.com:8443/api"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestMatchScopePattern_ExactHost(t *testing.T) {
	if !matchScopePattern("example.com", "example.com") {
		t.Fatal("exact host should match")
	}
}

func TestMatchScopePattern_WildcardSubdomain(t *testing.T) {
	if !matchScopePattern("*.example.com", "sub.example.com") {
		t.Fatal("wildcard subdomain should match")
	}
}

func TestMatchScopePattern_WildcardAll(t *testing.T) {
	if !matchScopePattern("*", "anything.example.com/path") {
		t.Fatal("* should match everything")
	}
}

func TestMatchScopePattern_WithPath(t *testing.T) {
	if !matchScopePattern("example.com/api/*", "example.com/api/v1") {
		t.Fatal("path glob should match subpath")
	}
}

func TestMatchScopePattern_NoMatch(t *testing.T) {
	if matchScopePattern("other.com", "example.com") {
		t.Fatal("different host should not match")
	}
}

func TestMatchScopePattern_SchemeStripped(t *testing.T) {
	// Patterns may include scheme — should be stripped
	if !matchScopePattern("https://example.com", "example.com") {
		t.Fatal("pattern with scheme should match after stripping")
	}
}

func TestMatchScopePattern_PrefixNoWildcard(t *testing.T) {
	if !matchScopePattern("example.com/api", "example.com/api/v1/users") {
		t.Fatal("prefix pattern should match longer path")
	}
}
