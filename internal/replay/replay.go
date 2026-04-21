package replay

import (
	"context"
	"fmt"
	"sync"
	"time"

	caido "github.com/caido-community/sdk-go"
	gen "github.com/caido-community/sdk-go/graphql"
)

const (
	pollInitInterval = 50 * time.Millisecond
	pollMaxInterval  = 500 * time.Millisecond
	PollMaxRetries   = 20
)

var (
	defaultSessionID string
	sessionMu        sync.Mutex
)

func GetOrCreateSession(
	ctx context.Context, client *caido.Client, inputID string,
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
		return "", fmt.Errorf("create replay session: %w", err)
	}
	defaultSessionID = resp.CreateReplaySession.Session.Id
	return defaultSessionID, nil
}

func ResetDefaultSession(newID string) {
	sessionMu.Lock()
	defaultSessionID = newID
	sessionMu.Unlock()
}

func PollForEntry(
	ctx context.Context,
	client *caido.Client,
	sessionID, prevEntryID string,
) (*gen.GetReplayEntryReplayEntry, error) {
	interval := pollInitInterval
	for range PollMaxRetries {
		sessResp, err := client.Replay.GetSession(ctx, sessionID)
		if err != nil {
			return nil, fmt.Errorf("poll session: %w", err)
		}
		sess := sessResp.ReplaySession
		if sess != nil && sess.ActiveEntry != nil &&
			sess.ActiveEntry.Id != prevEntryID {
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
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
		interval = min(interval*2, pollMaxInterval)
	}
	return nil, fmt.Errorf(
		"timed out waiting for response",
	)
}
