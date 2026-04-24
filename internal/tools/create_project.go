package tools

import (
	"context"
	"fmt"

	caido "github.com/caido-community/sdk-go"
	gen "github.com/caido-community/sdk-go/graphql"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// CreateProjectInput is the input for the create_project tool
type CreateProjectInput struct {
	Name string `json:"name" jsonschema:"required,Name of the new project"`
}

// CreateProjectOutput is the output of the create_project tool
type CreateProjectOutput struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func createProjectHandler(
	client *caido.Client,
) func(context.Context, *mcp.CallToolRequest, CreateProjectInput) (*mcp.CallToolResult, CreateProjectOutput, error) {
	return func(
		ctx context.Context,
		req *mcp.CallToolRequest,
		input CreateProjectInput,
	) (*mcp.CallToolResult, CreateProjectOutput, error) {
		if input.Name == "" {
			return nil, CreateProjectOutput{}, fmt.Errorf("name is required")
		}
		if len(input.Name) > 200 {
			return nil, CreateProjectOutput{}, fmt.Errorf(
				"name exceeds max length of 200",
			)
		}

		resp, err := client.Projects.Create(
			ctx, &gen.CreateProjectInput{Name: input.Name},
		)
		if err != nil {
			return nil, CreateProjectOutput{}, fmt.Errorf(
				"failed to create project: %w", err,
			)
		}

		payload := resp.CreateProject
		if payload.Project == nil {
			return nil, CreateProjectOutput{}, fmt.Errorf(
				"create project returned no project",
			)
		}

		return nil, CreateProjectOutput{
			ID:   payload.Project.Id,
			Name: payload.Project.Name,
		}, nil
	}
}

// RegisterCreateProjectTool registers the tool with the MCP server
func RegisterCreateProjectTool(
	server *mcp.Server, client *caido.Client,
) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "caido_create_project",
		Description: `Create a new Caido project. Returns id and name. Use caido_select_project to switch to it.`,
	}, createProjectHandler(client))
}
