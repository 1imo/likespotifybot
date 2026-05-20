package utils

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// SpotifyScopes required for playback polling and library modify.
const SpotifyScopes = "user-read-currently-playing user-read-playback-state user-library-modify"

// SpotifyConfig holds LikeSpotifyBot Spotify-related env configuration.
type SpotifyConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	HTTPListen   string

	PollPlaying         time.Duration
	PollPaused          time.Duration
	PollInactive        time.Duration
	PollPausedSlowAfter time.Duration
	PollInactiveAfter   time.Duration

	QuickPauseMax   time.Duration
	GestureCooldown time.Duration
	NotifyOnGesture bool
	StallSlackRatio float64

	MaxAPIRetries int
}

func LoadSpotifyConfig() SpotifyConfig {
	pollPaused := durationMsEnv("POLL_INTERVAL_PAUSED_MS", 0)
	if pollPaused <= 0 {
		pollPaused = durationMsEnv("POLL_INTERVAL_IDLE_MS", 5000)
	}
	return SpotifyConfig{
		ClientID:            strings.TrimSpace(os.Getenv("SPOTIFY_CLIENT_ID")),
		ClientSecret:        strings.TrimSpace(os.Getenv("SPOTIFY_CLIENT_SECRET")),
		RedirectURI:         strings.TrimSpace(os.Getenv("SPOTIFY_REDIRECT_URI")),
		HTTPListen:          listenAddr(),
		PollPlaying:         durationMsEnv("POLL_INTERVAL_PLAYING_MS", 2000),
		PollPaused:          pollPaused,
		PollInactive:        durationMsEnv("POLL_INTERVAL_INACTIVE_MS", 60000),
		PollPausedSlowAfter: durationMsEnv("POLL_PAUSED_SLOW_AFTER_MS", 15000),
		PollInactiveAfter:   durationSecEnv("POLL_INACTIVE_AFTER_SECONDS", 180),
		QuickPauseMax:       durationMsEnv("GESTURE_QUICK_PAUSE_MAX_MS", 4000),
		GestureCooldown:     durationSecEnv("GESTURE_COOLDOWN_SECONDS", 30),
		NotifyOnGesture:     boolEnvDefault("NOTIFY_ON_GESTURE", true),
		StallSlackRatio:     floatEnvDefault("GESTURE_STALL_SLACK_RATIO", 0.90),
		MaxAPIRetries:       intEnvDefault("SPOTIFY_API_MAX_RETRIES", 3),
	}
}

// PollTick returns the fastest poll interval (worker wake rate).
func (c SpotifyConfig) PollTick() time.Duration {
	return minDuration(c.PollPlaying, c.PollPaused, c.PollInactive)
}

func (c SpotifyConfig) Valid() bool {
	return c.ClientID != "" && c.ClientSecret != "" && c.RedirectURI != ""
}

func listenAddr() string {
	if v := strings.TrimSpace(os.Getenv("HTTP_LISTEN_ADDR")); v != "" {
		return v
	}
	return ":8080"
}

func durationMsEnv(key string, defaultMs int) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return time.Duration(defaultMs) * time.Millisecond
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return time.Duration(defaultMs) * time.Millisecond
	}
	return time.Duration(n) * time.Millisecond
}

func durationSecEnv(key string, defaultSec int) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return time.Duration(defaultSec) * time.Second
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return time.Duration(defaultSec) * time.Second
	}
	return time.Duration(n) * time.Second
}

func boolEnvDefault(key string, def bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return def
	}
}

func intEnvDefault(key string, def int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func floatEnvDefault(key string, def float64) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil || f <= 0 || f > 1 {
		return def
	}
	return f
}

func minDuration(vals ...time.Duration) time.Duration {
	if len(vals) == 0 {
		return time.Second
	}
	m := vals[0]
	for _, v := range vals[1:] {
		if v < m {
			m = v
		}
	}
	return m
}
