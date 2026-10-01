package tools

import (
	"context"

	caido "github.com/caido-community/sdk-go"
	gen "github.com/caido-community/sdk-go/graphql"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ListReplaySessionsInput is the input for the list_replay_sessions tool
type ListReplaySessionsInput struct{}

// ReplaySessionSummary is a summary of a replay session
type ReplaySessionSummary struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	ActiveEntryID *string `json:"activeEntryId,omitempty"`
}

// ListReplaySessionsOutput is the output of the list_replay_sessions tool
type ListReplaySessionsOutput struct {
	Sessions []ReplaySessionSummary `json:"sessions"`
	Total    int                    `json:"total"`
}

// listReplaySessionsHandler creates the handler function
func listReplaySessionsHandler(
	client *caido.Client,
) func(context.Context, *mcp.CallToolRequest, ListReplaySessionsInput) (*mcp.CallToolResult, ListReplaySessionsOutput, error) {
	return func(
		ctx context.Context,
		req *mcp.CallToolRequest,
		input ListReplaySessionsInput,
	) (*mcp.CallToolResult, ListReplaySessionsOutput, error) {
		// Total comes from the server's own count, not from len(Sessions).
		// The SDK's ListSessionSummaries would be tidier, but it drops the
		// connection's count, and then a page the server decided to truncate
		// would read as a complete list — the whole set does come back in one
		// page today (546 of 546, measured 2026-10-01), which is exactly the
		// kind of thing that stops being true quietly. Reported separately,
		// Total > len(sessions) says so out loud.
		resp, err := client.Replay.ListSessions(ctx, nil)
		if err != nil {
			return nil, ListReplaySessionsOutput{}, err
		}

		conn := resp.ReplaySessions
		output := ListReplaySessionsOutput{
			Sessions: make([]ReplaySessionSummary, 0, len(conn.Edges)),
			Total:    conn.Count.Value,
		}

		// ReplaySession became an interface in 0.57 (HTTP and WS variants), so
		// activeEntry and collection no longer sit on the node itself.
		for _, edge := range conn.Edges {
			node := edge.Node
			if node == nil {
				continue
			}
			summary := ReplaySessionSummary{
				ID:   node.GetId(),
				Name: node.GetName(),
			}
			if httpNode, ok := node.(*gen.ListReplaySessionsReplaySessionsReplaySessionConnectionEdgesReplaySessionEdgeNodeReplaySessionHttp); ok &&
				httpNode.ActiveEntry != nil {
				id := (*httpNode.ActiveEntry).GetId()
				summary.ActiveEntryID = &id
			}
			output.Sessions = append(output.Sessions, summary)
		}

		return nil, output, nil
	}
}

// RegisterListReplaySessionsTool registers the tool with the MCP server
func RegisterListReplaySessionsTool(
	server *mcp.Server, client *caido.Client,
) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "caido_list_replay_sessions",
		Description: `List replay sessions. Returns id and name for each session.`,
		InputSchema: map[string]any{"type": "object"},
	}, listReplaySessionsHandler(client))
}
