package tools

import (
	"context"

	caido "github.com/caido-community/sdk-go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SelectEnvironmentInput is the input for the tool
type SelectEnvironmentInput struct {
	ID string `json:"id" jsonschema:"required,Environment ID to select. Pass empty string to deselect."`
}

// SelectEnvironmentOutput is the output
type SelectEnvironmentOutput struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

func selectEnvironmentHandler(
	client *caido.Client,
) func(context.Context, *mcp.CallToolRequest, SelectEnvironmentInput) (*mcp.CallToolResult, SelectEnvironmentOutput, error) {
	return func(
		ctx context.Context,
		req *mcp.CallToolRequest,
		input SelectEnvironmentInput,
	) (*mcp.CallToolResult, SelectEnvironmentOutput, error) {
		var idPtr *string
		if input.ID != "" {
			idPtr = &input.ID
		}

		resp, err := client.Environments.Select(ctx, idPtr)
		if err != nil {
			return nil, SelectEnvironmentOutput{}, err
		}

		env := resp.SelectEnvironment.Environment
		if env == nil {
			return nil, SelectEnvironmentOutput{}, nil
		}

		return nil, SelectEnvironmentOutput{
			ID:   env.Id,
			Name: env.Name,
		}, nil
	}
}

// RegisterSelectEnvironmentTool registers the tool
func RegisterSelectEnvironmentTool(
	server *mcp.Server, client *caido.Client,
) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "caido_select_environment",
		Description: `Select active environment. Variables from selected env are used in replay placeholders. Pass empty id to deselect.`,
	}, selectEnvironmentHandler(client))
}
