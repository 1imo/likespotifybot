package polling

import (
	"context"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"likespotifybot/services/gesture"
	"likespotifybot/services/spotify"
	"likespotifybot/utils"
)

// Coordinator polls Spotify playback for connected users with per-user adaptive intervals.
type Coordinator struct {
	cfg       utils.SpotifyConfig
	repo      *spotify.Repository
	client    *spotify.Client
	engine    *gesture.Engine
	debounce  *gesture.Debounce
	bot       *tgbotapi.BotAPI
	analytics *utils.Analytics
	log       *utils.Logger

	mu       sync.Mutex
	nextPoll map[int64]time.Time
}

func NewCoordinator(
	cfg utils.SpotifyConfig,
	repo *spotify.Repository,
	client *spotify.Client,
	engine *gesture.Engine,
	debounce *gesture.Debounce,
	bot *tgbotapi.BotAPI,
	analytics *utils.Analytics,
	log *utils.Logger,
) *Coordinator {
	return &Coordinator{
		cfg:       cfg,
		repo:      repo,
		client:    client,
		engine:    engine,
		debounce:  debounce,
		bot:       bot,
		analytics: analytics,
		log:       log,
		nextPoll:  make(map[int64]time.Time),
	}
}

func (c *Coordinator) RunDue(ctx context.Context, now time.Time) {
	ids, err := c.repo.ListConnectedTelegramIDs(ctx)
	if err != nil {
		if c.log != nil {
			c.log.Error("event=poll-run status=error step=list_users err=%v", err)
		}
		return
	}
	for _, tid := range ids {
		if !c.isDue(tid, now) {
			continue
		}
		c.pollUser(ctx, tid, now)
	}
}

func (c *Coordinator) isDue(telegramID int64, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	at, ok := c.nextPoll[telegramID]
	return !ok || !at.After(now)
}

func (c *Coordinator) scheduleNext(telegramID int64, at time.Time) {
	c.mu.Lock()
	c.nextPoll[telegramID] = at
	c.mu.Unlock()
}

func (c *Coordinator) pollUser(ctx context.Context, telegramID int64, now time.Time) {
	prev, err := c.repo.LoadPollState(ctx, telegramID)
	if err != nil {
		if c.log != nil {
			c.log.Warn("event=poll-user status=error step=load_state telegram_id=%d err=%v", telegramID, err)
		}
		c.scheduleNext(telegramID, now.Add(c.cfg.PollPaused))
		return
	}

	snap, err := c.client.GetPlayback(ctx, telegramID)
	if err != nil {
		if c.log != nil {
			c.log.Warn("event=poll-user status=error step=spotify_api telegram_id=%d err=%v", telegramID, err)
		}
		c.scheduleNext(telegramID, now.Add(c.cfg.PollPaused))
		return
	}

	if c.log != nil {
		wallMs := int64(0)
		progressDelta := 0
		if !prev.LastPolledAt.IsZero() {
			wallMs = now.Sub(prev.LastPolledAt).Milliseconds()
			progressDelta = snap.ProgressMs - prev.ProgressMs
		}
		c.log.Info(
			"event=poll-user status=snapshot telegram_id=%d device_active=%v playing=%v track_id=%s progress_ms=%d prev_playing=%v prev_progress_ms=%d wall_ms=%d progress_delta_ms=%d",
			telegramID, snap.DeviceActive, snap.IsPlaying, emptyTrack(snap.TrackID), snap.ProgressMs,
			prev.IsPlaying, prev.ProgressMs, wallMs, progressDelta,
		)
	}

	result, next, err := c.engine.Evaluate(ctx, telegramID, prev, snap, now)
	if err != nil {
		if c.log != nil {
			c.log.Warn("event=poll-user status=error step=gesture_eval telegram_id=%d err=%v", telegramID, err)
		}
		c.scheduleNext(telegramID, now.Add(c.cfg.PollPaused))
		return
	}

	next.LastPolledAt = now
	if err := c.repo.SavePollState(ctx, telegramID, next); err != nil && c.log != nil {
		c.log.Warn("event=poll-user status=error step=save_state telegram_id=%d err=%v", telegramID, err)
	}

	interval, reason := pollSchedule(c.cfg, next, snap, now)
	c.scheduleNext(telegramID, now.Add(interval))

	if c.log != nil {
		outcome := "no_like"
		if result != nil && result.Action == gesture.ActionLikeTrack {
			outcome = "like_" + result.Source
		}
		c.log.Info(
			"event=poll-user status=done telegram_id=%d outcome=%s next_poll_ms=%d schedule_reason=%s",
			telegramID, outcome, interval.Milliseconds(), reason,
		)
	}

	if result == nil || result.Action != gesture.ActionLikeTrack {
		return
	}

	if err := c.client.SaveTrack(ctx, telegramID, result.TrackID); err != nil {
		if c.log != nil {
			c.log.Warn("event=gesture-like status=error telegram_id=%d track_id=%s source=%s err=%v",
				telegramID, result.TrackID, result.Source, err)
		}
		c.track(ctx, telegramID, result.TrackID, "error", map[string]any{"error": err.Error(), "source": result.Source})
		return
	}

	if c.debounce != nil {
		c.debounce.Mark(telegramID, result.TrackID)
	}
	_ = c.repo.RecordLikeDebounce(ctx, telegramID, result.TrackID)

	if c.log != nil {
		c.log.Info("event=gesture-like status=ok telegram_id=%d track_id=%s source=%s saved=true",
			telegramID, result.TrackID, result.Source)
	}
	c.track(ctx, telegramID, result.TrackID, "ok", map[string]any{"source": result.Source})

	notify, err := c.repo.NotifyOnGesture(ctx, telegramID)
	if err != nil || !notify || !c.cfg.NotifyOnGesture || c.bot == nil {
		if c.log != nil && result != nil {
			c.log.Info("event=gesture-like status=ok telegram_id=%d notify_skipped=%v", telegramID, err != nil || !notify || !c.cfg.NotifyOnGesture)
		}
		return
	}
	msg := tgbotapi.NewMessage(telegramID, "❤️ Saved current track")
	if _, err := c.bot.Send(msg); err != nil && c.log != nil {
		c.log.Warn("event=gesture-like status=error telegram_id=%d step=telegram_notify err=%v", telegramID, err)
	}
}

func emptyTrack(id string) string {
	if id == "" {
		return "-"
	}
	return id
}

func (c *Coordinator) track(ctx context.Context, userID int64, trackID, status string, meta map[string]any) {
	if c.analytics == nil {
		return
	}
	if meta == nil {
		meta = map[string]any{}
	}
	meta["track_id"] = trackID
	_ = c.analytics.TrackEvent(ctx, utils.AnalyticsEvent{
		Name:     "gesture-like",
		UserID:   userID,
		EntityID: trackID,
		Status:   status,
		Meta:     meta,
	})
}
