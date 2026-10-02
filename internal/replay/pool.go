package replay

import (
	"context"
	"fmt"
	"sync"
	"time"

	caido "github.com/caido-community/sdk-go"
)

// retireTimeout bounds creating a replacement for a retired session. It runs
// on a detached context because the request that retired the session has
// usually just lost its own deadline.
const retireTimeout = 10 * time.Second

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

			id, _, err := NewSession(ctx, client)
			if err != nil {
				results[idx] = result{err: err}
				return
			}
			results[idx] = result{id: id}
		}(i)
	}
	wg.Wait()

	// Record every session that WAS created before deciding whether the pool
	// as a whole succeeded. Returning nil on the first error used to leave the
	// successful ones behind in Caido, where they are visible in the
	// operator's Replay list and nothing ever deletes them — a 20-request
	// batch that failed on the last create orphaned 19 sessions per attempt.
	var firstErr error
	for _, r := range results {
		if r.err != nil {
			if firstErr == nil {
				firstErr = r.err
			}
			continue
		}
		pool.sessions <- r.id
		pool.created = append(pool.created, r.id)
	}
	if firstErr != nil {
		pool.Cleanup(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("session pool create: %w", firstErr)
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

// Release returns a session to the pool. Only a request that is provably
// finished with a session may call this: a session whose task may still be
// running will answer the NEXT request's startReplayTask with TaskInProgress
// and then hand it the previous request's response.
func (p *SessionPool) Release(id string) {
	p.sessions <- id
}

// Retire drops a session that may still have a task running on it and puts a
// fresh one in its place, so the pool keeps its width. The retired session is
// left in `created`, so Cleanup still deletes it at the end of the batch.
//
// If the replacement cannot be created the pool shrinks by one. That is
// deliberate: a narrower pool makes later requests wait or fail to acquire,
// whereas putting the busy session back makes them silently inherit it.
func (p *SessionPool) Retire(ctx context.Context, id string) {
	ctx, cancel := context.WithTimeout(
		context.WithoutCancel(ctx), retireTimeout,
	)
	defer cancel()

	newID, _, err := NewSession(ctx, p.client)
	if err != nil {
		return
	}
	p.mu.Lock()
	p.created = append(p.created, newID)
	p.mu.Unlock()
	p.sessions <- newID
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
