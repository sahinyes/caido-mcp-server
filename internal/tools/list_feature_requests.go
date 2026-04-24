package tools

import (
	"context"
	"fmt"

	"github.com/c0tton-fluff/caido-mcp-server/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ListFeatureRequestsInput is the input for the list_feature_requests tool.
type ListFeatureRequestsInput struct {
	Priority string `json:"priority,omitempty" jsonschema:"Filter by priority: low medium high"`
	Tag      string `json:"tag,omitempty"      jsonschema:"Filter by tag"`
}

// ListFeatureRequestsOutput is the output of the list_feature_requests tool.
type ListFeatureRequestsOutput struct {
	FeatureRequests []store.FeatureRequest `json:"featureRequests"`
	Total           int                    `json:"total"`
	Summary         string                 `json:"summary"`
}

func listFeatureRequestsHandler(
	fr *store.FeatureRequestStore,
) func(context.Context, *mcp.CallToolRequest, ListFeatureRequestsInput) (*mcp.CallToolResult, ListFeatureRequestsOutput, error) {
	return func(
		ctx context.Context,
		req *mcp.CallToolRequest,
		input ListFeatureRequestsInput,
	) (*mcp.CallToolResult, ListFeatureRequestsOutput, error) {
		all, err := fr.List()
		if err != nil {
			return nil, ListFeatureRequestsOutput{}, fmt.Errorf(
				"list feature requests: %w", err,
			)
		}

		filtered := all[:0:0]
		for _, f := range all {
			if input.Priority != "" && f.Priority != input.Priority {
				continue
			}
			if input.Tag != "" && !hasTag(f.Tags, input.Tag) {
				continue
			}
			filtered = append(filtered, f)
		}

		summary := fmt.Sprintf("%d feature request(s)", len(filtered))
		if len(filtered) == 0 {
			summary = "No feature requests found."
		}

		return nil, ListFeatureRequestsOutput{
			FeatureRequests: filtered,
			Total:           len(filtered),
			Summary:         summary,
		}, nil
	}
}

func hasTag(tags []string, tag string) bool {
	for _, t := range tags {
		if t == tag {
			return true
		}
	}
	return false
}

// RegisterListFeatureRequestsTool registers the list_feature_requests tool.
func RegisterListFeatureRequestsTool(
	server *mcp.Server, fr *store.FeatureRequestStore,
) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "caido_list_feature_requests",
		Description: `List all submitted feature requests for the Caido MCP server. Optionally filter by priority or tag.`,
	}, listFeatureRequestsHandler(fr))
}
