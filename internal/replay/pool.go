package replay

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	caido "github.com/caido-community/sdk-go"
)

const (
	// retireTimeout bounds creating a replacement for a retired session. It runs
	// on a detached context because the request that retired the session has
	// usually just lost its own deadline.
	retireTimeout = 10 * time.Second
)

// cleanupTimeout bounds deleting a batch's sessions.
//
// Cleanup runs on a context detached from the caller's - the batch is over
// and its deadline usually with it - and the SDK's http.Client has no
// Timeout of its own, so without this the final round trip of every batch
// was bounded by nothing at all: a Caido that accepts the connection and
// never answers would hold RunBatch open forever, after every result was
// already in hand.
//
// A var, not a const, so a test can shrink it: see sendOverallTimeout.
// Production never assigns it.
var cleanupTimeout = 15 * time.Second

// ErrPoolDrained is returned by Acquire when every session has been retired and
// none could be replaced.
var ErrPoolDrained = errors.New(
	"replay session pool is empty: every session was retired and no " +
		"replacement could be created",
)

// SessionPool manages a pool of replay sessions for parallel sends.
// Each session can only handle one request at a time, so we need N
// sessions for N concurrent requests.
type SessionPool struct {
	client   *caido.Client
	sessions chan string
	mu       sync.Mutex
	created  []string
	// live counts the sessions still circulating or checked out. It can only
	// fall, and only when a retired session could not be replaced.
	live int
	// drained is closed when live reaches zero, which is the only thing that
	// can wake a waiter that will never get a session. Without it, Acquire
	// waited on the context alone - and an MCP tool call frequently has no
	// deadline, so a pool that collapsed left RunBatch's wg.Wait() blocked
	// forever and threw away every result that was already in hand.
	drained chan struct{}
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
		drained:  make(chan struct{}),
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
		pool.live++
	}
	if firstErr != nil {
		pool.Cleanup(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("session pool create: %w", firstErr)
	}

	return pool, nil
}

// Acquire blocks until a session is available, the pool drains, or ctx ends.
func (p *SessionPool) Acquire(ctx context.Context) (string, error) {
	// Checked first and without blocking, so a drained pool that still has a
	// session in flight cannot lose the race to its own closed channel.
	select {
	case id := <-p.sessions:
		return id, nil
	default:
	}
	select {
	case id := <-p.sessions:
		return id, nil
	case <-p.drained:
		return "", ErrPoolDrained
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
		// The pool is now one narrower, permanently. Say so, so that a waiter
		// which can never be served is woken with an error instead of waiting
		// out a deadline it may not have.
		p.mu.Lock()
		p.live--
		if p.live <= 0 {
			select {
			case <-p.drained:
			default:
				close(p.drained)
			}
		}
		p.mu.Unlock()
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
	ctx, cancel := context.WithTimeout(ctx, cleanupTimeout)
	defer cancel()
	_, _ = p.client.Replay.DeleteSessions(ctx, ids)
}
