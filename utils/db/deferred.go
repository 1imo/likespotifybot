package db

import (
	"context"
	"fmt"
	"time"

	"likespotifybot/utils"
)

const deferredWriteQueueSize = 512
const deferredWriteTimeout = 20 * time.Second
const deferredWriteSlowThreshold = 500 * time.Millisecond

// DeferredWriteQueue holds DB work off the hot path (Telegram update handler). A single worker
// runs Enqueued functions serially so Supabase round-trips do not block replies.
type DeferredWriteQueue struct {
	ch chan func(context.Context, DB) error
}

func NewDeferredWriteQueue() *DeferredWriteQueue {
	return &DeferredWriteQueue{
		ch: make(chan func(context.Context, DB) error, deferredWriteQueueSize),
	}
}

// Enqueue schedules a write. Returns an error if the buffer is full (caller should fall back to sync DB).
func (q *DeferredWriteQueue) Enqueue(fn func(context.Context, DB) error) error {
	if q == nil || fn == nil {
		return fmt.Errorf("deferred write: nil queue or fn")
	}
	select {
	case q.ch <- fn:
		return nil
	default:
		return fmt.Errorf("deferred write queue full")
	}
}

// Run processes queued writes until ctx is canceled. Intended as one goroutine from app.go.
func (q *DeferredWriteQueue) Run(ctx context.Context, store DB, log *utils.Logger) {
	if q == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			if log != nil {
				log.Info("event=deferred-write-worker status=stopped pending=%d", len(q.ch))
			}
			return
		case fn := <-q.ch:
			started := time.Now()
			runCtx, cancel := context.WithTimeout(context.Background(), deferredWriteTimeout)
			err := fn(runCtx, store)
			cancel()
			if log != nil {
				durationMs := time.Since(started).Milliseconds()
				pending := len(q.ch)
				if err != nil {
					log.Warn("event=deferred-write status=error duration_ms=%d pending=%d err=%v", durationMs, pending, err)
					continue
				}
				if time.Since(started) >= deferredWriteSlowThreshold {
					log.Warn("event=deferred-write status=slow duration_ms=%d pending=%d", durationMs, pending)
				}
			}
		}
	}
}
