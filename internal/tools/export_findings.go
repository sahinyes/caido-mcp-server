package tools

import (
	"context"
	"fmt"

	caido "github.com/caido-community/sdk-go"
	gen "github.com/caido-community/sdk-go/graphql"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ExportFindingsInput is the input for the tool
type ExportFindingsInput struct {
	IDs      []string `json:"ids,omitempty" jsonschema:"List of finding IDs to export"`
	Reporter string   `json:"reporter,omitempty" jsonschema:"Export all findings by this reporter name"`
}

// ExportFindingsOutput is the output
type ExportFindingsOutput struct {
	ExportID string `json:"exportId"`
}

func exportFindingsHandler(
	client *caido.Client,
) func(context.Context, *mcp.CallToolRequest, ExportFindingsInput) (*mcp.CallToolResult, ExportFindingsOutput, error) {
	return func(
		ctx context.Context,
		req *mcp.CallToolRequest,
		input ExportFindingsInput,
	) (*mcp.CallToolResult, ExportFindingsOutput, error) {
		if len(input.IDs) == 0 && input.Reporter == "" {
			return nil, ExportFindingsOutput{}, fmt.Errorf(
				"provide either ids or reporter",
			)
		}

		expInput := &gen.ExportFindingsInput{}
		if len(input.IDs) > 0 {
			expInput.Ids = input.IDs
		} else {
			expInput.Filter = &gen.FilterClauseFindingInput{
				Reporter: &input.Reporter,
			}
		}

		resp, err := client.Findings.Export(ctx, expInput)
		if err != nil {
			return nil, ExportFindingsOutput{}, err
		}

		payload := resp.ExportFindings
		if payload.Error != nil {
			return nil, ExportFindingsOutput{}, fmt.Errorf(
				"export findings failed",
			)
		}

		return nil, ExportFindingsOutput{
			ExportID: payload.Export.Id,
		}, nil
	}
}

// RegisterExportFindingsTool registers the tool
func RegisterExportFindingsTool(
	server *mcp.Server, client *caido.Client,
) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "caido_export_findings",
		Description: `Export findings. Filter by IDs or reporter name. Returns exportId for download.`,
	}, exportFindingsHandler(client))
}
