package handlecommand

import (
	"context"
	"fmt"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	handlemessage "likespotifybot/use-cases/handle-message"
)

// CallbackAnswer is the Telegram callback_query response.
type CallbackAnswer struct {
	Text      string
	ShowAlert bool
	Message   *handlemessage.OutboundMessage
}

func (s *SpotifyCommands) HandleCallback(ctx context.Context, data string, telegramID, chatID int64) (CallbackAnswer, error) {
	s.track(ctx, "callback-requested", telegramID, chatID, "ok", map[string]any{"data": data})

	switch data {
	case CallbackConnect:
		url, err := s.ConnectURL(ctx, telegramID, chatID)
		if err != nil {
			return CallbackAnswer{Text: "Could not start Spotify login. Try again later.", ShowAlert: true}, nil
		}
		msg := handlemessage.OutboundMessage{
			Text: "Open this link to connect Spotify (valid for 15 minutes):",
			ReplyMarkup: handlemessage.NewInlineKeyboard([][]tgbotapi.InlineKeyboardButton{
				{tgbotapi.NewInlineKeyboardButtonURL("Connect Spotify", url)},
			}),
		}
		return CallbackAnswer{Text: "Opening Spotify connect…", Message: &msg}, nil

	case CallbackDisconnect:
		if err := s.repo.DeleteAccount(ctx, telegramID); err != nil {
			s.track(ctx, "spotify-disconnect", telegramID, chatID, "error", map[string]any{"error": err.Error()})
			return CallbackAnswer{Text: "Disconnect failed.", ShowAlert: true}, err
		}
		s.track(ctx, "spotify-disconnect", telegramID, chatID, "ok", nil)
		out, err := s.HandleStart(ctx, telegramID, chatID)
		if err != nil {
			return CallbackAnswer{Text: "Disconnected.", Message: &handlemessage.OutboundMessage{Text: "Spotify disconnected."}}, nil
		}
		return CallbackAnswer{Text: "Disconnected.", Message: &out}, nil

	case CallbackGestures:
		s.track(ctx, "callback-gestures-placeholder", telegramID, chatID, "ok", nil)
		return CallbackAnswer{
			Text:      "Gesture settings (coming soon): quick pause → resume to like.",
			ShowAlert: true,
		}, nil

	case CallbackStatus:
		connected, err := s.repo.IsConnected(ctx, telegramID)
		if err != nil {
			s.track(ctx, "callback-status", telegramID, chatID, "error", map[string]any{"error": err.Error()})
			return CallbackAnswer{Text: "Could not load status.", ShowAlert: true}, err
		}
		if !connected {
			s.track(ctx, "callback-status", telegramID, chatID, "miss", map[string]any{"reason": "not_connected"})
			return CallbackAnswer{Text: "Not connected to Spotify.", ShowAlert: true}, nil
		}
		enabled, _ := s.repo.QuickPauseLikeEnabled(ctx, telegramID)
		st, err := s.repo.LoadPollState(ctx, telegramID)
		if err != nil {
			s.track(ctx, "callback-status", telegramID, chatID, "error", map[string]any{"error": err.Error()})
			return CallbackAnswer{Text: "Connected. Playback state unavailable.", ShowAlert: true}, nil
		}
		paused := "no"
		if st.PausedAt != nil {
			paused = "yes"
		}
		gesture := "off"
		if enabled {
			gesture = "on"
		}
		s.track(ctx, "callback-status", telegramID, chatID, "ok", map[string]any{
			"gesture_enabled": enabled,
			"is_playing":      st.IsPlaying,
			"has_track":       st.TrackID != "",
		})
		return CallbackAnswer{
			Text: fmt.Sprintf(
				"Connected\nGesture: %s\nTrack: %s\nPlaying: %v\nPaused window: %s",
				gesture, emptyDash(st.TrackID), st.IsPlaying, paused,
			),
			ShowAlert: true,
		}, nil

	default:
		return CallbackAnswer{}, nil
	}
}

func emptyDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
