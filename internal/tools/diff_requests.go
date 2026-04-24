package tools

import (
	"context"
	"fmt"

	caido "github.com/caido-community/sdk-go"
	"github.com/c0tton-fluff/caido-mcp-server/internal/httputil"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// DiffRequestsInput is the input for the diff_requests tool
type DiffRequestsInput struct {
	BaseID   string `json:"baseId" jsonschema:"required,Base request ID (the 'before')"`
	CompID   string `json:"compId" jsonschema:"required,Comparison request ID (the 'after')"`
	Sections string `json:"sections,omitempty" jsonschema:"What to diff: all, headers, body, status (default: all)"`
}

// HeaderDiff represents a difference in headers
type HeaderDiff struct {
	Name    string `json:"name"`
	BaseVal string `json:"baseValue,omitempty"`
	CompVal string `json:"compValue,omitempty"`
	Status  string `json:"status"` // added, removed, changed
}

// DiffResult is the structured diff output
type DiffResult struct {
	BaseID        string       `json:"baseId"`
	CompID        string       `json:"compId"`
	StatusBase    int          `json:"statusCodeBase,omitempty"`
	StatusComp    int          `json:"statusCodeComp,omitempty"`
	StatusChanged bool         `json:"statusChanged"`
	ReqMethodBase string       `json:"requestMethodBase,omitempty"`
	ReqMethodComp string       `json:"requestMethodComp,omitempty"`
	MethodChanged bool         `json:"methodChanged"`
	ReqHeaders    []HeaderDiff `json:"requestHeaderDiffs,omitempty"`
	RespHeaders   []HeaderDiff `json:"responseHeaderDiffs,omitempty"`
	ReqBodyBase   string       `json:"requestBodyBase,omitempty"`
	ReqBodyComp   string       `json:"requestBodyComp,omitempty"`
	ReqBodySame   bool         `json:"requestBodySame"`
	RespBodyBase  string       `json:"responseBodyBase,omitempty"`
	RespBodyComp  string       `json:"responseBodyComp,omitempty"`
	RespBodySame  bool         `json:"responseBodySame"`
	BodyLimitUsed int          `json:"bodyLimitUsed"`
}

// diffRequestsHandler creates the handler function
func diffRequestsHandler(
	client *caido.Client,
) func(context.Context, *mcp.CallToolRequest, DiffRequestsInput) (*mcp.CallToolResult, DiffResult, error) {
	return func(
		ctx context.Context,
		req *mcp.CallToolRequest,
		input DiffRequestsInput,
	) (*mcp.CallToolResult, DiffResult, error) {
		if input.BaseID == "" || input.CompID == "" {
			return nil, DiffResult{}, fmt.Errorf(
				"both baseId and compId are required",
			)
		}

		sections := input.Sections
		if sections == "" {
			sections = "all"
		}

		bodyLimit := 4000

		baseResp, err := client.Requests.Get(ctx, input.BaseID)
		if err != nil {
			return nil, DiffResult{}, fmt.Errorf(
				"failed to get base request %s: %w", input.BaseID, err,
			)
		}
		compResp, err := client.Requests.Get(ctx, input.CompID)
		if err != nil {
			return nil, DiffResult{}, fmt.Errorf(
				"failed to get comp request %s: %w", input.CompID, err,
			)
		}

		if baseResp.Request == nil {
			return nil, DiffResult{}, fmt.Errorf(
				"base request %s not found", input.BaseID,
			)
		}
		if compResp.Request == nil {
			return nil, DiffResult{}, fmt.Errorf(
				"comp request %s not found", input.CompID,
			)
		}

		baseReq := baseResp.Request
		compReq := compResp.Request

		result := DiffResult{
			BaseID:        input.BaseID,
			CompID:        input.CompID,
			BodyLimitUsed: bodyLimit,
		}

		result.ReqMethodBase = baseReq.Method
		result.ReqMethodComp = compReq.Method
		result.MethodChanged = baseReq.Method != compReq.Method

		if baseReq.Response != nil {
			result.StatusBase = baseReq.Response.StatusCode
		}
		if compReq.Response != nil {
			result.StatusComp = compReq.Response.StatusCode
		}
		result.StatusChanged = result.StatusBase != result.StatusComp

		baseParsedReq := httputil.ParseBase64(
			baseReq.Raw, true, true, 0, bodyLimit,
		)
		compParsedReq := httputil.ParseBase64(
			compReq.Raw, true, true, 0, bodyLimit,
		)

		if sections == "all" || sections == "headers" {
			if baseParsedReq != nil && compParsedReq != nil {
				result.ReqHeaders = diffHeaders(
					baseParsedReq.Headers, compParsedReq.Headers,
				)
			}
		}

		if sections == "all" || sections == "body" {
			if baseParsedReq != nil {
				result.ReqBodyBase = baseParsedReq.Body
			}
			if compParsedReq != nil {
				result.ReqBodyComp = compParsedReq.Body
			}
			result.ReqBodySame = result.ReqBodyBase == result.ReqBodyComp
		}

		if baseReq.Response != nil && compReq.Response != nil {
			baseParsedResp := httputil.ParseBase64(
				baseReq.Response.Raw, true, true, 0, bodyLimit,
			)
			compParsedResp := httputil.ParseBase64(
				compReq.Response.Raw, true, true, 0, bodyLimit,
			)

			if sections == "all" || sections == "headers" {
				if baseParsedResp != nil && compParsedResp != nil {
					result.RespHeaders = diffHeaders(
						baseParsedResp.Headers, compParsedResp.Headers,
					)
				}
			}

			if sections == "all" || sections == "body" {
				if baseParsedResp != nil {
					result.RespBodyBase = baseParsedResp.Body
				}
				if compParsedResp != nil {
					result.RespBodyComp = compParsedResp.Body
				}
				result.RespBodySame = result.RespBodyBase == result.RespBodyComp
			}
		}

		return nil, result, nil
	}
}

// diffHeaders computes header-level diffs between two sets
func diffHeaders(base, comp []httputil.Header) []HeaderDiff {
	baseMap := make(map[string]string)
	for _, h := range base {
		baseMap[h.Name] = h.Value
	}
	compMap := make(map[string]string)
	for _, h := range comp {
		compMap[h.Name] = h.Value
	}

	var diffs []HeaderDiff

	for _, h := range base {
		cv, exists := compMap[h.Name]
		if !exists {
			diffs = append(diffs, HeaderDiff{
				Name:    h.Name,
				BaseVal: h.Value,
				Status:  "removed",
			})
		} else if h.Value != cv {
			diffs = append(diffs, HeaderDiff{
				Name:    h.Name,
				BaseVal: h.Value,
				CompVal: cv,
				Status:  "changed",
			})
		}
	}

	for _, h := range comp {
		if _, exists := baseMap[h.Name]; !exists {
			diffs = append(diffs, HeaderDiff{
				Name:    h.Name,
				CompVal: h.Value,
				Status:  "added",
			})
		}
	}

	return diffs
}

// RegisterDiffRequestsTool registers the tool with the MCP server
func RegisterDiffRequestsTool(
	server *mcp.Server, client *caido.Client,
) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "caido_diff_requests",
		Description: `Compare two captured requests and their responses. Shows header diffs (added/removed/changed), body diffs, status code and method changes. Params: baseId, compId, sections (all|headers|body|status, default: all).`,
	}, diffRequestsHandler(client))
}
