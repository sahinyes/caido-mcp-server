package tools

import (
	"context"
	"fmt"
	gql "github.com/Khan/genqlient/graphql"
	caido "github.com/caido-community/sdk-go"
	gen "github.com/caido-community/sdk-go/graphql"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const setupPrefix = "[mcp-setup] "

// SetupProgramInput is the input for the setup_program tool
type SetupProgramInput struct {
	ProjectName     string            `json:"projectName" jsonschema:"required,Project name (created if not exists; selected if exists)"`
	ScopeInclude    []string          `json:"scopeInclude" jsonschema:"required,Allowlist patterns (e.g. *.example.com)"`
	ScopeExclude    []string          `json:"scopeExclude,omitempty" jsonschema:"Denylist patterns"`
	RequiredHeaders map[string]string `json:"requiredHeaders,omitempty" jsonschema:"Headers to inject on all in-scope requests (e.g. {X-Bug-Bounty: program=example})"`
	ReplaceExisting *bool             `json:"replaceExisting,omitempty" jsonschema:"Update existing rules instead of duplicating (default true)"`
}

// SetupProgramStepResult reports created/updated status of a single step
type SetupProgramStepResult struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Created bool   `json:"created"`
}

// SetupProgramOutput is the output of the setup_program tool
type SetupProgramOutput struct {
	Project     SetupProgramStepResult   `json:"project"`
	Scope       SetupProgramStepResult   `json:"scope"`
	TamperRules []SetupProgramStepResult `json:"tamperRules"`
	Notes       []string                 `json:"notes,omitempty"`
	Summary     string                   `json:"summary"`
}

func setupProgramHandler(
	client *caido.Client,
) func(context.Context, *mcp.CallToolRequest, SetupProgramInput) (*mcp.CallToolResult, SetupProgramOutput, error) {
	return func(
		ctx context.Context,
		req *mcp.CallToolRequest,
		input SetupProgramInput,
	) (*mcp.CallToolResult, SetupProgramOutput, error) {
		if input.ProjectName == "" {
			return nil, SetupProgramOutput{}, fmt.Errorf("projectName is required")
		}
		if len(input.ScopeInclude) == 0 {
			return nil, SetupProgramOutput{}, fmt.Errorf("scopeInclude is required")
		}
		replaceExisting := true
		if input.ReplaceExisting != nil {
			replaceExisting = *input.ReplaceExisting
		}

		output := SetupProgramOutput{
			TamperRules: []SetupProgramStepResult{},
		}
		var notes []string

		// Step 1: find or create project
		projectResult, err := findOrCreateProject(
			ctx, client, input.ProjectName,
		)
		if err != nil {
			return nil, SetupProgramOutput{}, fmt.Errorf(
				"project step failed: %w", err,
			)
		}
		output.Project = projectResult

		// Step 2: select project
		if _, err := client.Projects.Select(ctx, projectResult.ID); err != nil {
			return nil, SetupProgramOutput{}, fmt.Errorf(
				"select project failed: %w", err,
			)
		}

		// Step 3: find or create scope (idempotent)
		scopeName := setupPrefix + input.ProjectName
		denylist := input.ScopeExclude
		if denylist == nil {
			denylist = []string{}
		}
		scopeResult, err := findOrCreateScope(
			ctx, client, scopeName, input.ScopeInclude, denylist,
		)
		if err != nil {
			return nil, SetupProgramOutput{}, fmt.Errorf(
				"scope step failed: %w", err,
			)
		}
		output.Scope = scopeResult

		// Step 4: get or create tamper collection for mcp-setup rules
		collectionID, err := findOrCreateTamperCollection(ctx, client)
		if err != nil {
			return nil, SetupProgramOutput{}, fmt.Errorf(
				"tamper collection step failed: %w", err,
			)
		}

		// Step 5: create/update tamper rules for required headers
		for headerName, headerValue := range input.RequiredHeaders {
			ruleName := setupPrefix + headerName
			ruleResult, err := applyHeaderTamperRule(
				ctx, client, collectionID,
				ruleName, headerName, headerValue,
				replaceExisting,
			)
			if err != nil {
				notes = append(notes, fmt.Sprintf(
					"tamper rule for %s failed: %v", headerName, err,
				))
				continue
			}
			output.TamperRules = append(output.TamperRules, ruleResult)
		}

		// Rate limit note (not supported via tamper rules)
		notes = append(notes,
			"rate_limit_rps is not supported via tamper rules — "+
				"configure delay in Automate session settings instead",
		)

		output.Notes = notes
		output.Summary = fmt.Sprintf(
			"project %q selected (%s), scope %s, %d tamper rule(s) applied",
			input.ProjectName,
			map[bool]string{true: "new", false: "existing"}[output.Project.Created],
			map[bool]string{true: "created", false: "already existed"}[output.Scope.Created],
			len(output.TamperRules),
		)

		return nil, output, nil
	}
}

func findOrCreateProject(
	ctx context.Context, client *caido.Client, name string,
) (SetupProgramStepResult, error) {
	listResp, err := client.Projects.List(ctx)
	if err != nil {
		return SetupProgramStepResult{}, err
	}

	for _, p := range listResp.Projects {
		if p.Name == name {
			return SetupProgramStepResult{
				ID:      p.Id,
				Name:    p.Name,
				Created: false,
			}, nil
		}
	}

	createResp, err := client.Projects.Create(
		ctx, &gen.CreateProjectInput{Name: name},
	)
	if err != nil {
		return SetupProgramStepResult{}, err
	}
	if createResp.CreateProject.Project == nil {
		return SetupProgramStepResult{}, fmt.Errorf(
			"create project returned no project",
		)
	}
	p := createResp.CreateProject.Project
	return SetupProgramStepResult{
		ID:      p.Id,
		Name:    p.Name,
		Created: true,
	}, nil
}

func findOrCreateScope(
	ctx context.Context,
	client *caido.Client,
	name string,
	allowlist, denylist []string,
) (SetupProgramStepResult, error) {
	listResp, err := client.Scopes.List(ctx)
	if err != nil {
		return SetupProgramStepResult{}, err
	}
	for _, s := range listResp.Scopes {
		if s.Name == name {
			return SetupProgramStepResult{
				ID:      s.Id,
				Name:    s.Name,
				Created: false,
			}, nil
		}
	}

	createResp, err := client.Scopes.Create(ctx, &gen.CreateScopeInput{
		Name:      name,
		Allowlist: allowlist,
		Denylist:  denylist,
	})
	if err != nil {
		return SetupProgramStepResult{}, err
	}
	if createResp.CreateScope.Scope == nil {
		return SetupProgramStepResult{}, fmt.Errorf("create scope returned no scope")
	}
	s := createResp.CreateScope.Scope
	return SetupProgramStepResult{
		ID:      s.Id,
		Name:    s.Name,
		Created: true,
	}, nil
}

func findOrCreateTamperCollection(
	ctx context.Context, client *caido.Client,
) (string, error) {
	listResp, err := client.Tamper.ListCollections(ctx)
	if err != nil {
		return "", err
	}

	collectionName := setupPrefix + "rules"
	for _, c := range listResp.TamperRuleCollections {
		if c.Name == collectionName {
			return c.Id, nil
		}
	}

	// Use first available collection if mcp-setup one doesn't exist
	if len(listResp.TamperRuleCollections) > 0 {
		return listResp.TamperRuleCollections[0].Id, nil
	}

	// Create a new collection
	createResp, err := client.Tamper.CreateCollection(
		ctx, &gen.CreateTamperRuleCollectionInput{Name: collectionName},
	)
	if err != nil {
		return "", err
	}
	if createResp.CreateTamperRuleCollection.Collection == nil {
		return "", fmt.Errorf("create tamper collection returned no collection")
	}
	return createResp.CreateTamperRuleCollection.Collection.Id, nil
}

func applyHeaderTamperRule(
	ctx context.Context,
	client *caido.Client,
	collectionID string,
	ruleName, headerName, headerValue string,
	replaceExisting bool,
) (SetupProgramStepResult, error) {
	match := fmt.Sprintf(`(?i)^%s:.*`, headerName)
	replace := fmt.Sprintf("%s: %s", headerName, headerValue)

	// Check for existing rule with this name
	if replaceExisting {
		listResp, err := client.Tamper.ListCollections(ctx)
		if err == nil {
			for _, c := range listResp.TamperRuleCollections {
				for _, r := range c.Rules {
					if r.Name == ruleName {
						updated, err := rawUpdateTamperRule(
							ctx, client, r.Id, ruleName,
							"requestHeader", match, replace,
						)
						if err != nil {
							return SetupProgramStepResult{}, err
						}
						return SetupProgramStepResult{
							ID:      updated.ID,
							Name:    updated.Name,
							Created: false,
						}, nil
					}
				}
			}
		}
	}

	// Create new rule
	section, err := buildTamperSectionMap("requestHeader", match, replace)
	if err != nil {
		return SetupProgramStepResult{}, err
	}

	sources := []gen.Source{"INTERCEPT", "AUTOMATE"}
	vars := &createTamperRuleVars{
		Input: createTamperRuleGQLInput{
			CollectionId: collectionID,
			Name:         ruleName,
			Section:      section,
			Sources:      sources,
		},
	}
	gqlReq := &gql.Request{
		OpName:    "CreateTamperRule",
		Query:     createTamperRuleMutation,
		Variables: vars,
	}
	data := &createTamperRuleResp{}
	gqlResp := &gql.Response{Data: data}
	if err := client.GraphQL.MakeRequest(ctx, gqlReq, gqlResp); err != nil {
		return SetupProgramStepResult{}, err
	}
	payload := data.CreateTamperRule
	if payload.Error != nil {
		return SetupProgramStepResult{}, fmt.Errorf(
			"create tamper rule failed: %s", payload.Error.Typename,
		)
	}
	if payload.Rule == nil {
		return SetupProgramStepResult{}, fmt.Errorf(
			"create tamper rule returned no rule",
		)
	}
	return SetupProgramStepResult{
		ID:      payload.Rule.Id,
		Name:    payload.Rule.Name,
		Created: true,
	}, nil
}

func rawUpdateTamperRule(
	ctx context.Context,
	client *caido.Client,
	id, name, sectionKey, match, replace string,
) (UpdateTamperRuleOutput, error) {
	section, err := buildTamperSectionMap(sectionKey, match, replace)
	if err != nil {
		return UpdateTamperRuleOutput{}, err
	}

	sources := []gen.Source{"INTERCEPT", "AUTOMATE"}
	vars := &updateTamperRuleVars{
		ID: id,
		Input: updateTamperRuleGQLIn{
			Name:    name,
			Section: section,
			Sources: sources,
		},
	}
	gqlReq := &gql.Request{
		OpName:    "UpdateTamperRule",
		Query:     updateTamperRuleMutation,
		Variables: vars,
	}
	data := &updateTamperRuleResp{}
	gqlResp := &gql.Response{Data: data}
	if err := client.GraphQL.MakeRequest(ctx, gqlReq, gqlResp); err != nil {
		return UpdateTamperRuleOutput{}, err
	}
	payload := data.UpdateTamperRule
	if payload.Error != nil {
		return UpdateTamperRuleOutput{}, fmt.Errorf(
			"update tamper rule failed: %s", payload.Error.Typename,
		)
	}
	if payload.Rule == nil {
		return UpdateTamperRuleOutput{}, fmt.Errorf(
			"update tamper rule returned no rule",
		)
	}
	return UpdateTamperRuleOutput{
		ID:   payload.Rule.Id,
		Name: payload.Rule.Name,
	}, nil
}

// RegisterSetupProgramTool registers the tool with the MCP server
func RegisterSetupProgramTool(
	server *mcp.Server, client *caido.Client,
) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "caido_setup_program",
		Description: `One-call program initialization. Finds or creates project, ` +
			`creates scope, and sets up required header tamper rules. Idempotent ` +
			`— safe to re-run at session start. requiredHeaders example: ` +
			`{"X-Bug-Bounty": "program=example,user=yourname"}.`,
	}, setupProgramHandler(client))
}
