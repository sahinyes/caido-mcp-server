package tools

import (
	"context"
	"fmt"
	"time"

	caido "github.com/caido-community/sdk-go"
	"github.com/c0tton-fluff/caido-mcp-server/internal/httputil"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// GetFindingInput is the input for the get_finding tool
type GetFindingInput struct {
	ID string `json:"id" jsonschema:"required,Finding ID to retrieve"`
}

// GetFindingRequestDetail is the request info embedded in a finding
type GetFindingRequestDetail struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Host   string `json:"host"`
	Port   int    `json:"port"`
	Path   string `json:"path"`
	Query  string `json:"query,omitempty"`
	IsTLS  bool   `json:"isTls"`
	URL    string `json:"url"`
}

// GetFindingOutput is the output of the get_finding tool
type GetFindingOutput struct {
	ID          string                   `json:"id"`
	Title       string                   `json:"title"`
	Host        string                   `json:"host"`
	Path        string                   `json:"path"`
	Reporter    string                   `json:"reporter"`
	Description *string                  `json:"description,omitempty"`
	DedupeKey   *string                  `json:"dedupeKey,omitempty"`
	Hidden      bool                     `json:"hidden"`
	CreatedAt   string                   `json:"createdAt"`
	Request     *GetFindingRequestDetail `json:"request,omitempty"`
}

// getFindingHandler creates the handler function
func getFindingHandler(
	client *caido.Client,
) func(context.Context, *mcp.CallToolRequest, GetFindingInput) (*mcp.CallToolResult, GetFindingOutput, error) {
	return func(
		ctx context.Context,
		req *mcp.CallToolRequest,
		input GetFindingInput,
	) (*mcp.CallToolResult, GetFindingOutput, error) {
		if input.ID == "" {
			return nil, GetFindingOutput{}, fmt.Errorf(
				"finding ID is required",
			)
		}

		resp, err := client.Findings.Get(ctx, input.ID)
		if err != nil {
			return nil, GetFindingOutput{}, fmt.Errorf(
				"failed to get finding %s: %w", input.ID, err,
			)
		}
		if resp.Finding == nil {
			return nil, GetFindingOutput{}, fmt.Errorf(
				"finding %s not found", input.ID,
			)
		}

		f := resp.Finding
		output := GetFindingOutput{
			ID:          f.Id,
			Title:       f.Title,
			Host:        f.Host,
			Path:        f.Path,
			Reporter:    f.Reporter,
			Description: f.Description,
			DedupeKey:   f.DedupeKey,
			Hidden:      f.Hidden,
			CreatedAt:   time.UnixMilli(f.CreatedAt).Format(time.RFC3339),
		}

		if f.Request.Id != "" {
			r := f.Request
			output.Request = &GetFindingRequestDetail{
				ID:     r.Id,
				Method: r.Method,
				Host:   r.Host,
				Port:   r.Port,
				Path:   r.Path,
				Query:  r.Query,
				IsTLS:  r.IsTls,
				URL:    httputil.BuildURL(r.IsTls, r.Host, r.Port, r.Path, r.Query),
			}
		}

		return nil, output, nil
	}
}

// RegisterGetFindingTool registers the tool with the MCP server
func RegisterGetFindingTool(
	server *mcp.Server, client *caido.Client,
) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "caido_get_finding",
		Description: `Get a single finding by ID with full details including title, host, path, reporter, description, and associated request. Params: id (finding ID).`,
	}, getFindingHandler(client))
}
