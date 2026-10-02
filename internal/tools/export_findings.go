package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	gql "github.com/Khan/genqlient/graphql"
	caido "github.com/caido-community/sdk-go"
	gen "github.com/caido-community/sdk-go/graphql"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type exportFindingsByIDsVars struct {
	Input *exportFindingsByIDsInput `json:"input"`
}

type exportFindingsByIDsInput struct {
	Ids []string `json:"ids"`
}

type exportFindingsByFilterVars struct {
	Input *exportFindingsByFilterInput `json:"input"`
}

type exportFindingsByFilterInput struct {
	Filter *exportFindingsFilter `json:"filter"`
}

type exportFindingsFilter struct {
	Reporter string `json:"reporter"`
}

type exportFindingsPayload struct {
	Export *struct {
		Id string `json:"id"`
	} `json:"export"`
	Error *struct {
		Typename string `json:"__typename"`
	} `json:"error"`
}

type exportFindingsResp struct {
	ExportFindings *exportFindingsPayload `json:"exportFindings"`
}

const exportFindingsMutation = `
mutation ExportFindings ($input: ExportFindingsInput!) {
	exportFindings(input: $input) {
		export { id }
		error { __typename ... on OtherUserError { code } }
	}
}`

func exportFindingsRaw(
	ctx context.Context,
	gqlClient gql.Client,
	ids []string,
	reporter string,
) (*exportFindingsResp, error) {
	var vars any
	if len(ids) > 0 {
		vars = &exportFindingsByIDsVars{
			Input: &exportFindingsByIDsInput{Ids: ids},
		}
	} else {
		vars = &exportFindingsByFilterVars{
			Input: &exportFindingsByFilterInput{
				Filter: &exportFindingsFilter{Reporter: reporter},
			},
		}
	}

	req := &gql.Request{
		OpName:    "ExportFindings",
		Query:     exportFindingsMutation,
		Variables: vars,
	}
	data := &exportFindingsResp{}
	resp := &gql.Response{Data: data}
	if err := gqlClient.MakeRequest(ctx, req, resp); err != nil {
		return nil, err
	}
	return data, nil
}

// ExportFindingsInput is the input for the tool
type ExportFindingsInput struct {
	IDs      []string `json:"ids,omitempty" jsonschema:"List of finding IDs to export"`
	Reporter string   `json:"reporter,omitempty" jsonschema:"Export all findings by this reporter name"`
	Format   string   `json:"format,omitempty" jsonschema:"Output format: json, markdown, csv (returns content inline). Omit to get native exportId."`
}

// ExportFindingsOutput is the output
type ExportFindingsOutput struct {
	ExportID string `json:"exportId,omitempty"`
	Content  string `json:"content,omitempty"`
	Format   string `json:"format,omitempty"`
	// Warning names what the export could NOT deliver: requested ids that no
	// page contained, or a page cap reached while findings remained. A
	// formatted export that silently returns fewer findings than were asked
	// for is indistinguishable from "those findings do not exist".
	Warning string `json:"warning,omitempty"`
}

func exportFindingsHandler(
	client *caido.Client,
) func(context.Context, *mcp.CallToolRequest, ExportFindingsInput) (*mcp.CallToolResult, ExportFindingsOutput, error) {
	return func(
		ctx context.Context,
		req *mcp.CallToolRequest,
		input ExportFindingsInput,
	) (*mcp.CallToolResult, ExportFindingsOutput, error) {
		if len(input.IDs) == 0 && input.Reporter == "" {
			return nil, ExportFindingsOutput{}, fmt.Errorf(
				"provide either ids or reporter",
			)
		}

		format := strings.ToLower(input.Format)
		if format == "json" || format == "markdown" || format == "csv" {
			content, warning, err := exportFindingsFormatted(
				ctx, client, input.IDs, input.Reporter, format,
			)
			if err != nil {
				return nil, ExportFindingsOutput{}, err
			}
			return nil, ExportFindingsOutput{
				Content: content,
				Format:  format,
				Warning: warning,
			}, nil
		}

		resp, err := exportFindingsRaw(
			ctx, client.GraphQL, input.IDs, input.Reporter,
		)
		if err != nil {
			return nil, ExportFindingsOutput{}, err
		}

		payload := resp.ExportFindings
		if payload == nil {
			return nil, ExportFindingsOutput{}, fmt.Errorf(
				"export findings returned no payload",
			)
		}
		if payload.Error != nil {
			return nil, ExportFindingsOutput{}, fmt.Errorf(
				"export findings failed: %s",
				payload.Error.Typename,
			)
		}
		if payload.Export == nil {
			return nil, ExportFindingsOutput{}, fmt.Errorf(
				"export findings returned no export",
			)
		}

		return nil, ExportFindingsOutput{
			ExportID: payload.Export.Id,
		}, nil
	}
}

type exportedFinding struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Host        string  `json:"host"`
	Path        string  `json:"path"`
	Reporter    string  `json:"reporter"`
	CreatedAt   string  `json:"created_at"`
	RequestID   string  `json:"request_id,omitempty"`
	Description *string `json:"description,omitempty"`
}

const (
	// exportPageSize is findings per page.
	exportPageSize = 100
	// exportMaxPages bounds the walk. It is a stop, not a silent cap: hitting
	// it is reported in the output.
	exportMaxPages = 50
)

// exportFindingsFormatted collects findings for the inline formats.
//
// It FOLLOWS pagination. The first version asked for one page of 100 and then
// filtered client-side by id, so a request for three ids that happened to sit
// on page 2 came back empty and well-formed - the agent reads that as "no such
// findings", and the findings list grows past 100 in a week of real use. It
// also returns a warning rather than quietly delivering less than was asked
// for.
func exportFindingsFormatted(
	ctx context.Context,
	client *caido.Client,
	ids []string,
	reporter string,
	format string,
) (string, string, error) {
	idSet := make(map[string]bool, len(ids))
	for _, id := range ids {
		idSet[id] = true
	}

	limit := exportPageSize
	var cursor *string
	var findings []exportedFinding
	found := make(map[string]bool, len(idSet))
	truncated := false

	for page := 0; ; page++ {
		if page >= exportMaxPages {
			truncated = true
			break
		}
		opts := &caido.ListFindingsOptions{First: &limit, After: cursor}
		if reporter != "" {
			opts.Filter = &gen.FilterClauseFindingInput{
				Reporter: &reporter,
			}
		}

		resp, err := client.Findings.List(ctx, opts)
		if err != nil {
			return "", "", fmt.Errorf("failed to list findings: %w", err)
		}

		for _, edge := range resp.Findings.Edges {
			f := edge.Node
			if len(idSet) > 0 && !idSet[f.Id] {
				continue
			}
			found[f.Id] = true
			findings = append(findings, exportedFinding{
				ID:          f.Id,
				Title:       f.Title,
				Host:        f.Host,
				Path:        f.Path,
				Reporter:    f.Reporter,
				CreatedAt:   time.UnixMilli(f.CreatedAt).Format(time.RFC3339),
				RequestID:   f.Request.Id,
				Description: f.Description,
			})
		}

		// Every requested id is in hand: no reason to walk the rest.
		if len(idSet) > 0 && len(found) == len(idSet) {
			break
		}
		if !resp.Findings.PageInfo.HasNextPage ||
			resp.Findings.PageInfo.EndCursor == nil {
			break
		}
		cursor = resp.Findings.PageInfo.EndCursor
	}

	var warnings []string
	if len(idSet) > 0 && len(found) < len(idSet) {
		missing := make([]string, 0, len(idSet)-len(found))
		for _, id := range ids {
			if !found[id] {
				missing = append(missing, id)
			}
		}
		warnings = append(warnings, fmt.Sprintf(
			"%d of %d requested findings were not found: %s",
			len(missing), len(idSet), strings.Join(missing, ", "),
		))
	}
	if truncated {
		warnings = append(warnings, fmt.Sprintf(
			"stopped after %d pages of %d; more findings exist",
			exportMaxPages, exportPageSize,
		))
	}
	warning := strings.Join(warnings, "; ")

	switch format {
	case "json":
		b, err := json.MarshalIndent(findings, "", "  ")
		if err != nil {
			return "", warning, err
		}
		return string(b), warning, nil

	case "markdown":
		var sb strings.Builder
		sb.WriteString("# Findings Export\n\n")
		for _, f := range findings {
			sb.WriteString(fmt.Sprintf("## %s\n", f.Title))
			sb.WriteString(fmt.Sprintf("- **Host:** %s\n", f.Host))
			sb.WriteString(fmt.Sprintf("- **Path:** %s\n", f.Path))
			sb.WriteString(fmt.Sprintf("- **Reporter:** %s\n", f.Reporter))
			sb.WriteString(fmt.Sprintf("- **Created:** %s\n", f.CreatedAt))
			sb.WriteString(fmt.Sprintf("- **Request ID:** %s\n", f.RequestID))
			if f.Description != nil {
				sb.WriteString(fmt.Sprintf("\n%s\n", *f.Description))
			}
			sb.WriteString("\n---\n\n")
		}
		return sb.String(), warning, nil

	case "csv":
		var sb strings.Builder
		sb.WriteString("id,title,host,path,reporter,created_at,request_id,description\n")
		for _, f := range findings {
			desc := ""
			if f.Description != nil {
				desc = strings.ReplaceAll(*f.Description, `"`, `""`)
			}
			sb.WriteString(fmt.Sprintf(
				"%q,%q,%q,%q,%q,%q,%q,%q\n",
				f.ID, f.Title, f.Host, f.Path,
				f.Reporter, f.CreatedAt, f.RequestID, desc,
			))
		}
		return sb.String(), warning, nil
	}

	return "", "", fmt.Errorf("unsupported format: %s", format)
}

// RegisterExportFindingsTool registers the tool
func RegisterExportFindingsTool(
	server *mcp.Server, client *caido.Client,
) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "caido_export_findings",
		Description: `Export findings. Filter by IDs or reporter name. Returns exportId for download.`,
	}, exportFindingsHandler(client))
}
