package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	caido "github.com/caido-community/sdk-go"
	"github.com/c0tton-fluff/caido-mcp-server/internal/httputil"
	"github.com/c0tton-fluff/caido-mcp-server/internal/replay"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ReplayRequestInput is the input for the replay_request tool
type ReplayRequestInput struct {
	ID            string            `json:"id" jsonschema:"required,Request ID to clone and replay"`
	Host          string            `json:"host,omitempty" jsonschema:"Override target host (default: original request host)"`
	Port          int               `json:"port,omitempty" jsonschema:"Override target port (default: original)"`
	TLS           *bool             `json:"tls,omitempty" jsonschema:"Override TLS (default: original)"`
	SetHeaders    map[string]string `json:"setHeaders,omitempty" jsonschema:"Headers to add or replace (name -> value)"`
	RemoveHeaders []string          `json:"removeHeaders,omitempty" jsonschema:"Header names to remove"`
	Body          *string           `json:"body,omitempty" jsonschema:"Replace request body with this value"`
	Method        string            `json:"method,omitempty" jsonschema:"Override HTTP method"`
	Path          string            `json:"path,omitempty" jsonschema:"Override request path"`
	SessionID     string            `json:"sessionId,omitempty" jsonschema:"Replay session ID (optional)"`
	BodyLimit     int               `json:"bodyLimit,omitempty" jsonschema:"Response body byte limit (default 2000)"`
	BodyOffset    int               `json:"bodyOffset,omitempty" jsonschema:"Response body byte offset (default 0)"`
}

// ReplayRequestOutput is the output of the replay_request tool
type ReplayRequestOutput struct {
	RequestID   string                  `json:"requestId,omitempty"`
	EntryID     string                  `json:"entryId,omitempty"`
	SessionID   string                  `json:"sessionId"`
	StatusCode  int                     `json:"statusCode,omitempty"`
	ElapsedMs   int                     `json:"elapsed_ms,omitempty"`
	RoundtripMs int                     `json:"roundtripMs,omitempty"` // deprecated: use elapsed_ms
	Request     *httputil.ParsedMessage `json:"request,omitempty"`
	Response    *httputil.ParsedMessage `json:"response,omitempty"`
	Error       string                  `json:"error,omitempty"`
}

// replayRequestHandler creates the handler function
func replayRequestHandler(
	client *caido.Client,
) func(context.Context, *mcp.CallToolRequest, ReplayRequestInput) (*mcp.CallToolResult, ReplayRequestOutput, error) {
	return func(
		ctx context.Context,
		req *mcp.CallToolRequest,
		input ReplayRequestInput,
	) (*mcp.CallToolResult, ReplayRequestOutput, error) {
		if input.ID == "" {
			return nil, ReplayRequestOutput{}, fmt.Errorf(
				"request ID is required",
			)
		}

		// Fetch original request
		origResp, err := client.Requests.Get(ctx, input.ID)
		if err != nil {
			return nil, ReplayRequestOutput{}, fmt.Errorf(
				"failed to get request %s: %w", input.ID, err,
			)
		}
		if origResp.Request == nil {
			return nil, ReplayRequestOutput{}, fmt.Errorf(
				"request %s not found", input.ID,
			)
		}

		orig := origResp.Request

		// Parse raw request
		parsed := httputil.ParseBase64(orig.Raw, true, true, 0, 0)
		if parsed == nil {
			return nil, ReplayRequestOutput{}, fmt.Errorf(
				"failed to parse request %s", input.ID,
			)
		}

		// Apply modifications
		modifiedRaw := applyModifications(parsed, input)

		// Get or create replay session
		sessionID, err := replay.GetOrCreateSession(
			ctx, client, input.SessionID,
		)
		if err != nil {
			return nil, ReplayRequestOutput{}, err
		}

		rawBase64 := base64.StdEncoding.EncodeToString([]byte(modifiedRaw))

		host := orig.Host
		if input.Host != "" {
			host = input.Host
		}
		port := orig.Port
		if input.Port != 0 {
			port = input.Port
		}
		useTLS := orig.IsTls
		if input.TLS != nil {
			useTLS = *input.TLS
		}

		conn := caido.ReplayConnection{
			Host:  host,
			Port:  port,
			IsTLS: useTLS,
		}

		taskResp, sendState, err := replay.SendRaw(
			ctx, client, sessionID, conn, rawBase64, true, false,
		)
		if err != nil || isTaskInProgress(taskResp) {
			newSessionID, _, createErr := replay.NewSession(ctx, client)
			if createErr != nil {
				return nil, ReplayRequestOutput{}, fmt.Errorf(
					"failed to create fallback session: %w", createErr,
				)
			}
			sessionID = newSessionID
			if input.SessionID == "" {
				replay.ResetDefaultSession(sessionID)
			}
			_, sendState, err = replay.SendRaw(
				ctx, client, sessionID, conn, rawBase64, true, false,
			)
			if err != nil {
				return nil, ReplayRequestOutput{}, fmt.Errorf(
					"failed to replay request (retry): %w", err,
				)
			}
		}

		output := ReplayRequestOutput{SessionID: sessionID}

		entry, pollErr := replay.PollForEntry(ctx, client, sendState)
		if pollErr != nil {
			output.Error = fmt.Sprintf(
				"poll failed: %v (use get_replay_entry to retry)", pollErr,
			)
			output.EntryID = replay.ActiveEntryID(ctx, client, sessionID)
			return nil, output, nil
		}

		output.EntryID = entry.ID

		if entry.Error != nil && *entry.Error != "" {
			output.Error = *entry.Error
		}

		bodyLimit := input.BodyLimit
		if bodyLimit == 0 {
			bodyLimit = httputil.DefaultBodyLimit
		}

		if entry.Request != nil {
			output.RequestID = entry.Request.ID
			output.Request = httputil.ParseBase64(
				entry.Request.Raw, true, false, 0, 0,
			)
			if entry.Request.Response != nil {
				resp := entry.Request.Response
				output.StatusCode = resp.StatusCode
				output.ElapsedMs = resp.RoundtripTime
			output.RoundtripMs = resp.RoundtripTime
				output.Response = httputil.ParseBase64(
					resp.Raw, true, true, input.BodyOffset, bodyLimit,
				)
			}
		}

		return nil, output, nil
	}
}

// applyModifications rebuilds the raw HTTP request with the given modifications
func applyModifications(parsed *httputil.ParsedMessage, input ReplayRequestInput) string {
	var b strings.Builder

	// First line: method, path, version
	firstLine := parsed.FirstLine
	parts := strings.SplitN(firstLine, " ", 3)
	method := ""
	path := ""
	version := "HTTP/1.1"
	if len(parts) >= 1 {
		method = parts[0]
	}
	if len(parts) >= 2 {
		path = parts[1]
	}
	if len(parts) >= 3 {
		version = parts[2]
	}

	if input.Method != "" {
		method = strings.ToUpper(input.Method)
	}
	if input.Path != "" {
		path = input.Path
	}

	b.WriteString(method)
	b.WriteString(" ")
	b.WriteString(path)
	b.WriteString(" ")
	b.WriteString(version)
	b.WriteString("\r\n")

	// Build remove set
	removeSet := make(map[string]bool)
	for _, name := range input.RemoveHeaders {
		removeSet[strings.ToLower(name)] = true
	}

	// Override set (normalised key -> original value)
	overrideMap := make(map[string]string)
	overrideVal := make(map[string]string)
	for name, val := range input.SetHeaders {
		overrideMap[strings.ToLower(name)] = name
		overrideVal[strings.ToLower(name)] = val
	}

	// Write original headers with modifications applied
	written := make(map[string]bool)
	for _, h := range parsed.Headers {
		lower := strings.ToLower(h.Name)
		if removeSet[lower] {
			continue
		}
		if val, ok := overrideVal[lower]; ok {
			b.WriteString(overrideMap[lower])
			b.WriteString(": ")
			b.WriteString(val)
			b.WriteString("\r\n")
			written[lower] = true
			continue
		}
		b.WriteString(h.Name)
		b.WriteString(": ")
		b.WriteString(h.Value)
		b.WriteString("\r\n")
	}

	// Append new headers not already written
	for lower, name := range overrideMap {
		if !written[lower] {
			b.WriteString(name)
			b.WriteString(": ")
			b.WriteString(overrideVal[lower])
			b.WriteString("\r\n")
		}
	}

	b.WriteString("\r\n")

	// Body
	if input.Body != nil {
		b.WriteString(*input.Body)
	} else {
		b.WriteString(parsed.Body)
	}

	return b.String()
}

// RegisterReplayRequestTool registers the tool with the MCP server
func RegisterReplayRequestTool(
	server *mcp.Server, client *caido.Client,
) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "caido_replay_request",
		Description: `Clone a captured request by ID and resend with modifications. Fetches the original request fresh on every call. Supports host/port/tls override (changes TCP target, not just Host header), setHeaders (add/replace), removeHeaders, body (replace), method, path. sessionId controls the replay session pool — original request is always re-fetched from Caido. Returns response inline.`,
	}, replayRequestHandler(client))
}
