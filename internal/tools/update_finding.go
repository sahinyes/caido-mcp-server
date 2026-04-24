package tools

import (
	"context"
	"fmt"

	gql "github.com/Khan/genqlient/graphql"
	caido "github.com/caido-community/sdk-go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type updateFindingVars struct {
	ID    string              `json:"id"`
	Input updateFindingGQLIn  `json:"input"`
}

type updateFindingGQLIn struct {
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
	Hidden      *bool   `json:"hidden,omitempty"`
}

type updateFindingResp struct {
	UpdateFinding struct {
		Finding *struct {
			Id    string  `json:"id"`
			Title string  `json:"title"`
			Host  string  `json:"host"`
		} `json:"finding"`
		Error *struct {
			Typename string `json:"__typename"`
		} `json:"error"`
	} `json:"updateFinding"`
}

const updateFindingMutation = `
mutation UpdateFinding($id: ID!, $input: UpdateFindingInput!) {
	updateFinding(id: $id, input: $input) {
		error { __typename }
		finding { id title host }
	}
}`

// UpdateFindingInput is the input for the update_finding tool
type UpdateFindingInput struct {
	ID          string  `json:"id" jsonschema:"required,ID of the finding to update"`
	Title       *string `json:"title,omitempty" jsonschema:"New title"`
	Description *string `json:"description,omitempty" jsonschema:"New description"`
	Hidden      *bool   `json:"hidden,omitempty" jsonschema:"Hide or show the finding"`
}

// UpdateFindingOutput is the output of the update_finding tool
type UpdateFindingOutput struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Host  string `json:"host"`
}

func updateFindingHandler(
	client *caido.Client,
) func(context.Context, *mcp.CallToolRequest, UpdateFindingInput) (*mcp.CallToolResult, UpdateFindingOutput, error) {
	return func(
		ctx context.Context,
		req *mcp.CallToolRequest,
		input UpdateFindingInput,
	) (*mcp.CallToolResult, UpdateFindingOutput, error) {
		if input.ID == "" {
			return nil, UpdateFindingOutput{}, fmt.Errorf("id is required")
		}
		if input.Title == nil && input.Description == nil && input.Hidden == nil {
			return nil, UpdateFindingOutput{}, fmt.Errorf(
				"at least one of title, description, or hidden must be set",
			)
		}

		vars := &updateFindingVars{
			ID: input.ID,
			Input: updateFindingGQLIn{
				Title:       input.Title,
				Description: input.Description,
				Hidden:      input.Hidden,
			},
		}

		gqlReq := &gql.Request{
			OpName:    "UpdateFinding",
			Query:     updateFindingMutation,
			Variables: vars,
		}
		data := &updateFindingResp{}
		gqlResp := &gql.Response{Data: data}
		if err := client.GraphQL.MakeRequest(
			ctx, gqlReq, gqlResp,
		); err != nil {
			return nil, UpdateFindingOutput{}, err
		}

		payload := data.UpdateFinding
		if payload.Error != nil {
			return nil, UpdateFindingOutput{}, fmt.Errorf(
				"update finding failed: %s",
				payload.Error.Typename,
			)
		}
		if payload.Finding == nil {
			return nil, UpdateFindingOutput{}, fmt.Errorf(
				"update finding returned no finding",
			)
		}

		return nil, UpdateFindingOutput{
			ID:    payload.Finding.Id,
			Title: payload.Finding.Title,
			Host:  payload.Finding.Host,
		}, nil
	}
}

// RegisterUpdateFindingTool registers the tool with the MCP server
func RegisterUpdateFindingTool(
	server *mcp.Server, client *caido.Client,
) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "caido_update_finding",
		Description: `Update a finding's title, description, or visibility. Note: severity/tags/notes not supported by Caido schema.`,
	}, updateFindingHandler(client))
}
