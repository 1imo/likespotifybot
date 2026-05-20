/*
Package routes receives Telegram updates (long polling via UpdateRouter.Run) and dispatches
to command/message routes with injected middleware/controller chains.
*/
package routes

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"likespotifybot/controllers"
	"likespotifybot/middleware"
	handlemessage "likespotifybot/use-cases/handle-message"
	"likespotifybot/utils"
)

type UpdateRouter struct {
	bot                *tgbotapi.BotAPI
	log                *utils.Logger
	userMiddleware     *middleware.HandleUserMiddleware
	messageRoute       *MessageRoute
	commandRoute       *CommandRoute
	commandController  *controllers.CommandController
}

func NewUpdateRouter(
	bot *tgbotapi.BotAPI,
	log *utils.Logger,
	userMiddleware *middleware.HandleUserMiddleware,
	messageRoute *MessageRoute,
	commandRoute *CommandRoute,
	commandController *controllers.CommandController,
) *UpdateRouter {
	return &UpdateRouter{
		bot:               bot,
		log:               log,
		userMiddleware:    userMiddleware,
		messageRoute:      messageRoute,
		commandRoute:      commandRoute,
		commandController: commandController,
	}
}

func (r *UpdateRouter) Run(ctx context.Context) {
	if _, err := r.bot.Request(tgbotapi.DeleteWebhookConfig{}); err != nil && r.log != nil {
		r.log.Warn("could not clear Telegram webhook URL: %v", err)
	}

	u := tgbotapi.NewUpdate(0)
	u.Timeout = getUpdatesTimeoutSeconds()
	u.AllowedUpdates = []string{"message", "callback_query"}
	updates := r.bot.GetUpdatesChan(u)
	defer r.bot.StopReceivingUpdates()

	for {
		select {
		case <-ctx.Done():
			return
		case update, ok := <-updates:
			if !ok {
				return
			}
			started := time.Now()
			err := r.HandleUpdate(ctx, update)
			if r.log != nil {
				durationMs := time.Since(started).Milliseconds()
				kind := updateKind(update)
				if err != nil {
					r.log.Error("event=update-handle kind=%s status=error duration_ms=%d err=%v", kind, durationMs, err)
				} else {
					r.log.Info("event=update-handle kind=%s status=ok duration_ms=%d", kind, durationMs)
				}
			}
		}
	}
}

func (r *UpdateRouter) HandleUpdate(ctx context.Context, update tgbotapi.Update) error {
	if r.log != nil {
		if raw, err := json.Marshal(update); err == nil {
			r.log.Info("event=raw-update json=%s", string(raw))
		}
	}

	if update.CallbackQuery != nil {
		return r.handleCallbackQuery(ctx, update.CallbackQuery)
	}

	if update.Message == nil {
		return nil
	}

	user := middleware.TelegramUser{
		TelegramID: update.Message.From.ID,
		Username:   update.Message.From.UserName,
		FirstName:  update.Message.From.FirstName,
		LastName:   update.Message.From.LastName,
	}

	if update.Message.IsCommand() {
		reply, err := r.commandRoute.Handle(ctx, user, update.Message.Chat.ID, "/"+update.Message.Command(), update.Message.CommandArguments())
		if err != nil {
			return err
		}
		if strings.TrimSpace(reply.Text) == "" && reply.ReplyMarkup == nil {
			return nil
		}
		return sendChatOutbound(r.bot, update.Message.Chat.ID, reply)
	}

	body := strings.TrimSpace(update.Message.Text)
	if body == "" {
		body = strings.TrimSpace(update.Message.Caption)
	}
	if body == "" {
		return nil
	}

	reply, err := r.messageRoute.Handle(ctx, user, update.Message.Chat.ID, body)
	if err != nil {
		return err
	}
	if strings.TrimSpace(reply.Text) == "" && reply.ReplyMarkup == nil {
		return nil
	}
	return sendChatOutbound(r.bot, update.Message.Chat.ID, reply)
}

func (r *UpdateRouter) handleCallbackQuery(ctx context.Context, cq *tgbotapi.CallbackQuery) error {
	if cq == nil || cq.From == nil || cq.Message == nil || r.commandController == nil {
		return nil
	}
	data := strings.TrimSpace(cq.Data)
	if !strings.HasPrefix(data, "lsb:") {
		return nil
	}

	user := middleware.TelegramUser{
		TelegramID: cq.From.ID,
		Username:   cq.From.UserName,
		FirstName:  cq.From.FirstName,
		LastName:   cq.From.LastName,
	}
	if r.userMiddleware != nil {
		if err := r.userMiddleware.EnsureUser(ctx, user, cq.Message.Chat.ID); err != nil {
			if r.log != nil {
				r.log.Warn("callback: ensure user failed: %v", err)
			}
			return err
		}
	}

	ans, err := r.commandController.HandleCallback(ctx, data, cq.From.ID, cq.Message.Chat.ID)
	if err != nil {
		return err
	}

	cb := tgbotapi.CallbackConfig{
		CallbackQueryID: cq.ID,
		Text:            ans.Text,
		ShowAlert:       ans.ShowAlert,
	}
	if _, err := r.bot.Request(cb); err != nil {
		return err
	}
	if ans.Message != nil {
		return sendChatOutbound(r.bot, cq.Message.Chat.ID, *ans.Message)
	}
	return nil
}

func getUpdatesTimeoutSeconds() int {
	raw := strings.TrimSpace(os.Getenv("GET_UPDATES_TIMEOUT_SECONDS"))
	if raw == "" {
		return 50
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 50
	}
	if n > 50 {
		return 50
	}
	return n
}

func sendChatOutbound(bot *tgbotapi.BotAPI, chatID int64, msg handlemessage.OutboundMessage) error {
	cfg := tgbotapi.NewMessage(chatID, msg.Text)
	if utils.LooksLikeTelegramHTML(msg.Text) {
		cfg.ParseMode = "HTML"
	}
	if msg.ReplyMarkup != nil {
		cfg.ReplyMarkup = msg.ReplyMarkup
	}
	_, err := bot.Send(cfg)
	return err
}

func updateKind(update tgbotapi.Update) string {
	if update.Message != nil {
		if update.Message.IsCommand() {
			return "command"
		}
		return "message"
	}
	if update.CallbackQuery != nil {
		return "callback_query"
	}
	return "other"
}
