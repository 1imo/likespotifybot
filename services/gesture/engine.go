package gesture

import (
	"context"
	"time"

	"likespotifybot/services/spotify"
	"likespotifybot/utils"
)

// Action is a gesture-triggered side effect.
type Action string

const (
	ActionLikeTrack Action = "like_track"
)

// Result describes a detected gesture.
type Result struct {
	Action  Action
	TrackID string
	Source  string // "edge" or "stall"
}

// Engine interprets playback transitions as gestures.
// TODO: add more gesture rules (double-tap pause, long pause, etc.).
type Engine struct {
	cfg      utils.SpotifyConfig
	repo     *spotify.Repository
	debounce *Debounce
	log      *utils.Logger
}

func NewEngine(cfg utils.SpotifyConfig, repo *spotify.Repository, debounce *Debounce, log *utils.Logger) *Engine {
	return &Engine{cfg: cfg, repo: repo, debounce: debounce, log: log}
}

// Evaluate compares previous and current playback snapshots.
func (e *Engine) Evaluate(
	ctx context.Context,
	telegramID int64,
	prev spotify.PollState,
	cur *spotify.PlaybackSnapshot,
	now time.Time,
) (*Result, spotify.PollState, error) {
	next := prev

	if cur == nil || !cur.DeviceActive {
		next.IsPlaying = false
		next.PausedAt = nil
		next.ProgressMs = 0
		if next.InactiveSince == nil {
			t := now
			next.InactiveSince = &t
		}
		e.logDecision(telegramID, "no_device", "spotify reports no active playback device", nil)
		return nil, next, nil
	}

	next.InactiveSince = nil

	enabled, err := e.repo.QuickPauseLikeEnabled(ctx, telegramID)
	if err != nil || !enabled {
		next.TrackID = cur.TrackID
		next.IsPlaying = cur.IsPlaying
		next.ProgressMs = cur.ProgressMs
		if err != nil {
			e.logDecision(telegramID, "gesture_check_error", err.Error(), nil)
		} else {
			e.logDecision(telegramID, "gesture_disabled", "quick pause like toggled off", nil)
		}
		return nil, next, err
	}

	trackID := cur.TrackID
	if trackID == "" {
		e.logDecision(telegramID, "no_track", "active device but no track id", nil)
		return nil, next, nil
	}

	// Playing → paused: start pause window.
	if prev.IsPlaying && !cur.IsPlaying {
		t := now
		next.PausedAt = &t
		next.TrackID = trackID
		next.IsPlaying = false
		next.ProgressMs = cur.ProgressMs
		e.logDecision(telegramID, "pause_started", "playing to paused", map[string]any{
			"track_id": trackID, "progress_ms": cur.ProgressMs,
		})
		return nil, next, nil
	}

	// Still paused: ensure pause clock exists for adaptive polling.
	if !cur.IsPlaying {
		if next.PausedAt == nil {
			t := now
			next.PausedAt = &t
		}
		next.TrackID = trackID
		next.IsPlaying = false
		next.ProgressMs = cur.ProgressMs
		pauseFor := now.Sub(*next.PausedAt).Milliseconds()
		e.logDecision(telegramID, "still_paused", "waiting for resume", map[string]any{
			"track_id": trackID, "pause_ms": pauseFor,
		})
		return nil, next, nil
	}

	// Paused → playing quickly: quick pause → resume = like.
	if !prev.IsPlaying && cur.IsPlaying && prev.PausedAt != nil && prev.TrackID == trackID {
		pauseDur := now.Sub(*prev.PausedAt)
		if pauseDur > 0 && pauseDur <= e.cfg.QuickPauseMax {
			res, ok, skipReason, err := e.tryLike(ctx, telegramID, trackID, "edge", next, cur)
			if err != nil {
				return nil, next, err
			}
			if ok {
				e.logDecision(telegramID, "like_edge", "quick pause resume", map[string]any{
					"track_id": trackID, "pause_ms": pauseDur.Milliseconds(),
				})
				return res, next, nil
			}
			e.logDecision(telegramID, "like_skipped", skipReason, map[string]any{"source": "edge", "pause_ms": pauseDur.Milliseconds()})
		} else {
			e.logDecision(telegramID, "resume_too_slow", "pause exceeded quick window", map[string]any{
				"pause_ms": pauseDur.Milliseconds(), "max_ms": e.cfg.QuickPauseMax.Milliseconds(),
			})
		}
	}

	// Both polls show playing: infer pause/resume in the sleep gap from progress stall.
	if prev.IsPlaying && cur.IsPlaying && prev.TrackID == trackID && !prev.LastPolledAt.IsZero() {
		stall := e.inferredStallMs(prev, cur, now)
		wallMs := now.Sub(prev.LastPolledAt).Milliseconds()
		progressDelta := cur.ProgressMs - prev.ProgressMs
		if stall > 0 && stall <= e.cfg.QuickPauseMax {
			res, ok, skipReason, err := e.tryLike(ctx, telegramID, trackID, "stall", next, cur)
			if err != nil {
				return nil, next, err
			}
			if ok {
				e.logDecision(telegramID, "like_stall", "progress stall inferred pause in poll gap", map[string]any{
					"track_id": trackID, "stall_ms": stall.Milliseconds(),
					"wall_ms": wallMs, "progress_delta_ms": progressDelta,
				})
				return res, next, nil
			}
			e.logDecision(telegramID, "like_skipped", skipReason, map[string]any{
				"source": "stall", "stall_ms": stall.Milliseconds(),
			})
		} else if stall > 0 {
			e.logDecision(telegramID, "stall_too_long", "inferred pause over quick window", map[string]any{
				"stall_ms": stall.Milliseconds(), "max_ms": e.cfg.QuickPauseMax.Milliseconds(),
				"wall_ms": wallMs, "progress_delta_ms": progressDelta,
			})
		}
	}

	next.TrackID = trackID
	next.IsPlaying = cur.IsPlaying
	next.ProgressMs = cur.ProgressMs
	if cur.IsPlaying {
		next.PausedAt = nil
	}
	e.logDecision(telegramID, "no_gesture", "playing unchanged", map[string]any{
		"track_id": trackID, "progress_ms": cur.ProgressMs,
	})
	return nil, next, nil
}

func (e *Engine) tryLike(
	ctx context.Context,
	telegramID int64,
	trackID, source string,
	next spotify.PollState,
	cur *spotify.PlaybackSnapshot,
) (*Result, bool, string, error) {
	if e.debounce != nil && e.debounce.InCooldown(telegramID, trackID) {
		next.IsPlaying = true
		next.PausedAt = nil
		next.ProgressMs = cur.ProgressMs
		return nil, false, "in_memory_cooldown", nil
	}
	since := time.Now().UTC().Add(-e.cfg.GestureCooldown)
	recent, err := e.repo.RecentlyLiked(ctx, telegramID, trackID, since)
	if err != nil {
		return nil, false, "", err
	}
	if recent {
		next.IsPlaying = true
		next.PausedAt = nil
		return nil, false, "recently_liked_db", nil
	}
	next.IsPlaying = cur.IsPlaying
	next.PausedAt = nil
	next.ProgressMs = cur.ProgressMs
	return &Result{Action: ActionLikeTrack, TrackID: trackID, Source: source}, true, "", nil
}

func (e *Engine) logDecision(telegramID int64, outcome, detail string, fields map[string]any) {
	if e.log == nil {
		return
	}
	if fields == nil {
		e.log.Info("event=gesture-eval telegram_id=%d outcome=%s detail=%s", telegramID, outcome, detail)
		return
	}
	// Flatten a few common fields into the log line for readability.
	trackID, _ := fields["track_id"].(string)
	e.log.Info("event=gesture-eval telegram_id=%d outcome=%s detail=%s track_id=%s fields=%v",
		telegramID, outcome, detail, trackID, fields)
}

// inferredStallMs estimates pause duration when both samples are "playing" but progress advanced too slowly.
func (e *Engine) inferredStallMs(prev spotify.PollState, cur *spotify.PlaybackSnapshot, now time.Time) time.Duration {
	wall := now.Sub(prev.LastPolledAt)
	if wall < 150*time.Millisecond {
		return 0
	}
	wallMs := wall.Milliseconds()
	progressDelta := int64(cur.ProgressMs - prev.ProgressMs)
	if progressDelta < 0 {
		return 0
	}
	expectedMin := int64(float64(wallMs) * e.cfg.StallSlackRatio)
	stallMs := expectedMin - progressDelta
	if stallMs <= 0 {
		return 0
	}
	return time.Duration(stallMs) * time.Millisecond
}
