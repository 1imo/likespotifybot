/*
MessageRoute runs user middleware then MessageController (strain/url/unknown root).
*/

package routes

import (
	"context"

	"likespotifybot/controllers"
	"likespotifybot/middleware"
	handlemessage "likespotifybot/use-cases/handle-message"
	"likespotifybot/utils"
)

type MessageRoute struct {
	middleware *middleware.HandleUserMiddleware
	controller *controllers.MessageController
	log        *utils.Logger
}

func NewMessageRoute(middleware *middleware.HandleUserMiddleware, controller *controllers.MessageController, log *utils.Logger) *MessageRoute {
	return &MessageRoute{middleware: middleware, controller: controller, log: log}
}

func (r *MessageRoute) Handle(ctx context.Context, user middleware.TelegramUser, chatID int64, message string) (handlemessage.OutboundMessage, error) {
	if err := r.middleware.EnsureUser(ctx, user, chatID); err != nil {
		if r.log != nil {
			r.log.Warn("message route: ensure user failed: %v", err)
		}
		return handlemessage.OutboundMessage{}, err
	}
	return r.controller.Handle(ctx, user.TelegramID, chatID, message)
}
