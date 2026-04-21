package replay

import (
	"context"
	"fmt"
	"sync"

	caido "github.com/caido-community/sdk-go"
	gen "github.com/caido-community/sdk-go/graphql"
)

// SessionPool manages a pool of replay sessions for parallel sends.
// Each session can only handle one request at a time, so we need N
// sessions for N concurrent requests.
type SessionPool struct {
	client   *caido.Client
	sessions chan string
	mu       sync.Mutex
	created  []string
}

// NewSessionPool creates a pool pre-filled with n replay sessions.
func NewSessionPool(
	ctx context.Context, client *caido.Client, n int,
) (*SessionPool, error) {
	if n < 1 {
		n = 1
	}
	if n > 50 {
		n = 50
	}

	pool := &SessionPool{
		client:   client,
		sessions: make(chan string, n),
		created:  make([]string, 0, n),
	}

	// Create sessions in parallel, bounded by 5 concurrent creates.
	type result struct {
		id  string
		err error
	}
	results := make([]result, n)
	sem := make(chan struct{}, 5)
	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			resp, err := client.Replay.CreateSession(
				ctx, &gen.CreateReplaySessionInput{},
			)
			if err != nil {
				results[idx] = result{err: err}
				return
			}
			results[idx] = result{
				id: resp.CreateReplaySession.Session.Id,
			}
		}(i)
	}
	wg.Wait()

	for _, r := range results {
		if r.err != nil {
			return nil, fmt.Errorf("session pool create: %w", r.err)
		}
		pool.sessions <- r.id
		pool.created = append(pool.created, r.id)
	}

	return pool, nil
}

// Acquire blocks until a session is available.
func (p *SessionPool) Acquire(ctx context.Context) (string, error) {
	select {
	case id := <-p.sessions:
		return id, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// Release returns a session to the pool.
func (p *SessionPool) Release(id string) {
	p.sessions <- id
}

// Size returns the number of sessions created.
func (p *SessionPool) Size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.created)
}

// Cleanup deletes all sessions created by this pool.
func (p *SessionPool) Cleanup(ctx context.Context) {
	p.mu.Lock()
	ids := make([]string, len(p.created))
	copy(ids, p.created)
	p.mu.Unlock()

	if len(ids) == 0 {
		return
	}
	_, _ = p.client.Replay.DeleteSessions(ctx, ids)
}
