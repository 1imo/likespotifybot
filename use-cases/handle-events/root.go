// Package handleevents drains the in-memory analytics queue and inserts into app_analytics.
// Started from bg-services/handle-events; keeps update handlers off the DB insert path.
package handleevents

import (
	"context"
	"encoding/json"
	"time"

	"likespotifybot/utils"
	"likespotifybot/utils/db"
)

const insertTimeout = 15 * time.Second
const analyticsInsertSlowThreshold = 400 * time.Millisecond

type RootUseCase struct {
	store db.DB
	log   *utils.Logger
	q     *utils.Analytics
}

func NewRootUseCase(store db.DB, q *utils.Analytics, log *utils.Logger) *RootUseCase {
	return &RootUseCase{store: store, q: q, log: log}
}

func (u *RootUseCase) Run(ctx context.Context) {
	if u.log != nil {
		u.log.Info("event=analytics-worker status=started")
	}
	for {
		e, err := u.q.Next(ctx)
		if err != nil {
			flushed := u.flushPending()
			if u.log != nil {
				u.log.Info("event=analytics-worker status=stopped flushed=%d", flushed)
			}
			return
		}
		u.persist(e)
	}
}

func (u *RootUseCase) flushPending() int {
	n := 0
	for {
		e, ok := u.q.TryDequeue()
		if !ok {
			return n
		}
		u.persist(e)
		n++
	}
}

func (u *RootUseCase) persist(e utils.AnalyticsEvent) {
	started := time.Now()
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}

	meta, err := json.Marshal(e.Meta)
	if err != nil {
		if u.log != nil {
			u.log.Warn("analytics marshal meta: %v", err)
		}
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), insertTimeout)
	defer cancel()

	_, err = u.store.ExecContext(
		ctx,
		`INSERT INTO app_analytics (event_name, user_id, entity_id, event_status, event_at, meta)
		 VALUES ($1, $2, $3, $4, $5, $6::jsonb)`,
		e.Name, e.UserID, e.EntityID, e.Status, e.Timestamp, string(meta),
	)
	if u.log != nil {
		durationMs := time.Since(started).Milliseconds()
		if err != nil {
			u.log.Warn("event=analytics-insert status=error duration_ms=%d event_name=%s err=%v", durationMs, e.Name, err)
			return
		}
		if time.Since(started) >= analyticsInsertSlowThreshold {
			u.log.Warn("event=analytics-insert status=slow duration_ms=%d event_name=%s", durationMs, e.Name)
		}
	}
}
