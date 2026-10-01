package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

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
	sessionID, err := replay.GetOrCreateSession(ctx, client, "")
	if err != nil {
		return "", err
	}

	rawB64 := base64.StdEncoding.EncodeToString([]byte(raw))
	conn := caido.ReplayConnection{Host: host, Port: port, IsTLS: useTLS}

	taskResp, sendState, err := replay.SendRaw(
		ctx, client, sessionID, conn, rawB64, true, false,
	)
	hasError := taskResp != nil &&
		taskResp.StartReplayTask.GetError() != nil
	if err != nil || hasError {
		isTaskBusy := false
		if err != nil {
			isTaskBusy = strings.Contains(
				err.Error(), "TaskInProgressUserError",
			)
		} else {
			isTaskBusy = true
		}

		if isTaskBusy {
			newSessionID, _, createErr := replay.NewSession(ctx, client)
			if createErr != nil {
				return "", fmt.Errorf(
					"fallback session: %w", createErr,
				)
			}
			sessionID = newSessionID
			replay.ResetDefaultSession(sessionID)
			_, sendState, err = replay.SendRaw(
				ctx, client, sessionID, conn, rawB64, true, false,
			)
			if err != nil {
				return "", fmt.Errorf("send retry: %w", err)
			}
		} else if err != nil {
			return "", fmt.Errorf("send: %w", err)
		}
	}

	entry, err := replay.PollForEntry(ctx, client, sendState)
	if err != nil {
		return "", err
	}

	if entry.Request == nil || entry.Request.Response == nil {
		return "", fmt.Errorf("no response received")
	}

	resp := httputil.ParseBase64(
		entry.Request.Response.Raw, true, true, 0, bodyLimit,
	)
	return fmtResp(resp, allHeaders) + "\n", nil
}
