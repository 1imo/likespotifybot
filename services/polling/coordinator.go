package polling

import (
	"context"
	"fmt"
	"html"
	"strings"
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

	if !result.GestureEnabled {
		c.track(ctx, telegramID, result.TrackID, "skipped",
			gestureLikeMeta(result, false, false, map[string]any{"reason": "gesture_disabled"}))
		c.sendGestureDisabledHint(telegramID, result)
		return
	}

	if c.debounce != nil && c.debounce.InCooldown(telegramID, result.TrackID) {
		return
	}

	inLibrary, err := c.client.TrackInLibrary(ctx, telegramID, result.TrackID)
	if err != nil {
		if c.log != nil {
			c.log.Warn("event=gesture-like status=error telegram_id=%d step=library_check track_id=%s err=%v",
				telegramID, result.TrackID, err)
		}
	} else if inLibrary {
		c.markLikedQuiet(ctx, telegramID, result.TrackID)
		c.track(ctx, telegramID, result.TrackID, "skipped",
			gestureLikeMeta(result, true, true, map[string]any{"reason": "already_in_library"}))
		if c.log != nil {
			c.log.Info("event=gesture-like status=skipped telegram_id=%d track_id=%s reason=already_in_library",
				telegramID, result.TrackID)
		}
		return
	}

	if c.debounce != nil {
		c.debounce.Mark(telegramID, result.TrackID)
	}

	if err := c.client.SaveTrack(ctx, telegramID, result.TrackID); err != nil {
		if c.log != nil {
			c.log.Warn("event=gesture-like status=error telegram_id=%d track_id=%s source=%s err=%v",
				telegramID, result.TrackID, result.Source, err)
		}
		c.track(ctx, telegramID, result.TrackID, "error",
			gestureLikeMeta(result, true, false, map[string]any{"error": err.Error()}))
		return
	}

	c.markLikedQuiet(ctx, telegramID, result.TrackID)

	if c.log != nil {
		c.log.Info("event=gesture-like status=ok telegram_id=%d track_id=%s source=%s saved=true",
			telegramID, result.TrackID, result.Source)
	}
	c.track(ctx, telegramID, result.TrackID, "ok", gestureLikeMeta(result, true, false, nil))

	notify, err := c.repo.NotifyOnGesture(ctx, telegramID)
	if err != nil || !notify || !c.cfg.NotifyOnGesture || c.bot == nil {
		if c.log != nil && result != nil {
			c.log.Info("event=gesture-like status=ok telegram_id=%d notify_skipped=%v", telegramID, err != nil || !notify || !c.cfg.NotifyOnGesture)
		}
		return
	}
	c.sendSavedTrackNotification(telegramID, result)
}

func (c *Coordinator) sendGestureDisabledHint(telegramID int64, result *gesture.Result) {
	if c.bot == nil {
		return
	}
	hintKey := result.TrackID + ":disabled_hint"
	if c.debounce != nil && c.debounce.InCooldown(telegramID, hintKey) {
		return
	}
	msg := tgbotapi.NewMessage(telegramID, gestureDisabledCaption(result))
	msg.ParseMode = tgbotapi.ModeHTML
	if _, err := c.bot.Send(msg); err != nil {
		if c.log != nil {
			c.log.Warn("event=gesture-like status=error telegram_id=%d step=telegram_disabled_hint err=%v", telegramID, err)
		}
		return
	}
	if c.debounce != nil {
		c.debounce.Mark(telegramID, hintKey)
	}
}

func (c *Coordinator) sendSavedTrackNotification(telegramID int64, result *gesture.Result) {
	caption := savedTrackCaption(result)
	if result.AlbumImageURL != "" {
		photo := tgbotapi.NewPhoto(telegramID, tgbotapi.FileURL(result.AlbumImageURL))
		photo.Caption = caption
		photo.ParseMode = tgbotapi.ModeHTML
		if _, err := c.bot.Send(photo); err == nil {
			return
		} else if c.log != nil {
			c.log.Warn("event=gesture-like status=error telegram_id=%d step=telegram_photo err=%v", telegramID, err)
		}
	}
	msg := tgbotapi.NewMessage(telegramID, caption)
	msg.ParseMode = tgbotapi.ModeHTML
	if _, err := c.bot.Send(msg); err != nil && c.log != nil {
		c.log.Warn("event=gesture-like status=error telegram_id=%d step=telegram_notify err=%v", telegramID, err)
	}
}

func (c *Coordinator) markLikedQuiet(ctx context.Context, telegramID int64, trackID string) {
	if c.debounce != nil {
		c.debounce.Mark(telegramID, trackID)
	}
	_ = c.repo.RecordLikeDebounce(ctx, telegramID, trackID)
}

func formatTrackLine(result *gesture.Result) (titleHTML, byArtistHTML string) {
	name := result.TrackName
	if name == "" {
		name = "this track"
	}
	titleHTML = html.EscapeString(name)
	if result.Artist != "" {
		byArtistHTML = " by " + html.EscapeString(result.Artist)
	}
	return titleHTML, byArtistHTML
}

func gestureDisabledCaption(result *gesture.Result) string {
	title, by := formatTrackLine(result)
	return fmt.Sprintf(
		"We wanted to save <b>%s</b>%s to Your Library, but quick pause / unpause is <b>off</b>.\n\nUse /toggle to enable.",
		title, by,
	)
}

func savedTrackCaption(result *gesture.Result) string {
	title, by := formatTrackLine(result)
	link := html.EscapeString(spotifyTrackURL(result.TrackID))
	linkLine := "\n\n" + link
	return fmt.Sprintf("❤️ Saved <b>%s</b>%s%s", title, by, linkLine)
}

func spotifyTrackURL(trackID string) string {
	id := strings.TrimPrefix(trackID, "spotify:track:")
	return "https://open.spotify.com/track/" + id
}

func emptyTrack(id string) string {
	if id == "" {
		return "-"
	}
	return id
}

func gestureLikeMeta(result *gesture.Result, gestureEnabled, alreadyInLibrary bool, extra map[string]any) map[string]any {
	meta := map[string]any{
		"track_id":           result.TrackID,
		"track_name":         result.TrackName,
		"artist":             result.Artist,
		"source":             result.Source,
		"progress_ms":        result.ProgressMs,
		"track_duration_ms":  result.TrackDurationMs,
		"gesture_enabled":    gestureEnabled,
		"already_in_library": alreadyInLibrary,
	}
	for k, v := range extra {
		meta[k] = v
	}
	return meta
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
