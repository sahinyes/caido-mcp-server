package tools

import (
	"context"

	caido "github.com/caido-community/sdk-go"
	gen "github.com/caido-community/sdk-go/graphql"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// GetSitemapInput is the input for the get_sitemap tool
type GetSitemapInput struct {
	ParentID    string  `json:"parentId,omitempty" jsonschema:"Parent entry ID to get children (omit for root domains)"`
	ScopeID     *string `json:"scopeId,omitempty" jsonschema:"Filter root entries to a scope ID"`
	Host        string  `json:"host,omitempty" jsonschema:"Filter entries by exact host label (e.g. api.example.com)"`
	PathPrefix  string  `json:"pathPrefix,omitempty" jsonschema:"Filter entries by path prefix (e.g. /api/v2/)"`
	Depth       int     `json:"depth,omitempty" jsonschema:"Max directory depth to return (default unlimited)"`
	MaxEndpoints int    `json:"maxEndpoints,omitempty" jsonschema:"Hard cap on returned entries (default 500)"`
}

// SitemapEntrySummary is a summary of a sitemap entry
type SitemapEntrySummary struct {
	ID             string  `json:"id"`
	Label          string  `json:"label"`
	Kind           string  `json:"kind"`
	HasDescendants bool    `json:"hasDescendants"`
	RequestID      *string `json:"requestId,omitempty"`
	Method         *string `json:"method,omitempty"`
	StatusCode     *int    `json:"statusCode,omitempty"`
}

// GetSitemapOutput is the output of the get_sitemap tool
type GetSitemapOutput struct {
	Entries []SitemapEntrySummary `json:"entries"`
}

// getSitemapHandler creates the handler function
func getSitemapHandler(
	client *caido.Client,
) func(context.Context, *mcp.CallToolRequest, GetSitemapInput) (*mcp.CallToolResult, GetSitemapOutput, error) {
	return func(
		ctx context.Context,
		req *mcp.CallToolRequest,
		input GetSitemapInput,
	) (*mcp.CallToolResult, GetSitemapOutput, error) {
		maxEndpoints := input.MaxEndpoints
		if maxEndpoints <= 0 {
			maxEndpoints = 500
		}

		entries := make([]SitemapEntrySummary, 0)

		if input.ParentID == "" {
			resp, err := client.Sitemap.ListRootEntries(ctx, input.ScopeID)
			if err != nil {
				return nil, GetSitemapOutput{}, err
			}

			for _, edge := range resp.SitemapRootEntries.Edges {
				if len(entries) >= maxEndpoints {
					break
				}
				e := edge.Node
				if input.Host != "" && e.Label != input.Host {
					continue
				}
				entries = append(entries, SitemapEntrySummary{
					ID:             e.Id,
					Label:          e.Label,
					Kind:           string(e.Kind),
					HasDescendants: e.HasDescendants,
				})
			}
		} else {
			depth := 1
			if input.Depth > 1 {
				depth = input.Depth
			}
			_ = depth // BFS traversal uses direct depth per level

			resp, err := client.Sitemap.ListDescendantEntries(
				ctx, input.ParentID,
				gen.SitemapDescendantsDepthDirect,
			)
			if err != nil {
				return nil, GetSitemapOutput{}, err
			}

			for _, edge := range resp.SitemapDescendantEntries.Edges {
				if len(entries) >= maxEndpoints {
					break
				}
				e := edge.Node
				if input.Host != "" && e.Label != input.Host {
					continue
				}
				if input.PathPrefix != "" {
					label := e.Label
					if len(label) < len(input.PathPrefix) || label[:len(input.PathPrefix)] != input.PathPrefix {
						continue
					}
				}
				summary := SitemapEntrySummary{
					ID:             e.Id,
					Label:          e.Label,
					Kind:           string(e.Kind),
					HasDescendants: e.HasDescendants,
				}
				if e.Request != nil {
					id := e.Request.Id
					method := e.Request.Method
					summary.RequestID = &id
					summary.Method = &method
					if e.Request.Response != nil {
						sc := e.Request.Response.StatusCode
						summary.StatusCode = &sc
					}
				}
				entries = append(entries, summary)
			}
		}

		return nil, GetSitemapOutput{Entries: entries}, nil
	}
}

// RegisterGetSitemapTool registers the tool with the MCP server
func RegisterGetSitemapTool(
	server *mcp.Server, client *caido.Client,
) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "caido_get_sitemap",
		Description: `Get sitemap. No params=root domains. parentId=children. Returns id/label/kind.`,
	}, getSitemapHandler(client))
}
