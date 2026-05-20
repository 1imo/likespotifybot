package handlecommand

import (
	"context"
	"fmt"

	handlemessage "likespotifybot/use-cases/handle-message"
)

// HandleToggleOnOff flips quick pause → like gesture detection for the user.
func (s *SpotifyCommands) HandleToggleOnOff(ctx context.Context, actorUserID, chatID int64) (handlemessage.OutboundMessage, error) {
	connected, err := s.repo.IsConnected(ctx, actorUserID)
	if err != nil {
		s.track(ctx, "toggle-on-off", actorUserID, chatID, "error", map[string]any{"error": err.Error()})
		return handlemessage.OutboundMessage{}, err
	}
	if !connected {
		s.track(ctx, "toggle-on-off", actorUserID, chatID, "miss", map[string]any{"reason": "not_connected"})
		return handlemessage.OutboundMessage{
			Text: "Connect Spotify first with /start.",
		}, nil
	}
	enabled, err := s.repo.ToggleQuickPauseLike(ctx, actorUserID)
	if err != nil {
		s.track(ctx, "toggle-on-off", actorUserID, chatID, "error", map[string]any{"error": err.Error()})
		return handlemessage.OutboundMessage{}, err
	}
	state := "off"
	if enabled {
		state = "on"
	}
	s.track(ctx, "toggle-on-off", actorUserID, chatID, "ok", map[string]any{"enabled": enabled})
	return handlemessage.OutboundMessage{
		Text: fmt.Sprintf("Quick pause → like gesture is now <b>%s</b>.", state),
	}, nil
}
