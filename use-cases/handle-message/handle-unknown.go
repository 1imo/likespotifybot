package handlemessage

import (
	"context"

	"likespotifybot/utils"
)

type HandleUnknownUseCase struct {
	analytics *utils.Analytics
}

func NewHandleUnknownUseCase(analytics *utils.Analytics) *HandleUnknownUseCase {
	return &HandleUnknownUseCase{analytics: analytics}
}

func (u *HandleUnknownUseCase) Handle(ctx context.Context, actorUserID, chatID int64, input string) (OutboundMessage, error) {
	_ = input
	if u.analytics != nil {
		_ = u.analytics.TrackEvent(ctx, utils.AnalyticsEvent{
			Name:   "message-unknown",
			UserID: actorUserID,
			Status: "ok",
			Meta:   utils.MetaWithChatID(chatID, nil),
		})
	}
	return OutboundMessage{
		Text: "Use /start to connect Spotify or manage your account.",
	}, nil
}
