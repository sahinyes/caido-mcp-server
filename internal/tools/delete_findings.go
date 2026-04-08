package tools

import (
	"context"
	"fmt"

	caido "github.com/caido-community/sdk-go"
	gen "github.com/caido-community/sdk-go/graphql"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// DeleteFindingsInput is the input for the tool
type DeleteFindingsInput struct {
	IDs      []string `json:"ids,omitempty" jsonschema:"List of finding IDs to delete"`
	Reporter string   `json:"reporter,omitempty" jsonschema:"Delete all findings by this reporter name"`
}

// DeleteFindingsOutput is the output
type DeleteFindingsOutput struct {
	DeletedIDs []string `json:"deletedIds"`
}

func deleteFindingsHandler(
	client *caido.Client,
) func(context.Context, *mcp.CallToolRequest, DeleteFindingsInput) (*mcp.CallToolResult, DeleteFindingsOutput, error) {
	return func(
		ctx context.Context,
		req *mcp.CallToolRequest,
		input DeleteFindingsInput,
	) (*mcp.CallToolResult, DeleteFindingsOutput, error) {
		if len(input.IDs) == 0 && input.Reporter == "" {
			return nil, DeleteFindingsOutput{}, fmt.Errorf(
				"provide either ids or reporter",
			)
		}

		delInput := &gen.DeleteFindingsInput{}
		if len(input.IDs) > 0 {
			delInput.Ids = input.IDs
		} else {
			delInput.Reporter = &input.Reporter
		}

		resp, err := client.Findings.Delete(ctx, delInput)
		if err != nil {
			return nil, DeleteFindingsOutput{}, err
		}

		return nil, DeleteFindingsOutput{
			DeletedIDs: resp.DeleteFindings.DeletedIds,
		}, nil
	}
}

// RegisterDeleteFindingsTool registers the tool
func RegisterDeleteFindingsTool(
	server *mcp.Server, client *caido.Client,
) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "caido_delete_findings",
		Description: `Delete findings by IDs or by reporter name. Params: ids (list) or reporter (string).`,
	}, deleteFindingsHandler(client))
}
