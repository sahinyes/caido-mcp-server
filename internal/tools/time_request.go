package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

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

// TimingSample is a single request timing observation.
//
// The measurement is Caido's OWN roundtrip, not the client wall clock. The
// client clock cannot answer this tool's question: a send is not a blocking
// call any more, it is a draft, a start and then a POLL at 50, 100, 200, 400,
// 500... ms, so every client-side number is quantised to that grid. Two
// payloads differing by 300 ms can land in the same bucket and read as
// identical, which is exactly the comparison a sleep oracle needs. The client
// total is kept beside it, named for what it is, because it is still the right
// number for "how long did this tool take".
type TimingSample struct {
	Index         int    `json:"index"`
	Payload       string `json:"payload,omitempty"`
	RoundtripNs   int64  `json:"roundtrip_ns"`    // Caido's measurement (ms resolution)
	ClientTotalNs int64  `json:"client_total_ns"` // MCP + SDK + poll grid
	Status        int    `json:"statusCode,omitempty"`
	Error         string `json:"error,omitempty"`
}

// TimingSummary holds aggregate stats across all samples (nanoseconds)
type TimingSummary struct {
	MinNs    int64 `json:"min_ns"`
	MedianNs int64 `json:"median_ns"`
	P95Ns    int64 `json:"p95_ns"`
	MaxNs    int64 `json:"max_ns"`
	Count    int   `json:"count"`
}

// TimingPerPayload holds stats for a single payload value (nanoseconds)
type TimingPerPayload struct {
	MedianNs int64 `json:"median_ns"`
	Count    int   `json:"count"`
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
		} else {
			// An A/B run with nothing to vary is not an A/B run. Both of these
			// used to be accepted silently: every arm sent the identical
			// request and per_payload then reported the arms as if they had
			// been compared, which is a fabricated comparison rather than a
			// missing one.
			if input.PayloadField == "" {
				return nil, TimingRequestOutput{}, fmt.Errorf(
					"payloads need payloadField: the placeholder in raw to "+
						"substitute each payload into",
				)
			}
			if !strings.Contains(input.Raw, input.PayloadField) {
				return nil, TimingRequestOutput{}, fmt.Errorf(
					"payloadField %q does not occur in raw",
					input.PayloadField,
				)
			}
		}

		allSamples := make([]TimingSample, 0, samples*len(payloads))
		allNs := make([]int64, 0, samples*len(payloads))
		perPayload := make(map[string][]int64)

		idx := 0
		for _, payload := range payloads {
			for i := 0; i < samples; i++ {
				rawBody := input.Raw
				if input.PayloadField != "" {
					// Substituted even when the payload is EMPTY: "" is the
					// natural baseline arm of an A/B run, and skipping it sent
					// the placeholder token itself as the value.
					rawBody = strings.ReplaceAll(
						rawBody, input.PayloadField, payload,
					)
				}

				sendInput := SendRequestInput{
					Raw:       rawBody,
					Host:      input.Host,
					Port:      input.Port,
					TLS:       input.TLS,
					SessionID: input.SessionID,
				}

				start := time.Now()
				result, err := executeSendRequest(ctx, client, sendInput)
				clientNs := time.Since(start).Nanoseconds()

				sample := TimingSample{
					Index:         idx,
					Payload:       payload,
					ClientTotalNs: clientNs,
				}
				if err != nil {
					sample.Error = err.Error()
				} else {
					roundtripNs := int64(result.ElapsedMs) *
						int64(time.Millisecond)
					sample.RoundtripNs = roundtripNs
					sample.Status = result.StatusCode
					allNs = append(allNs, roundtripNs)
					perPayload[payload] = append(
						perPayload[payload], roundtripNs,
					)
				}

				allSamples = append(allSamples, sample)
				idx++
			}
		}

		output := TimingRequestOutput{
			Samples: allSamples,
		}

		if len(allNs) > 0 {
			output.Summary = computeTimingSummary(allNs)
		}

		if len(payloads) > 1 || (len(payloads) == 1 && payloads[0] != "") {
			output.PerPayload = make(map[string]*TimingPerPayload)
			for p, ns := range perPayload {
				if len(ns) > 0 {
					s := computeTimingSummary(ns)
					output.PerPayload[p] = &TimingPerPayload{
						MedianNs: s.MedianNs,
						Count:    len(ns),
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

func computeTimingSummary(ns []int64) TimingSummary {
	sorted := make([]int64, len(ns))
	copy(sorted, ns)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

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
		MinNs:    min,
		MedianNs: median,
		P95Ns:    p95,
		MaxNs:    max,
		Count:    n,
	}
}

// RegisterTimeRequestTool registers the tool with the MCP server
func RegisterTimeRequestTool(
	server *mcp.Server, client *caido.Client,
) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "caido_time_request",
		Description: `Send a request N times and measure response timing. ` +
			`roundtrip_ns and every summary/per_payload number is CAIDO's own roundtrip ` +
			`(ms resolution - a 0 means the target answered in under a ` +
			`millisecond, not that the measurement failed; no MCP or SDK ` +
			`overhead). client_total_ns is this tool's ` +
			`wall clock and is quantised by the response poll schedule (50-500ms), ` +
			`so do not compare payloads with it. ` +
			`Use payloads+payloadField for A/B timing (sleep oracle detection). ` +
			`Statistical filtering via median/p95 recommended for sub-second delay detection.`,
	}, timeRequestHandler(client))
}
