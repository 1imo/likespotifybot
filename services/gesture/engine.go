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
	Action        Action
	TrackID       string
	TrackName     string
	Artist        string
	AlbumImageURL string
	ProgressMs     int // playhead position when the gesture fired
	TrackDurationMs int
	GestureEnabled bool
	Source         string // "edge" or "stall"
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

	gestureEnabled, err := e.repo.QuickPauseLikeEnabled(ctx, telegramID)
	if err != nil {
		next.TrackID = cur.TrackID
		next.IsPlaying = cur.IsPlaying
		next.ProgressMs = cur.ProgressMs
		e.logDecision(telegramID, "gesture_check_error", err.Error(), nil)
		return nil, next, err
	}

	trackID := cur.TrackID
	if trackID == "" {
		e.logDecision(telegramID, "no_track", "active device but no track id", nil)
		return nil, next, nil
	}

	if prev.TrackID != "" && prev.TrackID != trackID {
		next.PauseConfirmed = false
		next.PausedAt = nil
	}
	if prev.IsPlaying {
		next.PausedAt = nil
		next.PauseConfirmed = false
	}

	// Playing → paused: start pause window.
	if prev.IsPlaying && !cur.IsPlaying {
		t := now
		next.PausedAt = &t
		next.PauseConfirmed = false
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
		next.PauseConfirmed = true
		pauseFor := now.Sub(*next.PausedAt).Milliseconds()
		e.logDecision(telegramID, "still_paused", "waiting for resume", map[string]any{
			"track_id": trackID, "pause_ms": pauseFor,
		})
		return nil, next, nil
	}

	// Paused → playing quickly: quick pause → resume = like.
	if !prev.IsPlaying && cur.IsPlaying && prev.PausedAt != nil && prev.TrackID == trackID {
		pauseDur := now.Sub(*prev.PausedAt)
		progressDuringPause := cur.ProgressMs - prev.ProgressMs
		maxPause := e.quickPauseMaxEffective()
		if e.inTrackStartGrace(cur.ProgressMs) {
			e.logDecision(telegramID, "track_start_grace", "resume in first seconds of track", map[string]any{
				"track_id": trackID, "progress_ms": cur.ProgressMs,
				"grace_ms": e.cfg.TrackStartGrace.Milliseconds(),
			})
		} else if pauseDur > 0 && pauseDur <= maxPause && e.edgeResumeQualifies(prev, pauseDur, progressDuringPause) {
			res, ok, skipReason, err := e.tryLike(ctx, telegramID, trackID, "edge", next, cur)
			if err != nil {
				return nil, next, err
			}
			if ok {
				res.GestureEnabled = gestureEnabled
				if !gestureEnabled {
					e.logDecision(telegramID, "like_gesture_disabled", "quick pause detected but toggle is off", map[string]any{
						"track_id": trackID, "pause_ms": pauseDur.Milliseconds(),
					})
					return res, next, nil
				}
				e.logDecision(telegramID, "like_edge", "quick pause resume", map[string]any{
					"track_id": trackID, "pause_ms": pauseDur.Milliseconds(),
					"progress_during_pause_ms": progressDuringPause,
				})
				return res, next, nil
			}
			e.logDecision(telegramID, "like_skipped", skipReason, map[string]any{"source": "edge", "pause_ms": pauseDur.Milliseconds()})
		} else if pauseDur > maxPause {
			e.logDecision(telegramID, "resume_too_slow", "pause exceeded quick window", map[string]any{
				"pause_ms": pauseDur.Milliseconds(), "max_ms": maxPause.Milliseconds(),
			})
		} else if pauseDur > 0 {
			e.logDecision(telegramID, "resume_unconfirmed", "playing reported before pause confirmed on prior poll", map[string]any{
				"pause_ms": pauseDur.Milliseconds(), "fast_resume_max_ms": e.cfg.EdgeFastResumeMax.Milliseconds(),
				"pause_confirmed": prev.PauseConfirmed, "progress_during_pause_ms": progressDuringPause,
			})
		}
	}

	// Both polls show playing: infer pause/resume in the sleep gap from progress stall (optional).
	if e.cfg.StallEnabled && prev.IsPlaying && cur.IsPlaying && prev.TrackID == trackID && !prev.LastPolledAt.IsZero() {
		stall := e.inferredStallMs(prev, cur, now)
		wallMs := now.Sub(prev.LastPolledAt).Milliseconds()
		progressDelta := cur.ProgressMs - prev.ProgressMs
		if e.inTrackStartGrace(prev.ProgressMs) || e.inTrackStartGrace(cur.ProgressMs) {
			e.logDecision(telegramID, "track_start_grace", "stall ignored in first seconds of track", map[string]any{
				"track_id": trackID, "prev_progress_ms": prev.ProgressMs, "progress_ms": cur.ProgressMs,
				"grace_ms": e.cfg.TrackStartGrace.Milliseconds(),
			})
		} else if wallMs < e.stallMinWall().Milliseconds() {
			e.logDecision(telegramID, "stall_wall_too_short", "poll gap too short for stall gesture", map[string]any{
				"track_id": trackID, "wall_ms": wallMs, "min_wall_ms": e.stallMinWall().Milliseconds(),
				"stall_ms": stall.Milliseconds(),
			})
		} else if stall > 0 && stall < e.cfg.StallMin {
			e.logDecision(telegramID, "stall_too_small", "inferred pause below minimum", map[string]any{
				"track_id": trackID, "stall_ms": stall.Milliseconds(),
				"min_stall_ms": e.cfg.StallMin.Milliseconds(), "wall_ms": wallMs,
				"progress_delta_ms": progressDelta,
			})
		} else if stall >= e.cfg.StallMin && stall <= e.quickPauseMaxEffective() {
			res, ok, skipReason, err := e.tryLike(ctx, telegramID, trackID, "stall", next, cur)
			if err != nil {
				return nil, next, err
			}
			if ok {
				res.GestureEnabled = gestureEnabled
				if !gestureEnabled {
					e.logDecision(telegramID, "like_gesture_disabled", "stall detected but toggle is off", map[string]any{
						"track_id": trackID, "stall_ms": stall.Milliseconds(),
					})
					return res, next, nil
				}
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
		next.PauseConfirmed = false
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
	return &Result{
		Action:          ActionLikeTrack,
		TrackID:         trackID,
		TrackName:       cur.TrackName,
		Artist:          cur.Artist,
		AlbumImageURL:   cur.AlbumImageURL,
		ProgressMs:      cur.ProgressMs,
		TrackDurationMs: cur.DurationMs,
		Source:          source,
	}, true, "", nil
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

func (e *Engine) inTrackStartGrace(progressMs int) bool {
	return progressMs < int(e.cfg.TrackStartGrace.Milliseconds())
}

// quickPauseSlack absorbs poll jitter and Milliseconds() truncation (4.001s logs as pause_ms=4000).
func (e *Engine) quickPauseSlack() time.Duration {
	return 500 * time.Millisecond
}

func (e *Engine) quickPauseMaxEffective() time.Duration {
	return e.cfg.QuickPauseMax + e.quickPauseSlack()
}

// edgeResumeQualifies avoids a like when Spotify briefly reports playing=true once after
// pause_started but before still_paused. Progress advancing during pause means a real resume
// between polls even when wall_ms hits the quick-pause ceiling.
func (e *Engine) edgeResumeQualifies(prev spotify.PollState, pauseDur time.Duration, progressDuringPause int) bool {
	if prev.PauseConfirmed {
		return true
	}
	slack := 400 * time.Millisecond
	if pauseDur <= e.cfg.EdgeFastResumeMax+slack {
		return true
	}
	if progressDuringPause >= 300 && pauseDur <= e.quickPauseMaxEffective() {
		return true
	}
	return false
}

func (e *Engine) stallMinWall() time.Duration {
	// Stall needs a gap at least min_wall; cap at playing poll interval so 2s polls can qualify.
	if e.cfg.StallMinWall > e.cfg.PollPlaying {
		return e.cfg.PollPlaying
	}
	return e.cfg.StallMinWall
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
