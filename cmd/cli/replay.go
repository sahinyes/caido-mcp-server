package main

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/c0tton-fluff/caido-mcp-server/internal/httputil"
	"github.com/c0tton-fluff/caido-mcp-server/internal/replay"
	caido "github.com/caido-community/sdk-go"
)

// sendReplay sends a CRLF-normalized raw HTTP request via the Replay API
// and returns the terse-formatted response string.
func sendReplay(
	ctx context.Context,
	client *caido.Client,
	raw, host string,
	port int, useTLS bool,
	bodyLimit int, allHeaders bool,
) (string, error) {
	rawB64 := base64.StdEncoding.EncodeToString([]byte(raw))
	conn := caido.ReplayConnection{Host: host, Port: port, IsTLS: useTLS}

	// The busy/not-busy classification this used to do by hand was inverted in
	// both directions: ANY payload error counted as "session busy" and was
	// retried on a fresh session (so a permission refusal looked like
	// contention), while a transport error could never be busy because it
	// matched on the string "TaskInProgressUserError", which a Go error from
	// StartTask never contains - the refusal arrives in the PAYLOAD. replay.Send
	// classifies it from the payload's own type.
	outcome, err := replay.Send(ctx, client, replay.SendOptions{
		Conn:                conn,
		RawBase64:           rawB64,
		UpdateContentLength: true,
	})
	if err != nil {
		return "", err
	}
	if outcome.PollErr != nil {
		return "", fmt.Errorf(
			"no response yet: %w (session %s)",
			outcome.PollErr, outcome.SessionID,
		)
	}
	entry := outcome.Entry

	if entry.Request == nil || entry.Request.Response == nil {
		return "", fmt.Errorf("no response received")
	}

	resp := httputil.ParseBase64(
		entry.Request.Response.Raw, true, true, 0, bodyLimit,
	)
	return fmtResp(resp, allHeaders) + "\n", nil
}
