package tools

import (
	"context"
	"fmt"

	"github.com/c0tton-fluff/caido-mcp-server/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// DeleteFeatureRequestInput is the input for the delete_feature_request tool.
type DeleteFeatureRequestInput struct {
	ID     string `json:"id"               jsonschema:"required,Feature request ID to delete"`
	Reason string `json:"reason,omitempty" jsonschema:"Optional reason e.g. implemented as expected"`
}

// DeleteFeatureRequestOutput is the output of the delete_feature_request tool.
type DeleteFeatureRequestOutput struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Message string `json:"message"`
}

func deleteFeatureRequestHandler(
	fr *store.FeatureRequestStore,
) func(context.Context, *mcp.CallToolRequest, DeleteFeatureRequestInput) (*mcp.CallToolResult, DeleteFeatureRequestOutput, error) {
	return func(
		ctx context.Context,
		req *mcp.CallToolRequest,
		input DeleteFeatureRequestInput,
	) (*mcp.CallToolResult, DeleteFeatureRequestOutput, error) {
		f, err := fr.Get(input.ID)
		if err != nil {
			return nil, DeleteFeatureRequestOutput{}, fmt.Errorf(
				"delete feature request: %w", err,
			)
		}

		if err := fr.Delete(input.ID); err != nil {
			return nil, DeleteFeatureRequestOutput{}, fmt.Errorf(
				"delete feature request: %w", err,
			)
		}

		msg := fmt.Sprintf("Feature request %q (%s) deleted.", f.Title, f.ID)
		if input.Reason != "" {
			msg += " Reason: " + input.Reason
		}

		return nil, DeleteFeatureRequestOutput{
			ID:      f.ID,
			Title:   f.Title,
			Message: msg,
		}, nil
	}
}

// RegisterDeleteFeatureRequestTool registers the delete_feature_request tool.
func RegisterDeleteFeatureRequestTool(
	server *mcp.Server, fr *store.FeatureRequestStore,
) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "caido_delete_feature_request",
		Description: `Delete a feature request once you are satisfied it has been implemented correctly.
Provide an optional reason (e.g. "implemented as expected in v1.6").`,
	}, deleteFeatureRequestHandler(fr))
}
