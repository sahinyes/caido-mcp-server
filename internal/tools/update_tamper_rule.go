package tools

import (
	"context"
	"fmt"

	gql "github.com/Khan/genqlient/graphql"
	caido "github.com/caido-community/sdk-go"
	gen "github.com/caido-community/sdk-go/graphql"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// updateTamperRuleVars is the raw GraphQL variable wrapper.
// Same oneof workaround as create_tamper_rule: TamperSectionInput is a
// @oneOf, so we use map[string]any to avoid null unset variants.
type updateTamperRuleVars struct {
	ID    string                 `json:"id"`
	Input updateTamperRuleGQLIn  `json:"input"`
}

type updateTamperRuleGQLIn struct {
	Name      string         `json:"name"`
	Section   map[string]any `json:"section"`
	Condition *string        `json:"condition,omitempty"`
	Sources   []gen.Source   `json:"sources"`
}

type updateTamperRuleResp struct {
	UpdateTamperRule struct {
		Rule *struct {
			Id   string `json:"id"`
			Name string `json:"name"`
		} `json:"rule"`
		Error *struct {
			Typename string `json:"__typename"`
		} `json:"error"`
	} `json:"updateTamperRule"`
}

const updateTamperRuleMutation = `
mutation UpdateTamperRule($id: ID!, $input: UpdateTamperRuleInput!) {
	updateTamperRule(id: $id, input: $input) {
		error { __typename }
		rule { id name }
	}
}`

// UpdateTamperRuleInput is the input for the update_tamper_rule tool
type UpdateTamperRuleInput struct {
	ID        string   `json:"id" jsonschema:"required,ID of the tamper rule to update"`
	Name      string   `json:"name" jsonschema:"required,New name for the rule"`
	Section   string   `json:"section" jsonschema:"required,Section: requestAll requestHeader requestBody requestPath requestQuery requestMethod requestFirstLine requestSNI responseAll responseHeader responseBody responseFirstLine responseStatusCode"`
	Match     string   `json:"match,omitempty" jsonschema:"Regex pattern to match"`
	Replace   string   `json:"replace,omitempty" jsonschema:"Replacement string"`
	Condition *string  `json:"condition,omitempty" jsonschema:"HTTPQL filter condition"`
	Sources   []string `json:"sources,omitempty" jsonschema:"Traffic sources: INTERCEPT REPLAY AUTOMATE IMPORT PLUGIN WORKFLOW SAMPLE"`
}

// UpdateTamperRuleOutput is the output of the update_tamper_rule tool
type UpdateTamperRuleOutput struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func updateTamperRuleHandler(
	client *caido.Client,
) func(context.Context, *mcp.CallToolRequest, UpdateTamperRuleInput) (*mcp.CallToolResult, UpdateTamperRuleOutput, error) {
	return func(
		ctx context.Context,
		req *mcp.CallToolRequest,
		input UpdateTamperRuleInput,
	) (*mcp.CallToolResult, UpdateTamperRuleOutput, error) {
		if input.ID == "" {
			return nil, UpdateTamperRuleOutput{}, fmt.Errorf("id is required")
		}
		if input.Name == "" {
			return nil, UpdateTamperRuleOutput{}, fmt.Errorf("name is required")
		}
		if len(input.Name) > 200 {
			return nil, UpdateTamperRuleOutput{}, fmt.Errorf(
				"name exceeds max length of 200",
			)
		}
		if input.Condition != nil && len(*input.Condition) > 10000 {
			return nil, UpdateTamperRuleOutput{}, fmt.Errorf(
				"condition exceeds max length of 10000",
			)
		}

		section, err := buildTamperSectionMap(
			input.Section, input.Match, input.Replace,
		)
		if err != nil {
			return nil, UpdateTamperRuleOutput{}, err
		}

		sources := make([]gen.Source, 0, len(input.Sources))
		for _, s := range input.Sources {
			sources = append(sources, gen.Source(s))
		}

		vars := &updateTamperRuleVars{
			ID: input.ID,
			Input: updateTamperRuleGQLIn{
				Name:      input.Name,
				Section:   section,
				Condition: input.Condition,
				Sources:   sources,
			},
		}

		gqlReq := &gql.Request{
			OpName:    "UpdateTamperRule",
			Query:     updateTamperRuleMutation,
			Variables: vars,
		}
		data := &updateTamperRuleResp{}
		gqlResp := &gql.Response{Data: data}
		if err := client.GraphQL.MakeRequest(
			ctx, gqlReq, gqlResp,
		); err != nil {
			return nil, UpdateTamperRuleOutput{}, err
		}

		payload := data.UpdateTamperRule
		if payload.Error != nil {
			return nil, UpdateTamperRuleOutput{}, fmt.Errorf(
				"update tamper rule failed: %s",
				payload.Error.Typename,
			)
		}
		if payload.Rule == nil {
			return nil, UpdateTamperRuleOutput{}, fmt.Errorf(
				"update tamper rule returned no rule",
			)
		}

		return nil, UpdateTamperRuleOutput{
			ID:   payload.Rule.Id,
			Name: payload.Rule.Name,
		}, nil
	}
}

// RegisterUpdateTamperRuleTool registers the tool with the MCP server
func RegisterUpdateTamperRuleTool(
	server *mcp.Server, client *caido.Client,
) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "caido_update_tamper_rule",
		Description: `Update an existing Match & Replace (tamper) rule. ` +
			`Params: id (required), name (required), section (required), ` +
			`match (regex), replace (string), condition (HTTPQL), sources.`,
	}, updateTamperRuleHandler(client))
}
