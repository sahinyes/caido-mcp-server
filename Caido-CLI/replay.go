package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"time"

	caido "github.com/caido-community/sdk-go"
	gen "github.com/caido-community/sdk-go/graphql"
)

var (
	defaultSessionID string
	sessionMu        sync.Mutex
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
	sessionID, err := getOrCreateSession(ctx, client, "")
	if err != nil {
		return "", err
	}

	var prevEntryID string
	sessResp, err := client.Replay.GetSession(ctx, sessionID)
	if err == nil && sessResp.ReplaySession != nil &&
		sessResp.ReplaySession.ActiveEntry != nil {
		prevEntryID = sessResp.ReplaySession.ActiveEntry.Id
	}

	rawB64 := base64.StdEncoding.EncodeToString([]byte(raw))
	taskInput := &gen.StartReplayTaskInput{
		Connection: &gen.ConnectionInfoInput{
			Host:  host,
			Port:  port,
			IsTLS: useTLS,
		},
		Raw: rawB64,
		Settings: &gen.ReplayEntrySettingsInput{
			Placeholders:        []*gen.ReplayPlaceholderInput{},
			UpdateContentLength: true,
			ConnectionClose:     false,
		},
	}

	taskResp, err := client.Replay.SendRequest(
		ctx, sessionID, taskInput,
	)
	if err != nil || (taskResp != nil &&
		taskResp.StartReplayTask != nil &&
		taskResp.StartReplayTask.Error != nil) {
		isTaskBusy := false
		if err != nil {
			isTaskBusy = strings.Contains(
				err.Error(), "TaskInProgressUserError",
			)
		} else {
			isTaskBusy = true
		}

		if isTaskBusy {
			newResp, createErr := client.Replay.CreateSession(
				ctx, &gen.CreateReplaySessionInput{},
			)
			if createErr != nil {
				return "", fmt.Errorf(
					"fallback session: %w", createErr,
				)
			}
			sessionID = newResp.CreateReplaySession.Session.Id
			sessionMu.Lock()
			defaultSessionID = sessionID
			sessionMu.Unlock()
			prevEntryID = ""
			_, err = client.Replay.SendRequest(
				ctx, sessionID, taskInput,
			)
			if err != nil {
				return "", fmt.Errorf("send retry: %w", err)
			}
		} else if err != nil {
			return "", fmt.Errorf("send: %w", err)
		}
	}

	entry, err := pollForEntry(ctx, client, sessionID, prevEntryID)
	if err != nil {
		return "", err
	}

	if entry.Request == nil || entry.Request.Response == nil {
		return "", fmt.Errorf("no response received")
	}

	resp := parseRawBase64(
		entry.Request.Response.Raw, true, true, 0, bodyLimit,
	)
	return fmtResp(resp, allHeaders) + "\n", nil
}

func getOrCreateSession(
	ctx context.Context,
	client *caido.Client,
	inputID string,
) (string, error) {
	if inputID != "" {
		return inputID, nil
	}
	sessionMu.Lock()
	defer sessionMu.Unlock()
	if defaultSessionID != "" {
		return defaultSessionID, nil
	}
	resp, err := client.Replay.CreateSession(
		ctx, &gen.CreateReplaySessionInput{},
	)
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	defaultSessionID = resp.CreateReplaySession.Session.Id
	return defaultSessionID, nil
}

func pollForEntry(
	ctx context.Context,
	client *caido.Client,
	sessionID, prevEntryID string,
) (*gen.GetReplayEntryReplayEntry, error) {
	for i := 0; i < 20; i++ {
		sessResp, err := client.Replay.GetSession(ctx, sessionID)
		if err != nil {
			return nil, fmt.Errorf("poll: %w", err)
		}
		sess := sessResp.ReplaySession
		if sess == nil || sess.ActiveEntry == nil {
			goto wait
		}
		if sess.ActiveEntry.Id == prevEntryID {
			goto wait
		}
		{
			entryResp, err := client.Replay.GetEntry(
				ctx, sess.ActiveEntry.Id,
			)
			if err != nil {
				return nil, fmt.Errorf("poll entry: %w", err)
			}
			e := entryResp.ReplayEntry
			if e != nil && e.Request != nil &&
				e.Request.Response != nil {
				return e, nil
			}
		}
	wait:
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return nil, fmt.Errorf("timed out waiting for response (10s)")
}

// normalizeCRLF handles literal \r\n escapes and ensures proper line endings.
func normalizeCRLF(raw string) string {
	raw = strings.ReplaceAll(raw, `\r\n`, "\r\n")
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\n", "\r\n")
	if !strings.HasSuffix(raw, "\r\n\r\n") {
		if strings.HasSuffix(raw, "\r\n") {
			raw += "\r\n"
		} else {
			raw += "\r\n\r\n"
		}
	}
	return raw
}

func parseHostHeader(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(line), "host:") {
			return strings.TrimSpace(line[5:])
		}
	}
	return ""
}
