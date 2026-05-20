/*
bg-services/handle-broadcast runs the broadcast_outgoing sender on an interval.
Started from app.go when APP_ENV is live or test. zz-ops/convert-broadcasts-yml loads scheduled rows.
*/

package bgservices

import (
	"context"
	"time"

	"likespotifybot/utils"
)

type BroadcastRunner interface {
	RunOnce() error
}

type HandleBroadcastService struct {
	runner BroadcastRunner
	log    *utils.Logger
}

func NewHandleBroadcastService(runner BroadcastRunner, log *utils.Logger) *HandleBroadcastService {
	return &HandleBroadcastService{runner: runner, log: log}
}

func (s *HandleBroadcastService) RunEvery(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.runner.RunOnce(); err != nil && s.log != nil {
				s.log.Error("broadcast run failed: %v", err)
			}
		}
	}
}
