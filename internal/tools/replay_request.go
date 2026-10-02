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

		// Parse a CRLF-NORMALISED copy. The header/body split keys off
		// \r\n\r\n, so a captured request stored with bare LF line endings
		// parses as one enormous header block with an empty body - and the
		// rebuild path would then send a request with its body silently
		// dropped. The bytes that go on the wire on the verbatim path are still
		// orig.Raw, untouched.
		decoded, derr := base64.StdEncoding.DecodeString(orig.Raw)
		if derr != nil {
			return nil, ReplayRequestOutput{}, fmt.Errorf(
				"request %s is not valid base64: %w", input.ID, derr,
			)
		}
		parsed := httputil.ParseRaw(
			[]byte(httputil.NormalizeCRLF(string(decoded))), true, true, 0, 0,
		)
		if parsed == nil {
			return nil, ReplayRequestOutput{}, fmt.Errorf(
				"failed to parse request %s", input.ID,
			)
		}

		// Resend the CAPTURED BYTES unless the caller asked for a change.
		//
		// Rebuilding from the parse is lossy in ways that matter to what is
		// being tested: header order and spacing are normalised, every line
		// the parser did not model as a header is dropped, and the body is
		// re-emitted from the parsed copy. For a plain replay of request N
		// that is a different request than N. host/port/tls are NOT rewrites
		// here - they change the TCP target, not the bytes.
		rawBase64 := orig.Raw
		if wantsRewrite(input) {
			rawBase64 = base64.StdEncoding.EncodeToString(
				[]byte(applyModifications(parsed, input)),
			)
		}

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

		// One call does the whole sequence under this session's lock; see
		// replay.Send. A caller-supplied sessionId is never swapped for
		// another session behind the caller's back.
		outcome, err := replay.Send(ctx, client, replay.SendOptions{
			SessionID:           input.SessionID,
			Conn:                conn,
			RawBase64:           rawBase64,
			UpdateContentLength: true,
			PollTimeout:         replay.DefaultSendPollTimeout,
		})
		if err != nil {
			return nil, ReplayRequestOutput{}, err
		}

		output := ReplayRequestOutput{SessionID: outcome.SessionID}

		if outcome.PollErr != nil {
			// No entryId on purpose: the session's active entry may still be
			// the PREVIOUS send's request and response. See
			// replay.SendOutcome.EntryID.
			output.Error = fmt.Sprintf(
				"no response yet: %v (the task may still be running on "+
					"replay session %s - call list_replay_sessions to read "+
					"that session's activeEntryId, then get_replay_entry on "+
					"it)",
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

// wantsRewrite reports whether the caller asked for anything that changes the
// request BYTES. Connection overrides (host/port/tls) are not in the list:
// they retarget the same bytes.
func wantsRewrite(input ReplayRequestInput) bool {
	return input.Method != "" ||
		input.Path != "" ||
		input.Body != nil ||
		len(input.SetHeaders) > 0 ||
		len(input.RemoveHeaders) > 0
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
			// setHeaders is documented as "add or replace", so a header the
			// original carries TWICE must come out once. Without this the
			// override was written once per original occurrence - two Cookie
			// lines in, two identical Cookie lines out.
			if written[lower] {
				continue
			}
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
		Description: `Clone a captured request by ID and resend with modifications. Fetches the original request fresh on every call. Supports host/port/tls override (changes TCP target, not just Host header), setHeaders (add/replace), removeHeaders, body (replace), method, path. sessionId controls the replay session pool - original request is always re-fetched from Caido, and a sessionId you pass is never swapped for another session. With no setHeaders/removeHeaders/body/method/path the captured bytes are resent byte-for-byte except that Caido recomputes Content-Length (the session setting this server enforces); any of those overrides rebuilds the request, which also normalises header order and spacing. Returns response inline.`,
	}, replayRequestHandler(client))
}
