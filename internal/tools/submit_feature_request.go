package tools

import (
	"context"
	"fmt"

	"github.com/c0tton-fluff/caido-mcp-server/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SubmitFeatureRequestInput is the input for the submit_feature_request tool.
type SubmitFeatureRequestInput struct {
	Title       string   `json:"title"       jsonschema:"required,Short title for the feature request"`
	Description string   `json:"description" jsonschema:"required,Detailed description of the desired feature"`
	Priority    string   `json:"priority,omitempty" jsonschema:"Priority level: low medium high (default: medium)"`
	Tags        []string `json:"tags,omitempty"    jsonschema:"Optional labels e.g. UX performance workflow"`
}

// SubmitFeatureRequestOutput is the output of the submit_feature_request tool.
type SubmitFeatureRequestOutput struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Priority    string   `json:"priority"`
	Tags        []string `json:"tags,omitempty"`
	CreatedAt   string   `json:"createdAt"`
	Message     string   `json:"message"`
}

func submitFeatureRequestHandler(
	fr *store.FeatureRequestStore,
) func(context.Context, *mcp.CallToolRequest, SubmitFeatureRequestInput) (*mcp.CallToolResult, SubmitFeatureRequestOutput, error) {
	return func(
		ctx context.Context,
		req *mcp.CallToolRequest,
		input SubmitFeatureRequestInput,
	) (*mcp.CallToolResult, SubmitFeatureRequestOutput, error) {
		priority := input.Priority
		switch priority {
		case "low", "medium", "high":
		case "":
			priority = "medium"
		default:
			return nil, SubmitFeatureRequestOutput{}, fmt.Errorf(
				"invalid priority %q: must be low, medium, or high", priority,
			)
		}

		saved, err := fr.Add(input.Title, input.Description, priority, input.Tags)
		if err != nil {
			return nil, SubmitFeatureRequestOutput{}, fmt.Errorf(
				"save feature request: %w", err,
			)
		}

		return nil, SubmitFeatureRequestOutput{
			ID:          saved.ID,
			Title:       saved.Title,
			Description: saved.Description,
			Priority:    saved.Priority,
			Tags:        saved.Tags,
			CreatedAt:   saved.CreatedAt,
			Message: fmt.Sprintf(
				"Feature request %q submitted (id: %s). Use list_feature_requests to track it and delete_feature_request to close it once implemented.",
				saved.Title, saved.ID,
			),
		}, nil
	}
}

// RegisterSubmitFeatureRequestTool registers the submit_feature_request tool.
func RegisterSubmitFeatureRequestTool(
	server *mcp.Server, fr *store.FeatureRequestStore,
) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "caido_submit_feature_request",
		Description: `Submit a feature request for the Caido MCP server.
Feature requests are stored locally and can be listed and deleted once implemented.`,
	}, submitFeatureRequestHandler(fr))
}
