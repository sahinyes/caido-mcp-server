package tools

import (
	"context"
	"fmt"

	"github.com/c0tton-fluff/caido-mcp-server/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// GetFeatureRequestInput is the input for the get_feature_request tool.
type GetFeatureRequestInput struct {
	ID string `json:"id" jsonschema:"required,Feature request ID returned by submit_feature_request"`
}

// GetFeatureRequestOutput is the output of the get_feature_request tool.
type GetFeatureRequestOutput struct {
	store.FeatureRequest
}

func getFeatureRequestHandler(
	fr *store.FeatureRequestStore,
) func(context.Context, *mcp.CallToolRequest, GetFeatureRequestInput) (*mcp.CallToolResult, GetFeatureRequestOutput, error) {
	return func(
		ctx context.Context,
		req *mcp.CallToolRequest,
		input GetFeatureRequestInput,
	) (*mcp.CallToolResult, GetFeatureRequestOutput, error) {
		f, err := fr.Get(input.ID)
		if err != nil {
			return nil, GetFeatureRequestOutput{}, fmt.Errorf(
				"get feature request: %w", err,
			)
		}
		return nil, GetFeatureRequestOutput{f}, nil
	}
}

// RegisterGetFeatureRequestTool registers the get_feature_request tool.
func RegisterGetFeatureRequestTool(
	server *mcp.Server, fr *store.FeatureRequestStore,
) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "caido_get_feature_request",
		Description: `Get details of a specific feature request by ID.`,
	}, getFeatureRequestHandler(fr))
}
