/*
Package utils analytics provides an in-memory queue for events; bg-services/handle-events
persists to app_analytics so update handlers are not blocked on DB inserts.
See analytics.md in the likespotifybot module root for the full event catalog.
*/
package utils

import (
	"context"
	"fmt"
	"time"
)

const analyticsQueueSize = 2048

type AnalyticsEvent struct {
	Name      string
	UserID    int64
	EntityID  string
	Status    string
	Timestamp time.Time
	Meta      map[string]any
}

type Analytics struct {
	log   *Logger
	queue chan AnalyticsEvent
}

type AnalyticsFactory struct{}

var AnalyticsManager = NewAnalyticsFactory()

func NewAnalyticsFactory() *AnalyticsFactory {
	return &AnalyticsFactory{}
}

func (f *AnalyticsFactory) Init(log *Logger) *Analytics {
	return &Analytics{
		log:   log,
		queue: make(chan AnalyticsEvent, analyticsQueueSize),
	}
}

// Next blocks until an event is available or ctx is canceled.
func (a *Analytics) Next(ctx context.Context) (AnalyticsEvent, error) {
	select {
	case <-ctx.Done():
		return AnalyticsEvent{}, ctx.Err()
	case e := <-a.queue:
		return e, nil
	}
}

// TryDequeue returns one queued event if available without blocking.
func (a *Analytics) TryDequeue() (AnalyticsEvent, bool) {
	select {
	case e := <-a.queue:
		return e, true
	default:
		return AnalyticsEvent{}, false
	}
}

// MetaWithChatID builds meta with chat_id set last (Telegram chat where the update occurred). AnalyticsEvent.UserID should be the actor (From.ID).
func MetaWithChatID(chatID int64, fields map[string]any) map[string]any {
	if len(fields) == 0 {
		return map[string]any{"chat_id": chatID}
	}
	out := make(map[string]any, len(fields)+1)
	for k, v := range fields {
		out[k] = v
	}
	out["chat_id"] = chatID
	return out
}

// TrackEvent enqueues an event for async persistence. Returns an error only if the queue is full.
func (a *Analytics) TrackEvent(ctx context.Context, e AnalyticsEvent) error {
	select {
	case a.queue <- e:
		return nil
	default:
		if a.log != nil {
			a.log.Warn("analytics queue full, dropped event %q", e.Name)
		}
		return fmt.Errorf("analytics queue full")
	}
}
