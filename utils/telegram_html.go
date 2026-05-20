package utils

import "strings"

// LooksLikeTelegramHTML mirrors bot HTML parse-mode heuristics (see Telegram Bot API — HTML mode).
func LooksLikeTelegramHTML(text string) bool {
	if !strings.Contains(text, "<") || !strings.Contains(text, ">") {
		return false
	}
	return strings.Contains(text, "<b>") || strings.Contains(text, "<strong>") ||
		strings.Contains(text, "<i>") || strings.Contains(text, "<em>") ||
		strings.Contains(text, "<u>") || strings.Contains(text, "<s>") || strings.Contains(text, "<strike>") ||
		strings.Contains(text, "<code>") || strings.Contains(text, "<pre>") ||
		strings.Contains(text, "<a ") || strings.Contains(text, "<tg-spoiler>") ||
		strings.Contains(text, "<blockquote>") || strings.Contains(text, "<br")
}
