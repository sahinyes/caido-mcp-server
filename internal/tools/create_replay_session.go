package tools

import (
	"context"
	"fmt"

	"github.com/c0tton-fluff/caido-mcp-server/internal/replay"
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

		// Required since 0.57: ReplaySessionKind! has no default server-side.
		sessionInput.Kind = gen.ReplaySessionKindHttp

		// A session with no requestSource is created with no entry, and there
		// is no mutation that adds one later — so it could never be sent to.
		// Seed it, with the named request when there is one.
		if input.RequestID != "" {
			sessionInput.RequestSource = &gen.RequestSourceInput{
				Id: &input.RequestID,
			}
		} else {
			sessionInput.RequestSource = replay.PlaceholderSource()
		}

		sessionID, _, err := client.Replay.CreateSession(ctx, sessionInput)
		if err != nil {
			return nil, CreateReplaySessionOutput{}, fmt.Errorf(
				"failed to create replay session: %w", err,
			)
		}

		// createReplaySession takes no name — it never has, so this tool
		// accepted `name` and silently dropped it. Caido names a new session
		// after its number; renaming is a separate mutation.
		name := ""
		if input.Name != "" {
			if _, rerr := client.Replay.RenameSession(
				ctx, sessionID, input.Name,
			); rerr != nil {
				return nil, CreateReplaySessionOutput{}, fmt.Errorf(
					"session %s created but renaming it failed: %w",
					sessionID, rerr,
				)
			}
			name = input.Name
		}

		// The create payload's session is an interface now and the SDK returns
		// ids only, so an unnamed session's name is read back rather than
		// reported as empty.
		if name == "" {
			if sess, nerr := client.Replay.GetSession(
				ctx, sessionID,
			); nerr == nil && sess != nil {
				name = sess.Name
			}
		}

		return nil, CreateReplaySessionOutput{
			SessionID: sessionID,
			Name:      name,
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
