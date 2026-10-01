package caido

import (
	"context"

	gen "github.com/caido-community/sdk-go/graphql"
)

// StreamSDK provides read access to WebSocket and SSE streams (the Streams
// tab in Caido) and their frames.
type StreamSDK struct {
	client *Client
}

// ListStreamsOptions configures the List query.
//
// Caido 0.57.0 removed the protocol argument from the streams query and
// replaced it with an optional StreamQL filter. To restrict results to a
// protocol, set Filter to a StreamQL query (e.g. `protocol.eq:"ws"`) or
// filter the returned streams by their Protocol field.
type ListStreamsOptions struct {
	First   *int
	Last    *int
	After   *string
	Before  *string
	Filter  *gen.StreamQLInput
	Order   *gen.StreamOrderInput
	ScopeID *string
}

// List returns paginated streams (connections), optionally filtered by a
// StreamQL query.
func (s *StreamSDK) List(
	ctx context.Context, opts *ListStreamsOptions,
) (*gen.ListStreamsResponse, error) {
	var o ListStreamsOptions
	if opts != nil {
		o = *opts
	}
	return gen.ListStreams(
		ctx, s.client.GraphQL,
		o.First, o.Last, o.After, o.Before,
		o.Filter, o.Order, o.ScopeID,
	)
}

// Get returns a single stream by ID.
func (s *StreamSDK) Get(
	ctx context.Context, id string,
) (*gen.GetStreamResponse, error) {
	return gen.GetStream(ctx, s.client.GraphQL, id)
}

// ListWsMessagesOptions configures the WebSocket frame query.
type ListWsMessagesOptions struct {
	First  *int
	Last   *int
	After  *string
	Before *string
	Order  *gen.StreamWsMessageOrderInput
}

// ListWsMessages returns paginated WebSocket frames for a stream. Frame
// content (direction, format, length, raw Blob) lives on each message's
// head field.
func (s *StreamSDK) ListWsMessages(
	ctx context.Context, streamID string, opts *ListWsMessagesOptions,
) (*gen.ListStreamWsMessagesResponse, error) {
	var o ListWsMessagesOptions
	if opts != nil {
		o = *opts
	}
	return gen.ListStreamWsMessages(
		ctx, s.client.GraphQL,
		streamID, o.First, o.Last, o.After, o.Before, o.Order,
	)
}
