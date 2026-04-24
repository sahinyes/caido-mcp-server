package tools

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"

	caido "github.com/caido-community/sdk-go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// CheckScopeInput is the input for the check_scope tool
type CheckScopeInput struct {
	URL string `json:"url" jsonschema:"required,URL to check against scopes"`
}

// ScopeMatch describes a scope that matched the URL
type ScopeMatch struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	MatchPattern string `json:"matchPattern"`
}

// CheckScopeOutput is the output of the check_scope tool
type CheckScopeOutput struct {
	InScope       bool         `json:"inScope"`
	URL           string       `json:"url"`
	MatchedScopes []ScopeMatch `json:"matchedScopes,omitempty"`
	DeniedBy      []ScopeMatch `json:"deniedBy,omitempty"`
}

// checkScopeHandler creates the handler function
func checkScopeHandler(
	client *caido.Client,
) func(context.Context, *mcp.CallToolRequest, CheckScopeInput) (*mcp.CallToolResult, CheckScopeOutput, error) {
	return func(
		ctx context.Context,
		req *mcp.CallToolRequest,
		input CheckScopeInput,
	) (*mcp.CallToolResult, CheckScopeOutput, error) {
		if input.URL == "" {
			return nil, CheckScopeOutput{}, fmt.Errorf(
				"url is required",
			)
		}

		resp, err := client.Scopes.List(ctx)
		if err != nil {
			return nil, CheckScopeOutput{}, fmt.Errorf(
				"failed to list scopes: %w", err,
			)
		}

		output := CheckScopeOutput{URL: input.URL}

		// Build a normalised host+path string to match against
		target := normaliseURL(input.URL)

		for _, scope := range resp.Scopes {
			// Check denylist first
			for _, pattern := range scope.Denylist {
				if matchScopePattern(pattern, target) {
					output.DeniedBy = append(output.DeniedBy, ScopeMatch{
						ID:           scope.Id,
						Name:         scope.Name,
						MatchPattern: pattern,
					})
				}
			}

			// Check allowlist
			for _, pattern := range scope.Allowlist {
				if matchScopePattern(pattern, target) {
					output.MatchedScopes = append(output.MatchedScopes, ScopeMatch{
						ID:           scope.Id,
						Name:         scope.Name,
						MatchPattern: pattern,
					})
					break // one allow match per scope is enough
				}
			}
		}

		// In scope = at least one allowlist match AND no denylist match
		output.InScope = len(output.MatchedScopes) > 0 && len(output.DeniedBy) == 0

		return nil, output, nil
	}
}

// normaliseURL strips the scheme and returns "host/path" for glob matching.
// This mirrors Caido's own scope matching logic where patterns are host-based.
func normaliseURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	result := u.Host
	if u.Path != "" && u.Path != "/" {
		result += u.Path
	}
	return result
}

// matchScopePattern checks whether a Caido scope pattern matches the target.
// Caido patterns use glob syntax: * matches any sequence within a path segment,
// a bare * is treated as match-everything.
func matchScopePattern(pattern, target string) bool {
	// Normalise pattern — strip scheme if present
	if idx := strings.Index(pattern, "://"); idx >= 0 {
		pattern = pattern[idx+3:]
	}

	// Bare wildcard matches everything
	if pattern == "*" {
		return true
	}

	// Try direct glob match
	if matched, err := path.Match(pattern, target); err == nil && matched {
		return true
	}

	// Try with wildcard suffix to handle "*.example.com" matching "sub.example.com/path"
	if matched, err := path.Match(pattern+"/*", target); err == nil && matched {
		return true
	}
	if matched, err := path.Match(pattern+"*", target); err == nil && matched {
		return true
	}

	// Try prefix match for patterns without wildcards
	if !strings.ContainsAny(pattern, "*?") {
		return strings.HasPrefix(target, pattern)
	}

	return false
}

// RegisterCheckScopeTool registers the tool with the MCP server
func RegisterCheckScopeTool(
	server *mcp.Server, client *caido.Client,
) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "caido_check_scope",
		Description: `Check if a URL is within any configured Caido scope. Returns inScope (bool), which scopes matched the allowlist, and which scopes denied it via denylist. Useful before testing to confirm target is in scope. Params: url (required).`,
	}, checkScopeHandler(client))
}
