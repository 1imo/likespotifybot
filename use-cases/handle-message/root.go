package handlemessage

import (
	"context"

	"likespotifybot/utils"
)

type RootUseCase struct {
	unknown   *HandleUnknownUseCase
	analytics *utils.Analytics
}

func NewRootUseCase(unknown *HandleUnknownUseCase, analytics *utils.Analytics) *RootUseCase {
	return &RootUseCase{unknown: unknown, analytics: analytics}
}

func (u *RootUseCase) Handle(ctx context.Context, actorUserID, chatID int64, input string) (OutboundMessage, error) {
	if u.analytics != nil {
		_ = u.analytics.TrackEvent(ctx, utils.AnalyticsEvent{
			Name:   "message-received",
			UserID: actorUserID,
			Status: "ok",
			Meta:   utils.MetaWithChatID(chatID, map[string]any{"len": len(input)}),
		})
	}
	return u.unknown.Handle(ctx, actorUserID, chatID, input)
}
