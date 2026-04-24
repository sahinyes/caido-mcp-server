package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	caido "github.com/caido-community/sdk-go"
	gen "github.com/caido-community/sdk-go/graphql"
	"github.com/c0tton-fluff/caido-mcp-server/internal/httputil"
	"github.com/c0tton-fluff/caido-mcp-server/internal/replay"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ReplayRequestInput is the input for the replay_request tool
type ReplayRequestInput struct {
	ID            string            `json:"id" jsonschema:"required,Request ID to clone and replay"`
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
	RoundtripMs int                     `json:"roundtripMs,omitempty"`
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

		// Snapshot current active entry
		var previousEntryID string
		sessResp, err := client.Replay.GetSession(ctx, sessionID)
		if err == nil && sessResp.ReplaySession != nil &&
			sessResp.ReplaySession.ActiveEntry != nil {
			previousEntryID = sessResp.ReplaySession.ActiveEntry.Id
		}

		rawBase64 := base64.StdEncoding.EncodeToString([]byte(modifiedRaw))

		taskInput := &gen.StartReplayTaskInput{
			Connection: gen.ConnectionInfoInput{
				Host:  orig.Host,
				Port:  orig.Port,
				IsTLS: orig.IsTls,
			},
			Raw: rawBase64,
			Settings: gen.ReplayEntrySettingsInput{
				Placeholders:        []gen.ReplayPlaceholderInput{},
				UpdateContentLength: true,
				ConnectionClose:     false,
			},
		}

		_, err = client.Replay.SendRequest(ctx, sessionID, taskInput)
		if err != nil {
			if strings.Contains(err.Error(), "TaskInProgressUserError") {
				newSess, createErr := client.Replay.CreateSession(
					ctx, &gen.CreateReplaySessionInput{},
				)
				if createErr != nil {
					return nil, ReplayRequestOutput{}, fmt.Errorf(
						"failed to create fallback session: %w", createErr,
					)
				}
				sessionID = newSess.CreateReplaySession.Session.Id
				if input.SessionID == "" {
					replay.ResetDefaultSession(sessionID)
				}
				previousEntryID = ""
				_, err = client.Replay.SendRequest(ctx, sessionID, taskInput)
				if err != nil {
					return nil, ReplayRequestOutput{}, fmt.Errorf(
						"failed to replay request (retry): %w", err,
					)
				}
			} else {
				return nil, ReplayRequestOutput{}, fmt.Errorf(
					"failed to replay request: %w", err,
				)
			}
		}

		output := ReplayRequestOutput{SessionID: sessionID}

		entry, pollErr := replay.PollForEntry(
			ctx, client, sessionID, previousEntryID,
		)
		if pollErr != nil {
			output.Error = fmt.Sprintf(
				"poll failed: %v (use get_replay_entry to retry)", pollErr,
			)
			sResp, sErr := client.Replay.GetSession(ctx, sessionID)
			if sErr == nil && sResp.ReplaySession != nil &&
				sResp.ReplaySession.ActiveEntry != nil {
				output.EntryID = sResp.ReplaySession.ActiveEntry.Id
			}
			return nil, output, nil
		}

		output.EntryID = entry.Id

		bodyLimit := input.BodyLimit
		if bodyLimit == 0 {
			bodyLimit = httputil.DefaultBodyLimit
		}

		if entry.Request != nil {
			output.RequestID = entry.Request.Id
			output.Request = httputil.ParseBase64(
				entry.Request.Raw, true, false, 0, 0,
			)
			if entry.Request.Response != nil {
				resp := entry.Request.Response
				output.StatusCode = resp.StatusCode
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
		Description: `Clone a captured request and resend with modifications. Supports setHeaders (add/replace), removeHeaders, body (replace), method (override), path (override). Returns response inline. Params: id (request ID), setHeaders, removeHeaders, body, method, path, sessionId, bodyLimit, bodyOffset.`,
	}, replayRequestHandler(client))
}
