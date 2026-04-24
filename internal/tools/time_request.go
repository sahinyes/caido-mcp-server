package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	caido "github.com/caido-community/sdk-go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const maxTimingSamples = 20

// TimingRequestInput is the input for the time_request tool
type TimingRequestInput struct {
	Raw          string   `json:"raw" jsonschema:"required,Raw HTTP request including headers and body"`
	Host         string   `json:"host,omitempty" jsonschema:"Target host (overrides Host header)"`
	Port         int      `json:"port,omitempty" jsonschema:"Target port (default based on TLS)"`
	TLS          *bool    `json:"tls,omitempty" jsonschema:"Use HTTPS (default true)"`
	SessionID    string   `json:"sessionId,omitempty" jsonschema:"Replay session ID (optional)"`
	Samples      int      `json:"samples,omitempty" jsonschema:"Number of sends per payload (default 5, max 20)"`
	Payloads     []string `json:"payloads,omitempty" jsonschema:"Vary payloads per sample. Each replaces payload_field in the request body."`
	PayloadField string   `json:"payloadField,omitempty" jsonschema:"Placeholder string in raw body to substitute each payload into (e.g. PAYLOAD_HERE)"`
}

// TimingSample is a single request timing observation
type TimingSample struct {
	Index     int    `json:"index"`
	Payload   string `json:"payload,omitempty"`
	ElapsedMs int    `json:"elapsed_ms"`
	Status    int    `json:"statusCode,omitempty"`
	Error     string `json:"error,omitempty"`
}

// TimingSummary holds aggregate stats across all samples
type TimingSummary struct {
	MinMs    int `json:"min_ms"`
	MedianMs int `json:"median_ms"`
	P95Ms    int `json:"p95_ms"`
	MaxMs    int `json:"max_ms"`
	Count    int `json:"count"`
}

// TimingPerPayload holds stats for a single payload value
type TimingPerPayload struct {
	MedianMs int `json:"median_ms"`
	Count    int `json:"count"`
}

// TimingRequestOutput is the output of the time_request tool
type TimingRequestOutput struct {
	Samples    []TimingSample              `json:"samples"`
	Summary    TimingSummary               `json:"summary"`
	PerPayload map[string]*TimingPerPayload `json:"per_payload,omitempty"`
}

func timeRequestHandler(
	client *caido.Client,
) func(context.Context, *mcp.CallToolRequest, TimingRequestInput) (*mcp.CallToolResult, TimingRequestOutput, error) {
	return func(
		ctx context.Context,
		req *mcp.CallToolRequest,
		input TimingRequestInput,
	) (*mcp.CallToolResult, TimingRequestOutput, error) {
		if input.Raw == "" {
			return nil, TimingRequestOutput{}, fmt.Errorf("raw is required")
		}

		samples := input.Samples
		if samples <= 0 {
			samples = 5
		}
		if samples > maxTimingSamples {
			samples = maxTimingSamples
		}

		payloads := input.Payloads
		if len(payloads) == 0 {
			payloads = []string{""}
		}

		allSamples := make([]TimingSample, 0, samples*len(payloads))
		allMs := make([]int, 0, samples*len(payloads))
		perPayload := make(map[string][]int)

		idx := 0
		for _, payload := range payloads {
			for i := 0; i < samples; i++ {
				rawBody := input.Raw
				if payload != "" && input.PayloadField != "" {
					rawBody = strings.ReplaceAll(rawBody, input.PayloadField, payload)
				}

				sendInput := SendRequestInput{
					Raw:       rawBody,
					Host:      input.Host,
					Port:      input.Port,
					TLS:       input.TLS,
					SessionID: input.SessionID,
				}

				result, err := executeSendRequest(ctx, client, sendInput)
				sample := TimingSample{
					Index:   idx,
					Payload: payload,
				}
				if err != nil {
					sample.Error = err.Error()
				} else {
					sample.ElapsedMs = result.ElapsedMs
					sample.Status = result.StatusCode
					allMs = append(allMs, result.ElapsedMs)
					perPayload[payload] = append(perPayload[payload], result.ElapsedMs)
				}

				allSamples = append(allSamples, sample)
				idx++
			}
		}

		output := TimingRequestOutput{
			Samples: allSamples,
		}

		if len(allMs) > 0 {
			output.Summary = computeTimingSummary(allMs)
		}

		if len(payloads) > 1 || (len(payloads) == 1 && payloads[0] != "") {
			output.PerPayload = make(map[string]*TimingPerPayload)
			for p, ms := range perPayload {
				if len(ms) > 0 {
					s := computeTimingSummary(ms)
					output.PerPayload[p] = &TimingPerPayload{
						MedianMs: s.MedianMs,
						Count:    len(ms),
					}
				}
			}
		}

		return nil, output, nil
	}
}

// executeSendRequest runs the send_request logic and returns the output.
func executeSendRequest(
	ctx context.Context, client *caido.Client, input SendRequestInput,
) (SendRequestOutput, error) {
	var result SendRequestOutput
	handler := sendRequestHandler(client)
	_, out, err := handler(ctx, nil, input)
	if err != nil {
		return result, err
	}
	if out.Error != "" {
		return result, fmt.Errorf("%s", out.Error)
	}
	return out, nil
}

func computeTimingSummary(ms []int) TimingSummary {
	sorted := make([]int, len(ms))
	copy(sorted, ms)
	sort.Ints(sorted)

	n := len(sorted)
	min := sorted[0]
	max := sorted[n-1]

	median := sorted[n/2]
	if n%2 == 0 {
		median = (sorted[n/2-1] + sorted[n/2]) / 2
	}

	p95idx := int(float64(n) * 0.95)
	if p95idx >= n {
		p95idx = n - 1
	}
	p95 := sorted[p95idx]

	return TimingSummary{
		MinMs:    min,
		MedianMs: median,
		P95Ms:    p95,
		MaxMs:    max,
		Count:    n,
	}
}

// RegisterTimeRequestTool registers the tool with the MCP server
func RegisterTimeRequestTool(
	server *mcp.Server, client *caido.Client,
) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "caido_time_request",
		Description: `Send a request N times and measure response timing (ms). ` +
			`Use payloads+payloadField for A/B timing (e.g. sleep oracle detection). ` +
			`Returns samples array + min/median/p95/max summary. ` +
			`Note: timing is measured in milliseconds (Caido RoundtripTime resolution).`,
	}, timeRequestHandler(client))
}
