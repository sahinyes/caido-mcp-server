package tools

import (
	"context"
	"fmt"

	caido "github.com/caido-community/sdk-go"
	gen "github.com/caido-community/sdk-go/graphql"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// CreateAutomateSessionInput is the input for the create_automate_session tool
type CreateAutomateSessionInput struct {
	RequestID string `json:"requestId,omitempty" jsonschema:"Seed session from existing request ID (from HTTP history)"`
}

// CreateAutomateSessionOutput is the output of the create_automate_session tool
type CreateAutomateSessionOutput struct {
	SessionID string `json:"sessionId"`
	Name      string `json:"name"`
}

func createAutomateSessionHandler(
	client *caido.Client,
) func(context.Context, *mcp.CallToolRequest, CreateAutomateSessionInput) (*mcp.CallToolResult, CreateAutomateSessionOutput, error) {
	return func(
		ctx context.Context,
		req *mcp.CallToolRequest,
		input CreateAutomateSessionInput,
	) (*mcp.CallToolResult, CreateAutomateSessionOutput, error) {
		sessionInput := &gen.CreateAutomateSessionInput{}

		if input.RequestID != "" {
			sessionInput.RequestSource = &gen.RequestSourceInput{
				Id: &input.RequestID,
			}
		}

		resp, err := client.Automate.CreateSession(ctx, sessionInput)
		if err != nil {
			return nil, CreateAutomateSessionOutput{}, fmt.Errorf(
				"failed to create automate session: %w", err,
			)
		}

		s := resp.CreateAutomateSession.Session
		if s == nil {
			return nil, CreateAutomateSessionOutput{}, fmt.Errorf(
				"create automate session returned no session",
			)
		}

		return nil, CreateAutomateSessionOutput{
			SessionID: s.Id,
			Name:      s.Name,
		}, nil
	}
}

// RegisterCreateAutomateSessionTool registers the tool with the MCP server
func RegisterCreateAutomateSessionTool(
	server *mcp.Server, client *caido.Client,
) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "caido_create_automate_session",
		Description: `Create a new automate session, optionally seeded from an existing request ID. Returns sessionId for use with caido_start_automate or caido_automate_task_control.`,
	}, createAutomateSessionHandler(client))
}
