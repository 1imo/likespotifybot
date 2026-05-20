package spotify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"likespotifybot/utils"
)

const spotifyAPIBase = "https://api.spotify.com/v1"

// PlaybackSnapshot is normalized playback state for gesture detection.
type PlaybackSnapshot struct {
	TrackID        string
	TrackName      string
	Artist         string
	AlbumImageURL  string
	DurationMs     int
	IsPlaying      bool
	ProgressMs     int
	DeviceActive   bool
}

// Client calls Spotify Web API with token refresh and retries.
type Client struct {
	cfg   utils.SpotifyConfig
	repo  *Repository
	oauth *OAuthService
	hc    *http.Client
	log   *utils.Logger
}

func NewClient(cfg utils.SpotifyConfig, repo *Repository, oauth *OAuthService, log *utils.Logger) *Client {
	return &Client{
		cfg:   cfg,
		repo:  repo,
		oauth: oauth,
		hc:    &http.Client{Timeout: 12 * time.Second},
		log:   log,
	}
}

func (c *Client) ensureToken(ctx context.Context, telegramID int64) (*account, error) {
	acc, err := c.repo.GetAccount(ctx, telegramID)
	if err != nil {
		return nil, err
	}
	if time.Until(acc.TokenExpiresAt) > 60*time.Second {
		return acc, nil
	}
	if err := c.oauth.RefreshAccessToken(ctx, acc); err != nil {
		return nil, fmt.Errorf("refresh token: %w", err)
	}
	return c.repo.GetAccount(ctx, telegramID)
}

func (c *Client) GetPlayback(ctx context.Context, telegramID int64) (*PlaybackSnapshot, error) {
	acc, err := c.ensureToken(ctx, telegramID)
	if err != nil {
		return nil, err
	}
	body, status, err := c.doWithRetry(ctx, acc, http.MethodGet, spotifyAPIBase+"/me/player", nil)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNoContent || status == http.StatusNotFound {
		return &PlaybackSnapshot{DeviceActive: false}, nil
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("playback status=%d body=%s", status, string(body))
	}
	var raw struct {
		IsPlaying  bool `json:"is_playing"`
		ProgressMs int  `json:"progress_ms"`
		Item       *struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			DurationMs int    `json:"duration_ms"`
			Artists    []struct {
				Name string `json:"name"`
			} `json:"artists"`
			Album struct {
				Images []struct {
					URL    string `json:"url"`
					Height int    `json:"height"`
				} `json:"images"`
			} `json:"album"`
		} `json:"item"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	snap := &PlaybackSnapshot{IsPlaying: raw.IsPlaying, ProgressMs: raw.ProgressMs, DeviceActive: true}
	if raw.Item != nil {
		snap.TrackID = raw.Item.ID
		snap.TrackName = raw.Item.Name
		snap.DurationMs = raw.Item.DurationMs
		artistNames := make([]string, 0, len(raw.Item.Artists))
		for _, a := range raw.Item.Artists {
			if a.Name != "" {
				artistNames = append(artistNames, a.Name)
			}
		}
		snap.Artist = strings.Join(artistNames, ", ")
		snap.AlbumImageURL = pickAlbumImageURL(raw.Item.Album.Images)
	}
	return snap, nil
}

func pickAlbumImageURL(images []struct {
	URL    string `json:"url"`
	Height int    `json:"height"`
}) string {
	var bestURL string
	bestH := -1
	for _, img := range images {
		if img.URL == "" {
			continue
		}
		if img.Height > bestH {
			bestH = img.Height
			bestURL = img.URL
		}
	}
	if bestURL != "" {
		return bestURL
	}
	if len(images) > 0 {
		return images[0].URL
	}
	return ""
}

func trackURI(trackID string) string {
	if strings.HasPrefix(trackID, "spotify:") {
		return trackID
	}
	return "spotify:track:" + trackID
}

// TrackInLibrary reports whether the track is already in the user's saved library.
func (c *Client) TrackInLibrary(ctx context.Context, telegramID int64, trackID string) (bool, error) {
	acc, err := c.ensureToken(ctx, telegramID)
	if err != nil {
		return false, err
	}
	u := spotifyAPIBase + "/me/library/contains?uris=" + url.QueryEscape(trackURI(trackID))
	body, status, err := c.doWithRetry(ctx, acc, http.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	if status < 200 || status >= 300 {
		return false, fmt.Errorf("library contains status=%d body=%s", status, string(body))
	}
	var saved []bool
	if err := json.Unmarshal(body, &saved); err != nil {
		return false, err
	}
	return len(saved) > 0 && saved[0], nil
}

func (c *Client) SaveTrack(ctx context.Context, telegramID int64, trackID string) error {
	acc, err := c.ensureToken(ctx, telegramID)
	if err != nil {
		return err
	}
	// Dev-mode apps (Feb 2026+) require PUT /me/library with Spotify URIs;
	// legacy PUT /me/tracks returns 403 even with user-library-modify.
	u := spotifyAPIBase + "/me/library?uris=" + url.QueryEscape(trackURI(trackID))
	body, status, err := c.doWithRetry(ctx, acc, http.MethodPut, u, nil)
	if err != nil {
		return err
	}
	if status == http.StatusOK || status == http.StatusCreated {
		return nil
	}
	return fmt.Errorf("save to library status=%d body=%s", status, string(body))
}

func (c *Client) doWithRetry(ctx context.Context, acc *account, method, url string, payload io.Reader) ([]byte, int, error) {
	var lastErr error
	for attempt := 0; attempt < c.cfg.MaxAPIRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 400 * time.Millisecond)
		}
		body, status, err := c.doOnce(ctx, acc.AccessToken, method, url, payload)
		if err != nil {
			lastErr = err
			continue
		}
		if status == http.StatusUnauthorized {
			if err := c.oauth.RefreshAccessToken(ctx, acc); err != nil {
				return nil, status, err
			}
			refreshed, err := c.repo.GetAccount(ctx, acc.TelegramID)
			if err != nil {
				return nil, status, err
			}
			acc = refreshed
			continue
		}
		if status == http.StatusTooManyRequests {
			if c.log != nil {
				c.log.Warn("event=spotify-api status=rate_limited attempt=%d", attempt+1)
			}
			lastErr = fmt.Errorf("rate limited")
			continue
		}
		return body, status, nil
	}
	if lastErr != nil {
		return nil, 0, lastErr
	}
	return nil, 0, fmt.Errorf("spotify request failed after retries")
}

func (c *Client) doOnce(ctx context.Context, token, method, url string, payload io.Reader) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, payload)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := c.hc.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return body, res.StatusCode, nil
}
