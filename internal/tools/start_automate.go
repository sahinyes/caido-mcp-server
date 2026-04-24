package tools

import (
	"context"
	"fmt"

	caido "github.com/caido-community/sdk-go"
	gql "github.com/Khan/genqlient/graphql"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// StartAutomateInput is the input for the start_automate tool
type StartAutomateInput struct {
	RequestID string `json:"requestId" jsonschema:"required,Request ID to use as the automate session template"`
}

// StartAutomateOutput is the output of the start_automate tool
type StartAutomateOutput struct {
	SessionID string `json:"sessionId"`
	TaskID    string `json:"taskId"`
	Name      string `json:"name"`
}

// createAutomateSessionData is the response data for the CreateAutomateSession mutation
type createAutomateSessionData struct {
	CreateAutomateSession *struct {
		Session *struct {
			Id   string `json:"id"`
			Name string `json:"name"`
		} `json:"session"`
	} `json:"createAutomateSession"`
}

const createAutomateSessionMutation = `
mutation CreateAutomateSession($input: CreateAutomateSessionInput!) {
  createAutomateSession(input: $input) {
    session {
      id
      name
    }
  }
}`

// startAutomateHandler creates the handler function
func startAutomateHandler(
	client *caido.Client,
) func(context.Context, *mcp.CallToolRequest, StartAutomateInput) (*mcp.CallToolResult, StartAutomateOutput, error) {
	return func(
		ctx context.Context,
		req *mcp.CallToolRequest,
		input StartAutomateInput,
	) (*mcp.CallToolResult, StartAutomateOutput, error) {
		if input.RequestID == "" {
			return nil, StartAutomateOutput{}, fmt.Errorf(
				"requestId is required",
			)
		}

		// CreateAutomateSessionInput.requestSource is a @oneOf type —
		// only one field must be present. Use MakeRequest directly.
		sessionInput := map[string]interface{}{
			"requestSource": map[string]interface{}{
				"id": input.RequestID,
			},
		}

		resp := &gql.Response{Data: &createAutomateSessionData{}}
		err := client.GraphQL.MakeRequest(ctx, &gql.Request{
			Query:     createAutomateSessionMutation,
			Variables: map[string]interface{}{"input": sessionInput},
			OpName:    "CreateAutomateSession",
		}, resp)
		if err != nil {
			return nil, StartAutomateOutput{}, fmt.Errorf(
				"failed to create automate session: %w", err,
			)
		}

		data, ok := resp.Data.(*createAutomateSessionData)
		if !ok || data.CreateAutomateSession == nil || data.CreateAutomateSession.Session == nil {
			return nil, StartAutomateOutput{}, fmt.Errorf(
				"unexpected response from CreateAutomateSession",
			)
		}

		sessionID := data.CreateAutomateSession.Session.Id
		sessionName := data.CreateAutomateSession.Session.Name

		// Start the automate task
		taskResp, err := client.Automate.StartTask(ctx, sessionID)
		if err != nil {
			return nil, StartAutomateOutput{}, fmt.Errorf(
				"session created (%s) but failed to start task: %w",
				sessionID, err,
			)
		}

		taskID := ""
		if taskResp.StartAutomateTask.AutomateTask != nil {
			taskID = taskResp.StartAutomateTask.AutomateTask.Id
		}

		return nil, StartAutomateOutput{
			SessionID: sessionID,
			TaskID:    taskID,
			Name: sessionName,
		}, nil
	}
}

// RegisterStartAutomateTool registers the tool with the MCP server
func RegisterStartAutomateTool(
	server *mcp.Server, client *caido.Client,
) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "caido_start_automate",
		Description: `Create an automate (fuzzing) session from a captured request and start the task. Useful for race condition testing and parallel request sending. Returns sessionId and taskId. Follow up with caido_get_automate_session or caido_get_automate_entry for results. Params: requestId (required).`,
	}, startAutomateHandler(client))
}
