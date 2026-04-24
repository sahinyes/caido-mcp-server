package tools

import (
	"context"
	"fmt"

	caido "github.com/caido-community/sdk-go"
	gen "github.com/caido-community/sdk-go/graphql"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// CreateReplaySessionInput is the input for the create_replay_session tool
type CreateReplaySessionInput struct {
	RequestID    string  `json:"requestId,omitempty" jsonschema:"Seed session from existing request ID (from HTTP history)"`
	Name         string  `json:"name,omitempty" jsonschema:"Name for the new session"`
	CollectionID *string `json:"collectionId,omitempty" jsonschema:"Replay collection ID to place the session in"`
}

// CreateReplaySessionOutput is the output of the create_replay_session tool
type CreateReplaySessionOutput struct {
	SessionID string `json:"sessionId"`
	Name      string `json:"name"`
}

func createReplaySessionHandler(
	client *caido.Client,
) func(context.Context, *mcp.CallToolRequest, CreateReplaySessionInput) (*mcp.CallToolResult, CreateReplaySessionOutput, error) {
	return func(
		ctx context.Context,
		req *mcp.CallToolRequest,
		input CreateReplaySessionInput,
	) (*mcp.CallToolResult, CreateReplaySessionOutput, error) {
		sessionInput := &gen.CreateReplaySessionInput{
			CollectionId: input.CollectionID,
		}

		if input.RequestID != "" {
			sessionInput.RequestSource = &gen.RequestSourceInput{
				Id: &input.RequestID,
			}
		}

		resp, err := client.Replay.CreateSession(ctx, sessionInput)
		if err != nil {
			return nil, CreateReplaySessionOutput{}, fmt.Errorf(
				"failed to create replay session: %w", err,
			)
		}

		s := resp.CreateReplaySession.Session
		if s == nil {
			return nil, CreateReplaySessionOutput{}, fmt.Errorf(
				"create replay session returned no session",
			)
		}

		return nil, CreateReplaySessionOutput{
			SessionID: s.Id,
			Name:      s.Name,
		}, nil
	}
}

// RegisterCreateReplaySessionTool registers the tool with the MCP server
func RegisterCreateReplaySessionTool(
	server *mcp.Server, client *caido.Client,
) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "caido_create_replay_session",
		Description: `Create a new replay session, optionally seeded from an existing request ID. Returns sessionId for use with caido_send_request.`,
	}, createReplaySessionHandler(client))
}
