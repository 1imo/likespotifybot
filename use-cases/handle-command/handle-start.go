package handlecommand

import (
	"context"
	"fmt"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	handlemessage "likespotifybot/use-cases/handle-message"
)

const (
	CallbackConnect    = "lsb:connect"
	CallbackDisconnect = "lsb:disconnect"
	CallbackGestures   = "lsb:gestures"
	CallbackStatus     = "lsb:status"
)

func (s *SpotifyCommands) HandleStart(ctx context.Context, actorUserID, chatID int64) (handlemessage.OutboundMessage, error) {
	connected, err := s.repo.IsConnected(ctx, actorUserID)
	if err != nil {
		s.track(ctx, "start-opened", actorUserID, chatID, "error", map[string]any{"error": err.Error()})
		return handlemessage.OutboundMessage{}, err
	}
	s.track(ctx, "start-opened", actorUserID, chatID, "ok", map[string]any{"connected": connected})
	if connected {
		return handlemessage.OutboundMessage{
			Text:        authenticatedWelcome(),
			ReplyMarkup: authenticatedKeyboard(),
		}, nil
	}
	return handlemessage.OutboundMessage{
		Text:        unauthenticatedWelcome(),
		ReplyMarkup: unauthenticatedKeyboard(),
	}, nil
}

func (s *SpotifyCommands) ConnectURL(ctx context.Context, telegramID, chatID int64) (string, error) {
	if !s.oauth.Configured() {
		s.track(ctx, "spotify-connect-started", telegramID, chatID, "error", map[string]any{"reason": "oauth_not_configured"})
		return "", fmt.Errorf("spotify oauth is not configured")
	}
	url, err := s.oauth.BeginAuth(ctx, telegramID)
	if err != nil {
		s.track(ctx, "spotify-connect-started", telegramID, chatID, "error", map[string]any{"error": err.Error()})
		return "", err
	}
	s.track(ctx, "spotify-connect-started", telegramID, chatID, "ok", nil)
	return url, nil
}

func unauthenticatedWelcome() string {
	return `<b>LikeSpotifyBot</b>

Connect your Spotify account, then use a quick <b>pause → resume</b> while a track is playing to save it to Your Library.

Tap <b>Connect Spotify</b> below to get started.`
}

func authenticatedWelcome() string {
	return `<b>LikeSpotifyBot</b>

✅ <b>Spotify connected</b>

While music is playing, pause and resume within about four seconds to ❤️ save the current track.

Use the buttons below to manage your connection.`
}

func unauthenticatedKeyboard() tgbotapi.InlineKeyboardMarkup {
	return handlemessage.NewInlineKeyboard([][]tgbotapi.InlineKeyboardButton{
		{tgbotapi.NewInlineKeyboardButtonData("Connect Spotify", CallbackConnect)},
	})
}

func authenticatedKeyboard() tgbotapi.InlineKeyboardMarkup {
	return handlemessage.NewInlineKeyboard([][]tgbotapi.InlineKeyboardButton{
		{tgbotapi.NewInlineKeyboardButtonData("✅ Connected", CallbackStatus)},
		{
			tgbotapi.NewInlineKeyboardButtonData("Gesture settings", CallbackGestures),
			tgbotapi.NewInlineKeyboardButtonData("Status / debug", CallbackStatus),
		},
		{tgbotapi.NewInlineKeyboardButtonData("Disconnect Spotify", CallbackDisconnect)},
	})
}
