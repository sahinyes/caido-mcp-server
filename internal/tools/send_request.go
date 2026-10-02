package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"strconv"

	"github.com/c0tton-fluff/caido-mcp-server/internal/httputil"
	"github.com/c0tton-fluff/caido-mcp-server/internal/replay"
	caido "github.com/caido-community/sdk-go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SendRequestInput is the input for the send_request tool
type SendRequestInput struct {
	Raw             string `json:"raw" jsonschema:"required,Raw HTTP request including headers and body"`
	Host            string `json:"host,omitempty" jsonschema:"Target host (overrides Host header)"`
	Port            int    `json:"port,omitempty" jsonschema:"Target port (default based on TLS)"`
	TLS             *bool  `json:"tls,omitempty" jsonschema:"Use HTTPS (default true)"`
	SessionID       string `json:"sessionId,omitempty" jsonschema:"Replay session ID (optional)"`
	BodyLimit       int    `json:"bodyLimit,omitempty" jsonschema:"Response body byte limit (default 2000)"`
	BodyOffset      int    `json:"bodyOffset,omitempty" jsonschema:"Response body byte offset (default 0)"`
	NoRequestEcho   bool   `json:"noRequestEcho,omitempty" jsonschema:"Omit the echoed request from output. Use when chunking large bodies to stay within token limits."`
}

// SendRequestOutput is the output of the send_request tool
type SendRequestOutput struct {
	RequestID  string                  `json:"requestId,omitempty"`
	EntryID    string                  `json:"entryId,omitempty"`
	SessionID  string                  `json:"sessionId"`
	StatusCode int                     `json:"statusCode,omitempty"`
	ElapsedMs  int                     `json:"elapsed_ms,omitempty"`
	Request    *httputil.ParsedMessage `json:"request,omitempty"`
	Response   *httputil.ParsedMessage `json:"response,omitempty"`
	Error      string                  `json:"error,omitempty"`
}

// sendRequestHandler creates the handler function
func sendRequestHandler(
	client *caido.Client,
) func(context.Context, *mcp.CallToolRequest, SendRequestInput) (*mcp.CallToolResult, SendRequestOutput, error) {
	return func(
		ctx context.Context,
		req *mcp.CallToolRequest,
		input SendRequestInput,
	) (*mcp.CallToolResult, SendRequestOutput, error) {
		if input.Raw == "" {
			return nil, SendRequestOutput{}, fmt.Errorf(
				"raw HTTP request is required",
			)
		}
		if len(input.Raw) > 1048576 {
			return nil, SendRequestOutput{}, fmt.Errorf(
				"raw request exceeds max length of 1MB",
			)
		}

		raw := httputil.NormalizeCRLF(input.Raw)

		// Determine host
		host := input.Host
		if host == "" {
			host = httputil.ParseHostHeader(input.Raw)
		}
		if host == "" {
			return nil, SendRequestOutput{}, fmt.Errorf(
				"host is required (provide in input or Host header)",
			)
		}

		// Parse host:port
		if h, p, err := net.SplitHostPort(host); err == nil {
			host = h
			if input.Port == 0 {
				if port, pErr := strconv.Atoi(p); pErr == nil {
					input.Port = port
				}
			}
		}

		// Determine TLS and port
		useTLS := true
		if input.TLS != nil {
			useTLS = *input.TLS
		}
		port := input.Port
		if port == 0 {
			if useTLS {
				port = 443
			} else {
				port = 80
			}
		}

		rawBase64 := base64.StdEncoding.EncodeToString([]byte(raw))
		conn := caido.ReplayConnection{
			Host:  host,
			Port:  port,
			IsTLS: useTLS,
		}

		// One call does the whole sequence: resolve the session, serialise
		// everything that touches it, draft, start, wait. Doing those steps
		// here by hand is what let two concurrent tool calls trade drafts.
		outcome, err := replay.Send(ctx, client, replay.SendOptions{
			SessionID:           input.SessionID,
			Conn:                conn,
			RawBase64:           rawBase64,
			UpdateContentLength: true,
		})
		if err != nil {
			return nil, SendRequestOutput{}, err
		}

		output := SendRequestOutput{SessionID: outcome.SessionID}

		if outcome.PollErr != nil {
			// Deliberately NO entryId. The session's current active entry is
			// not necessarily this send's: if the task never started it still
			// holds the PREVIOUS request and its response, and this tool's
			// own description used to tell the caller to go fetch it. The
			// session id is the honest handle.
			output.Error = fmt.Sprintf(
				"no response yet: %v (the task may still be running on "+
					"replay session %s - re-read that session's active "+
					"entry with get_replay_entry)",
				outcome.PollErr, outcome.SessionID,
			)
			return nil, output, nil
		}

		entry := outcome.Entry
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
			if !input.NoRequestEcho {
				output.Request = httputil.ParseBase64(
					entry.Request.Raw, true, false, 0, 0,
				)
			}
			if entry.Request.Response != nil {
				resp := entry.Request.Response
				output.StatusCode = resp.StatusCode
				output.ElapsedMs = resp.RoundtripTime
				output.Response = httputil.ParseBase64(
					resp.Raw, true, true,
					input.BodyOffset, bodyLimit,
				)
			}
		}

		return nil, output, nil
	}
}

// RegisterSendRequestTool registers the tool with the MCP server
func RegisterSendRequestTool(
	server *mcp.Server, client *caido.Client,
) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "caido_send_request",
		Description: `Send HTTP request and return response inline. Returns statusCode, headers, body. Polls up to ~9s for response. On timeout, returns sessionId and NO entryId (the session's active entry may still be the previous send's) - re-read that session to collect the answer. Redirects are NOT followed (returns 3xx directly). TLS verification is enforced by the Caido replay engine. Use noRequestEcho=true when chunking large responses to reduce token usage.`,
	}, sendRequestHandler(client))
}
