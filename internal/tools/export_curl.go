package tools

import (
	"context"
	"fmt"
	"strings"

	caido "github.com/caido-community/sdk-go"
	"github.com/c0tton-fluff/caido-mcp-server/internal/httputil"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ExportCurlInput is the input for the export_curl tool
type ExportCurlInput struct {
	ID string `json:"id" jsonschema:"required,Request ID to export"`
}

// ExportCurlOutput is the output of the export_curl tool
type ExportCurlOutput struct {
	Curl string `json:"curl"`
}

// exportCurlHandler creates the handler function
func exportCurlHandler(
	client *caido.Client,
) func(context.Context, *mcp.CallToolRequest, ExportCurlInput) (*mcp.CallToolResult, ExportCurlOutput, error) {
	return func(
		ctx context.Context,
		req *mcp.CallToolRequest,
		input ExportCurlInput,
	) (*mcp.CallToolResult, ExportCurlOutput, error) {
		if input.ID == "" {
			return nil, ExportCurlOutput{}, fmt.Errorf(
				"request ID is required",
			)
		}

		resp, err := client.Requests.Get(ctx, input.ID)
		if err != nil {
			return nil, ExportCurlOutput{}, fmt.Errorf(
				"failed to get request %s: %w", input.ID, err,
			)
		}
		if resp.Request == nil {
			return nil, ExportCurlOutput{}, fmt.Errorf(
				"request %s not found", input.ID,
			)
		}

		r := resp.Request
		parsed := httputil.ParseBase64(r.Raw, true, true, 0, 0)
		if parsed == nil {
			return nil, ExportCurlOutput{}, fmt.Errorf(
				"failed to parse request %s", input.ID,
			)
		}

		scheme := "https"
		if !r.IsTls {
			scheme = "http"
		}

		curl := buildCurl(parsed, scheme)
		return nil, ExportCurlOutput{Curl: curl}, nil
	}
}

// buildCurl generates a curl command from parsed request content
func buildCurl(parsed *httputil.ParsedMessage, scheme string) string {
	var b strings.Builder

	// Parse method and URL from first line
	parts := strings.SplitN(parsed.FirstLine, " ", 3)
	method := "GET"
	path := "/"
	if len(parts) >= 2 {
		method = parts[0]
		path = parts[1]
	}

	// Find host from headers
	host := ""
	for _, h := range parsed.Headers {
		if strings.EqualFold(h.Name, "Host") {
			host = h.Value
			break
		}
	}

	url := scheme + "://" + host + path

	b.WriteString("curl")

	// Method (skip -X for GET)
	if method != "GET" {
		b.WriteString(" -X ")
		b.WriteString(shellQuote(method))
	}

	// URL
	b.WriteString(" ")
	b.WriteString(shellQuote(url))

	// Headers (skip Host, Content-Length — curl sets these)
	skipHeaders := map[string]bool{
		"host":           true,
		"content-length": true,
	}
	for _, h := range parsed.Headers {
		if skipHeaders[strings.ToLower(h.Name)] {
			continue
		}
		b.WriteString(" \\\n  -H ")
		b.WriteString(shellQuote(h.Name + ": " + h.Value))
	}

	// Body
	if parsed.Body != "" {
		b.WriteString(" \\\n  -d ")
		b.WriteString(shellQuote(parsed.Body))
	}

	return b.String()
}

// shellQuote wraps a string in single quotes for shell safety
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// RegisterExportCurlTool registers the tool with the MCP server
func RegisterExportCurlTool(
	server *mcp.Server, client *caido.Client,
) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "caido_export_curl",
		Description: `Export a captured request as a curl command. Useful for reproduction steps in reports. Params: id (request ID).`,
	}, exportCurlHandler(client))
}
