// Understand if msg ir URL, a strain, or smth else
// First check if it's a URL with universal regex
// If so handle-message/handle-url use case
// If not, check if it's a strain name
// If yes, handle-message/handle-strain use case
// If not, call handle-message/handle-unknown use case

package controllers

import (
	"context"

	handlemessage "likespotifybot/use-cases/handle-message"
)

type MessageHandler interface {
	Handle(ctx context.Context, actorUserID, chatID int64, input string) (handlemessage.OutboundMessage, error)
}

type MessageController struct {
	handler MessageHandler
}

func NewMessageController(handler MessageHandler) *MessageController {
	return &MessageController{handler: handler}
}

func (c *MessageController) Handle(ctx context.Context, actorUserID, chatID int64, input string) (handlemessage.OutboundMessage, error) {
	return c.handler.Handle(ctx, actorUserID, chatID, input)
}
