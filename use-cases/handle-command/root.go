/*
RootUseCase wraps policy HTML loading and records policy-requested analytics.
Injected into CommandController as PolicyHandler (same Handle signature).
Mirrors handle-message/root policy routing + analytics pattern.
*/

package handlecommand

import (
	"context"
	"strings"
	"likespotifybot/utils"
)

type RootUseCase struct {
	policyHandler *HandlePolicyUseCase
	analytics     *utils.Analytics
}

func NewRootUseCase(policyHandler *HandlePolicyUseCase, analytics *utils.Analytics) *RootUseCase {
	return &RootUseCase{policyHandler: policyHandler, analytics: analytics}
}

func (u *RootUseCase) Handle(ctx context.Context, actorUserID, chatID int64, policyName string) (string, error) {
	policyName = strings.TrimSpace(policyName)
	if policyName == "" {
		policyName = "help"
	}
	_ = u.analytics.TrackEvent(ctx, utils.AnalyticsEvent{
		Name:   "policy-requested",
		UserID: actorUserID,
		Status: "ok",
		Meta:   utils.MetaWithChatID(chatID, map[string]any{"policy": policyName}),
	})
	return u.policyHandler.Handle(ctx, policyName)
}
