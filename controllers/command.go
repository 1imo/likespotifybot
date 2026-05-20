/*
CommandController maps Telegram commands for LikeSpotifyBot.
*/

package controllers

import (
	"context"
	"strings"

	handlecommand "likespotifybot/use-cases/handle-command"
	handlemessage "likespotifybot/use-cases/handle-message"
	"likespotifybot/utils"
)

type PolicyHandler interface {
	Handle(ctx context.Context, actorUserID, chatID int64, policyName string) (string, error)
}

type CommandController struct {
	spotify       *handlecommand.SpotifyCommands
	policyHandler PolicyHandler
	analytics     *utils.Analytics
}

func NewCommandController(spotify *handlecommand.SpotifyCommands, policyHandler PolicyHandler, analytics *utils.Analytics) *CommandController {
	return &CommandController{
		spotify:       spotify,
		policyHandler: policyHandler,
		analytics:     analytics,
	}
}

func (c *CommandController) Handle(ctx context.Context, actorUserID, chatID int64, command string, argument string) (handlemessage.OutboundMessage, error) {
	cmd := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(command, "/")))
	_ = strings.TrimSpace(argument)

	if c.analytics != nil {
		_ = c.analytics.TrackEvent(ctx, utils.AnalyticsEvent{
			Name:   "command-requested",
			UserID: actorUserID,
			Status: "ok",
			Meta: utils.MetaWithChatID(chatID, map[string]any{
				"command": cmd,
			}),
		})
	}

	switch cmd {
	case "start":
		if chatID < 0 {
			return handlemessage.OutboundMessage{}, nil
		}
		return c.spotify.HandleStart(ctx, actorUserID, chatID)
	case "toggle", "toggle-on-off":
		return c.spotify.HandleToggle(ctx, actorUserID, chatID)
	case "help":
		s, err := c.policyHandler.Handle(ctx, actorUserID, chatID, "help")
		return handlemessage.OutboundMessage{Text: s}, err
	case "about":
		s, err := c.policyHandler.Handle(ctx, actorUserID, chatID, "about")
		return handlemessage.OutboundMessage{Text: s}, err
	case "legal":
		s, err := c.policyHandler.Handle(ctx, actorUserID, chatID, "legal")
		return handlemessage.OutboundMessage{Text: s}, err
	default:
		s, err := c.policyHandler.Handle(ctx, actorUserID, chatID, "help")
		return handlemessage.OutboundMessage{Text: s}, err
	}
}

// HandleCallback handles inline keyboard callbacks (lsb:*).
func (c *CommandController) HandleCallback(ctx context.Context, data string, telegramID, chatID int64) (handlecommand.CallbackAnswer, error) {
	return c.spotify.HandleCallback(ctx, data, telegramID, chatID)
}
