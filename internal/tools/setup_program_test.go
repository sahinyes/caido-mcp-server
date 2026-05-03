package tools

import (
	"context"
	"strings"
	"testing"
)

func TestSetupProgramHandler_RejectsEmptyScopeIncludeEntry(t *testing.T) {
	handler := setupProgramHandler(nil)
	_, _, err := handler(context.Background(), nil, SetupProgramInput{
		ProjectName:  "test",
		ScopeInclude: []string{"valid.com", ""},
	})
	if err == nil || !strings.Contains(err.Error(), "scopeInclude[1]") {
		t.Fatalf("expected scopeInclude[1] error, got: %v", err)
	}
}

func TestSetupProgramHandler_RejectsWhitespaceOnlyScopeIncludeEntry(t *testing.T) {
	handler := setupProgramHandler(nil)
	_, _, err := handler(context.Background(), nil, SetupProgramInput{
		ProjectName:  "test",
		ScopeInclude: []string{"  "},
	})
	if err == nil || !strings.Contains(err.Error(), "scopeInclude[0]") {
		t.Fatalf("expected scopeInclude[0] error, got: %v", err)
	}
}

func TestSetupProgramHandler_RejectsEmptyScopeExcludeEntry(t *testing.T) {
	handler := setupProgramHandler(nil)
	_, _, err := handler(context.Background(), nil, SetupProgramInput{
		ProjectName:  "test",
		ScopeInclude: []string{"valid.com"},
		ScopeExclude: []string{""},
	})
	if err == nil || !strings.Contains(err.Error(), "scopeExclude[0]") {
		t.Fatalf("expected scopeExclude[0] error, got: %v", err)
	}
}
