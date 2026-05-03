package tools

import (
	"context"
	"strings"
	"testing"
)

func TestCreateScopeHandler_RejectsEmptyAllowlistEntry(t *testing.T) {
	handler := createScopeHandler(nil)
	_, _, err := handler(context.Background(), nil, CreateScopeInput{
		Name:      "test",
		Allowlist: []string{"example.com", ""},
	})
	if err == nil || !strings.Contains(err.Error(), "allowlist[1]") {
		t.Fatalf("expected allowlist[1] error, got: %v", err)
	}
}

func TestCreateScopeHandler_RejectsWhitespaceOnlyEntry(t *testing.T) {
	handler := createScopeHandler(nil)
	_, _, err := handler(context.Background(), nil, CreateScopeInput{
		Name:      "test",
		Allowlist: []string{"   "},
	})
	if err == nil || !strings.Contains(err.Error(), "allowlist[0]") {
		t.Fatalf("expected allowlist[0] error, got: %v", err)
	}
}

func TestCreateScopeHandler_RejectsEmptyDenylistEntry(t *testing.T) {
	handler := createScopeHandler(nil)
	_, _, err := handler(context.Background(), nil, CreateScopeInput{
		Name:      "test",
		Allowlist: []string{"example.com"},
		Denylist:  []string{""},
	})
	if err == nil || !strings.Contains(err.Error(), "denylist[0]") {
		t.Fatalf("expected denylist[0] error, got: %v", err)
	}
}
