package tools

import (
	"context"
	"fmt"
	"strings"

	caido "github.com/caido-community/sdk-go"
	"github.com/c0tton-fluff/caido-mcp-server/internal/httputil"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SearchRequestsInput is the input for the search_requests tool
type SearchRequestsInput struct {
	Contains     string `json:"contains,omitempty" jsonschema:"Search string anywhere in request or response"`
	BodyContains string `json:"bodyContains,omitempty" jsonschema:"Search string in request or response body"`
	URLContains  string `json:"urlContains,omitempty" jsonschema:"Search string in URL"`
	Method       string `json:"method,omitempty" jsonschema:"Filter by HTTP method (GET, POST, etc.)"`
	StatusCode   int    `json:"statusCode,omitempty" jsonschema:"Filter by response status code"`
	Limit        int    `json:"limit,omitempty" jsonschema:"Maximum results to return (default 20, max 100)"`
	After        string `json:"after,omitempty" jsonschema:"Cursor for pagination"`
}

// SearchRequestsOutput is the output of the search_requests tool
type SearchRequestsOutput struct {
	Requests   []RequestSummary `json:"requests"`
	HasMore    bool             `json:"hasMore"`
	NextCursor string           `json:"nextCursor,omitempty"`
	Query      string           `json:"query,omitempty"`
}

// searchRequestsHandler creates the handler function
func searchRequestsHandler(
	client *caido.Client,
) func(context.Context, *mcp.CallToolRequest, SearchRequestsInput) (*mcp.CallToolResult, SearchRequestsOutput, error) {
	return func(
		ctx context.Context,
		req *mcp.CallToolRequest,
		input SearchRequestsInput,
	) (*mcp.CallToolResult, SearchRequestsOutput, error) {
		var clauses []string

		if input.Contains != "" {
			escaped := httpqlEscape(input.Contains)
			clauses = append(clauses,
				fmt.Sprintf(`(req.raw.cont:"%s" or resp.raw.cont:"%s")`, escaped, escaped),
			)
		}
		if input.BodyContains != "" {
			escaped := httpqlEscape(input.BodyContains)
			clauses = append(clauses,
				fmt.Sprintf(`(req.body.cont:"%s" or resp.body.cont:"%s")`, escaped, escaped),
			)
		}
		if input.URLContains != "" {
			escaped := httpqlEscape(input.URLContains)
			clauses = append(clauses,
				fmt.Sprintf(`req.url.cont:"%s"`, escaped),
			)
		}
		if input.Method != "" {
			clauses = append(clauses,
				fmt.Sprintf(`req.method.eq:"%s"`, strings.ToUpper(input.Method)),
			)
		}
		if input.StatusCode != 0 {
			clauses = append(clauses,
				fmt.Sprintf(`resp.status.eq:%d`, input.StatusCode),
			)
		}

		if len(clauses) == 0 {
			return nil, SearchRequestsOutput{}, fmt.Errorf(
				"at least one search parameter is required",
			)
		}

		httpql := strings.Join(clauses, " and ")

		limit := input.Limit
		if limit <= 0 {
			limit = 20
		}
		if limit > 100 {
			limit = 100
		}

		opts := &caido.ListRequestsOptions{
			First:  &limit,
			Filter: &httpql,
		}
		if input.After != "" {
			opts.After = &input.After
		}

		resp, err := client.Requests.List(ctx, opts)
		if err != nil {
			return nil, SearchRequestsOutput{}, fmt.Errorf(
				"search failed: %w", err,
			)
		}

		conn := resp.Requests
		output := SearchRequestsOutput{
			Requests: make([]RequestSummary, 0, len(conn.Edges)),
			Query:    httpql,
		}

		for _, edge := range conn.Edges {
			r := edge.Node
			summary := RequestSummary{
				ID:     r.Id,
				Method: r.Method,
				URL: httputil.BuildURL(
					r.IsTls, r.Host, r.Port, r.Path, r.Query,
				),
			}
			if r.Response != nil {
				summary.StatusCode = r.Response.StatusCode
			}
			output.Requests = append(output.Requests, summary)
		}

		if conn.PageInfo.HasNextPage {
			output.HasMore = true
			if conn.PageInfo.EndCursor != nil {
				output.NextCursor = *conn.PageInfo.EndCursor
			}
		}

		return nil, output, nil
	}
}

// httpqlEscape escapes double quotes in HTTPQL string values
func httpqlEscape(s string) string {
	return strings.ReplaceAll(s, `"`, `\"`)
}

// RegisterSearchRequestsTool registers the tool with the MCP server
func RegisterSearchRequestsTool(
	server *mcp.Server, client *caido.Client,
) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "caido_search_requests",
		Description: `Search requests without HTTPQL knowledge. Params: contains (anywhere), bodyContains (body only), urlContains (URL), method (GET/POST/etc), statusCode. Returns matching requests with id/method/url/status.`,
	}, searchRequestsHandler(client))
}
