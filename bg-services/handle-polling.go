package bgservices

import (
	"context"
	"time"

	"likespotifybot/services/polling"
	"likespotifybot/utils"
)

// HandlePollingService runs adaptive per-user Spotify playback polling.
type HandlePollingService struct {
	coord *polling.Coordinator
	cfg   utils.SpotifyConfig
	log   *utils.Logger
}

func NewHandlePollingService(coord *polling.Coordinator, cfg utils.SpotifyConfig, log *utils.Logger) *HandlePollingService {
	return &HandlePollingService{coord: coord, cfg: cfg, log: log}
}

func (s *HandlePollingService) Run(ctx context.Context) {
	if s.log != nil {
		s.log.Info("event=polling-worker status=started playing_ms=%d paused_ms=%d inactive_ms=%d tick_ms=%d",
			s.cfg.PollPlaying.Milliseconds(),
			s.cfg.PollPaused.Milliseconds(),
			s.cfg.PollInactive.Milliseconds(),
			s.cfg.PollTick().Milliseconds(),
		)
	}
	ticker := time.NewTicker(s.cfg.PollTick())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			if s.log != nil {
				s.log.Info("event=polling-worker status=stopped")
			}
			return
		case t := <-ticker.C:
			s.coord.RunDue(ctx, t.UTC())
		}
	}
}
