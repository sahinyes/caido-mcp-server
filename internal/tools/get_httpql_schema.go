package tools

import (
	"context"

	caido "github.com/caido-community/sdk-go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// HttpqlFieldDoc documents a single HTTPQL field namespace.
type HttpqlFieldDoc struct {
	Field     string   `json:"field"`
	Type      string   `json:"type"`
	Operators []string `json:"operators"`
	Notes     string   `json:"notes,omitempty"`
}

// HttpqlSchemaOutput is the output of the get_httpql_schema tool.
type HttpqlSchemaOutput struct {
	Fields      []HttpqlFieldDoc `json:"fields"`
	Combinators []string         `json:"combinators"`
	Examples    []string         `json:"examples"`
}

// GetHttpqlSchemaInput is the (empty) input for the get_httpql_schema tool.
type GetHttpqlSchemaInput struct{}

func getHttpqlSchemaHandler(
	_ *caido.Client,
) func(context.Context, *mcp.CallToolRequest, GetHttpqlSchemaInput) (*mcp.CallToolResult, HttpqlSchemaOutput, error) {
	return func(
		_ context.Context,
		_ *mcp.CallToolRequest,
		_ GetHttpqlSchemaInput,
	) (*mcp.CallToolResult, HttpqlSchemaOutput, error) {
		output := HttpqlSchemaOutput{
			Combinators: []string{"and", "or"},
			Fields: []HttpqlFieldDoc{
				{
					Field:     "req.url",
					Type:      "string",
					Operators: []string{"cont", "ncont", "eq", "neq", "like", "nlike"},
					Notes:     "Full URL including scheme, host, path, and query string",
				},
				{
					Field:     "req.host",
					Type:      "string",
					Operators: []string{"cont", "ncont", "eq", "neq", "like", "nlike"},
					Notes:     "Hostname only (no port)",
				},
				{
					Field:     "req.method",
					Type:      "string",
					Operators: []string{"eq", "neq"},
					Notes:     "HTTP method in uppercase: GET, POST, PUT, PATCH, DELETE, etc.",
				},
				{
					Field:     "req.path",
					Type:      "string",
					Operators: []string{"cont", "ncont", "eq", "neq", "like", "nlike"},
					Notes:     "URL path component only",
				},
				{
					Field:     "req.raw",
					Type:      "string",
					Operators: []string{"cont", "ncont"},
					Notes:     "Full raw request including headers and body",
				},
				{
					Field:     "req.body",
					Type:      "string",
					Operators: []string{"cont", "ncont"},
					Notes:     "Request body only",
				},
				{
					Field:     "resp.status",
					Type:      "integer",
					Operators: []string{"eq", "neq", "gt", "gte", "lt", "lte"},
					Notes:     "HTTP response status code",
				},
				{
					Field:     "resp.raw",
					Type:      "string",
					Operators: []string{"cont", "ncont"},
					Notes:     "Full raw response including headers and body",
				},
				{
					Field:     "resp.body",
					Type:      "string",
					Operators: []string{"cont", "ncont"},
					Notes:     "Response body only",
				},
			},
			Examples: []string{
				`req.host.eq:"api.example.com"`,
				`req.method.eq:"POST" and req.url.cont:"/graphql"`,
				`resp.status.eq:401 or resp.status.eq:403`,
				`req.body.cont:"password" and resp.status.neq:200`,
				`req.url.like:"%.json%"`,
				`(req.raw.cont:"Bearer" or req.raw.cont:"token") and resp.status.eq:200`,
				`resp.body.cont:"error" and resp.status.gte:500`,
			},
		}
		return nil, output, nil
	}
}

// RegisterGetHttpqlSchemaTool registers the tool with the MCP server.
func RegisterGetHttpqlSchemaTool(
	server *mcp.Server, client *caido.Client,
) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "caido_get_httpql_schema",
		Description: `Return the HTTPQL query language reference: fields, operators, combinators, and examples. Use this before writing httpql filters for list_requests, search_requests, list_intercept_entries, or tamper rule conditions.`,
	}, getHttpqlSchemaHandler(client))
}
