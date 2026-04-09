package tools

import (
	"context"
	"fmt"

	caido "github.com/caido-community/sdk-go"
	gen "github.com/caido-community/sdk-go/graphql"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// CreateTamperRuleInput is the input for the create_tamper_rule tool
type CreateTamperRuleInput struct {
	CollectionID string   `json:"collection_id" jsonschema:"required,ID of the tamper rule collection"`
	Name         string   `json:"name" jsonschema:"required,Name for the new rule"`
	Section      string   `json:"section" jsonschema:"required,Section to match: requestAll requestHeader requestBody requestPath requestQuery requestMethod requestFirstLine requestSNI responseAll responseHeader responseBody responseFirstLine responseStatusCode"`
	Match        string   `json:"match,omitempty" jsonschema:"Regex pattern to match"`
	Replace      string   `json:"replace,omitempty" jsonschema:"Replacement string"`
	Condition    *string  `json:"condition,omitempty" jsonschema:"HTTPQL filter condition"`
	Sources      []string `json:"sources,omitempty" jsonschema:"Traffic sources: INTERCEPT REPLAY AUTOMATE IMPORT PLUGIN WORKFLOW SAMPLE"`
}

// CreateTamperRuleOutput is the output of the create_tamper_rule tool
type CreateTamperRuleOutput struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// createTamperRuleHandler creates the handler function
func createTamperRuleHandler(
	client *caido.Client,
) func(context.Context, *mcp.CallToolRequest, CreateTamperRuleInput) (*mcp.CallToolResult, CreateTamperRuleOutput, error) {
	return func(
		ctx context.Context,
		req *mcp.CallToolRequest,
		input CreateTamperRuleInput,
	) (*mcp.CallToolResult, CreateTamperRuleOutput, error) {
		if len(input.Name) > 200 {
			return nil, CreateTamperRuleOutput{}, fmt.Errorf(
				"name exceeds max length of 200",
			)
		}
		if input.Condition != nil && len(*input.Condition) > 10000 {
			return nil, CreateTamperRuleOutput{}, fmt.Errorf(
				"condition exceeds max length of 10000",
			)
		}

		section, err := buildTamperSection(
			input.Section, input.Match, input.Replace,
		)
		if err != nil {
			return nil, CreateTamperRuleOutput{}, err
		}

		sources := make([]gen.Source, 0, len(input.Sources))
		for _, s := range input.Sources {
			sources = append(sources, gen.Source(s))
		}

		gqlInput := &gen.CreateTamperRuleInput{
			CollectionId: input.CollectionID,
			Name:         input.Name,
			Section:      section,
			Condition:    input.Condition,
			Sources:      sources,
		}

		resp, err := client.Tamper.CreateRule(ctx, gqlInput)
		if err != nil {
			return nil, CreateTamperRuleOutput{}, err
		}

		payload := resp.CreateTamperRule
		if payload.Error != nil {
			errType := "unknown"
			if tn := (*payload.Error).GetTypename(); tn != nil {
				errType = *tn
			}
			return nil, CreateTamperRuleOutput{}, fmt.Errorf(
				"create tamper rule failed: %s", errType,
			)
		}

		if payload.Rule == nil {
			return nil, CreateTamperRuleOutput{}, fmt.Errorf(
				"create tamper rule returned no rule",
			)
		}

		return nil, CreateTamperRuleOutput{
			ID:   payload.Rule.Id,
			Name: payload.Rule.Name,
		}, nil
	}
}

// buildTamperSection constructs a TamperSectionInput from the
// section name and optional match/replace strings.
func buildTamperSection(
	section, match, replace string,
) (gen.TamperSectionInput, error) {
	matcher := gen.TamperMatcherRawInput{
		Regex: &gen.TamperMatcherRegexInput{Regex: match},
	}
	replacer := gen.TamperReplacerInput{
		Term: &gen.TamperReplacerTermInput{Term: replace},
	}

	rawOp := func() *gen.TamperOperationAllRawInput {
		return &gen.TamperOperationAllRawInput{
			Matcher: matcher, Replacer: replacer,
		}
	}
	bodyOp := func() *gen.TamperOperationBodyRawInput {
		return &gen.TamperOperationBodyRawInput{
			Matcher: matcher, Replacer: replacer,
		}
	}
	headerOp := func() *gen.TamperOperationHeaderRawInput {
		return &gen.TamperOperationHeaderRawInput{
			Matcher: matcher, Replacer: replacer,
		}
	}

	var s gen.TamperSectionInput
	switch section {
	case "requestAll":
		s.RequestAll = &gen.TamperSectionRequestAllInput{
			Operation: gen.TamperOperationAllInput{Raw: rawOp()},
		}
	case "requestHeader":
		s.RequestHeader = &gen.TamperSectionRequestHeaderInput{
			Operation: gen.TamperOperationHeaderInput{Raw: headerOp()},
		}
	case "requestBody":
		s.RequestBody = &gen.TamperSectionRequestBodyInput{
			Operation: gen.TamperOperationBodyInput{Raw: bodyOp()},
		}
	case "requestPath":
		s.RequestPath = &gen.TamperSectionRequestPathInput{}
	case "requestQuery":
		s.RequestQuery = &gen.TamperSectionRequestQueryInput{}
	case "requestMethod":
		s.RequestMethod = &gen.TamperSectionRequestMethodInput{}
	case "requestFirstLine":
		s.RequestFirstLine = &gen.TamperSectionRequestFirstLineInput{}
	case "requestSNI":
		s.RequestSNI = &gen.TamperSectionRequestSNIInput{}
	case "responseAll":
		s.ResponseAll = &gen.TamperSectionResponseAllInput{
			Operation: gen.TamperOperationAllInput{Raw: rawOp()},
		}
	case "responseHeader":
		s.ResponseHeader = &gen.TamperSectionResponseHeaderInput{
			Operation: gen.TamperOperationHeaderInput{Raw: headerOp()},
		}
	case "responseBody":
		s.ResponseBody = &gen.TamperSectionResponseBodyInput{
			Operation: gen.TamperOperationBodyInput{Raw: bodyOp()},
		}
	case "responseFirstLine":
		s.ResponseFirstLine = &gen.TamperSectionResponseFirstLineInput{}
	case "responseStatusCode":
		s.ResponseStatusCode = &gen.TamperSectionResponseStatusCodeInput{}
	default:
		return s, fmt.Errorf(
			"unknown section %q: use requestAll, requestHeader, "+
				"requestBody, responseAll, responseHeader, responseBody, "+
				"or other supported sections", section,
		)
	}
	return s, nil
}

// RegisterCreateTamperRuleTool registers the tool
func RegisterCreateTamperRuleTool(
	server *mcp.Server, client *caido.Client,
) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "caido_create_tamper_rule",
		Description: `Create a Match & Replace (tamper) rule. ` +
			`Params: collection_id (required), name (required), ` +
			`section (required: requestAll/requestHeader/requestBody/` +
			`responseAll/responseHeader/responseBody/etc), ` +
			`match (regex), replace (string), ` +
			`condition (HTTPQL filter), sources (traffic sources).`,
	}, createTamperRuleHandler(client))
}
