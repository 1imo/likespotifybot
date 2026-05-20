package handlebroadcast

import handlemessage "likespotifybot/use-cases/handle-message"

type MessageBroadcaster interface {
	SendOutbound(userID int64, msg handlemessage.OutboundMessage) (telegramMessageID int64, err error)
}
