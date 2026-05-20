package handlecommand

import (
	"context"

	"likespotifybot/services/spotify"
	"likespotifybot/utils"
)

// SpotifyCommands groups Spotify-related command and callback handlers.
type SpotifyCommands struct {
	repo      *spotify.Repository
	oauth     *spotify.OAuthService
	analytics *utils.Analytics
}

func NewSpotifyCommands(repo *spotify.Repository, oauth *spotify.OAuthService, analytics *utils.Analytics) *SpotifyCommands {
	return &SpotifyCommands{repo: repo, oauth: oauth, analytics: analytics}
}

func (s *SpotifyCommands) track(ctx context.Context, name string, userID, chatID int64, status string, meta map[string]any) {
	if s.analytics == nil {
		return
	}
	_ = s.analytics.TrackEvent(ctx, utils.AnalyticsEvent{
		Name:   name,
		UserID: userID,
		Status: status,
		Meta:   utils.MetaWithChatID(chatID, meta),
	})
}
