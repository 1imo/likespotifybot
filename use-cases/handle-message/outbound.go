package handlemessage

import tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

// OutboundMessage is a Telegram reply payload.
type OutboundMessage struct {
	Text        string
	ReplyMarkup interface{}
}

// NewInlineKeyboard wraps markup for controllers/routes.
func NewInlineKeyboard(rows [][]tgbotapi.InlineKeyboardButton) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}
