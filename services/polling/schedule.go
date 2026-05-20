package polling

import (
	"time"

	"likespotifybot/services/spotify"
	"likespotifybot/utils"
)

// NextPollInterval picks how long until the next Spotify poll for a user.
func NextPollInterval(cfg utils.SpotifyConfig, prev spotify.PollState, snap *spotify.PlaybackSnapshot, now time.Time) time.Duration {
	d, _ := pollSchedule(cfg, prev, snap, now)
	return d
}

// pollSchedule returns the next interval and a short reason label for logs.
func pollSchedule(cfg utils.SpotifyConfig, prev spotify.PollState, snap *spotify.PlaybackSnapshot, now time.Time) (time.Duration, string) {
	if snap == nil || !snap.DeviceActive {
		if prev.InactiveSince != nil && now.Sub(*prev.InactiveSince) >= cfg.PollInactiveAfter {
			return cfg.PollInactive, "inactive_180s_plus"
		}
		return cfg.PollPaused, "inactive_under_180s"
	}

	if !snap.IsPlaying {
		pauseStart := prev.PausedAt
		if pauseStart != nil {
			pauseFor := now.Sub(*pauseStart)
			if pauseFor >= cfg.PollPausedSlowAfter {
				return cfg.PollPaused, "paused_15s_plus"
			}
			// Fresh pause: poll sooner so still_paused / quick resume are not missed between 2–4s gaps.
			if !prev.PauseConfirmed && pauseFor < 5*time.Second {
				half := cfg.PollPlaying / 2
				if half < 800*time.Millisecond {
					half = 800 * time.Millisecond
				}
				return half, "paused_fresh"
			}
		}
		return cfg.PollPlaying, "paused_under_15s"
	}

	return cfg.PollPlaying, "playing"
}
