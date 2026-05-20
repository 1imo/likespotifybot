package main

import (
	"context"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	bgservices "likespotifybot/bg-services"
	"likespotifybot/controllers"
	"likespotifybot/middleware"
	"likespotifybot/routes"
	"likespotifybot/services/gesture"
	"likespotifybot/services/polling"
	"likespotifybot/services/spotify"
	handlecommand "likespotifybot/use-cases/handle-command"
	handlebroadcast "likespotifybot/use-cases/handle-broadcast"
	handleevents "likespotifybot/use-cases/handle-events"
	handlemessage "likespotifybot/use-cases/handle-message"
	"likespotifybot/utils"
	"likespotifybot/utils/db"
)

/*
app.go is the composition root for LikeSpotifyBot.
It wires utils, middleware, controllers, use-cases, and background services.
*/

func main() {
	utils.Env.Init()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	logger := utils.NewAsyncLogger(ctx)

	database, err := db.DatabaseManager.Init(ctx)
	if err != nil {
		logger.Error("database init failed: %v", err)
		panic(err)
	}
	defer database.Close()

	analytics := utils.AnalyticsManager.Init(logger)
	deferredWrites := db.NewDeferredWriteQueue()
	go deferredWrites.Run(ctx, database, logger)

	handleEventsUC := handleevents.NewRootUseCase(database, analytics, logger)
	eventsService := bgservices.NewHandleEventsService(handleEventsUC, logger)
	go eventsService.Run(ctx)

	token := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))
	if token == "" {
		panic("TELEGRAM_BOT_TOKEN is required")
	}
	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		panic(err)
	}
	broadcastSender := &telegramBroadcaster{bot: bot}

	spotifyCfg := utils.LoadSpotifyConfig()
	if !spotifyCfg.Valid() {
		logger.Warn("Spotify OAuth is not fully configured (SPOTIFY_CLIENT_ID, SPOTIFY_CLIENT_SECRET, SPOTIFY_REDIRECT_URI)")
	}

	spotifyRepo := spotify.NewRepository(database)
	oauthSvc := spotify.NewOAuthService(spotifyCfg, spotifyRepo)
	spotifyClient := spotify.NewClient(spotifyCfg, spotifyRepo, oauthSvc, logger)
	gestureDebounce := gesture.NewDebounce(spotifyCfg.GestureCooldown)
	gestureEngine := gesture.NewEngine(spotifyCfg, spotifyRepo, gestureDebounce, logger)
	pollCoord := polling.NewCoordinator(spotifyCfg, spotifyRepo, spotifyClient, gestureEngine, gestureDebounce, bot, analytics, logger)
	pollingService := bgservices.NewHandlePollingService(pollCoord, spotifyCfg, logger)
	go pollingService.Run(ctx)

	httpService := bgservices.NewHandleHTTPService(spotifyCfg.HTTPListen, oauthSvc, bot, analytics, logger)
	go httpService.Run(ctx)

	spotifyCommands := handlecommand.NewSpotifyCommands(spotifyRepo, oauthSvc, analytics)
	handlePolicyUC := handlecommand.NewHandlePolicyUseCase()
	handleCommandPolicyUC := handlecommand.NewRootUseCase(handlePolicyUC, analytics)
	handleUnknownUC := handlemessage.NewHandleUnknownUseCase(analytics)
	handleMessageRootUC := handlemessage.NewRootUseCase(handleUnknownUC, analytics)
	handleBroadcastUC := handlebroadcast.NewRootUseCase(database, analytics, broadcastSender, broadcastSender, nil, logger)
	broadcastService := bgservices.NewHandleBroadcastService(handleBroadcastUC, logger)

	userMiddleware := middleware.NewHandleUserMiddleware(database, analytics, deferredWrites)
	messageController := controllers.NewMessageController(handleMessageRootUC)
	commandController := controllers.NewCommandController(spotifyCommands, handleCommandPolicyUC, analytics)

	messageRoute := routes.NewMessageRoute(userMiddleware, messageController, logger)
	commandRoute := routes.NewCommandRoute(userMiddleware, commandController, logger)
	updateRouter := routes.NewUpdateRouter(bot, logger, userMiddleware, messageRoute, commandRoute, commandController)

	go updateRouter.Run(ctx)

	if utils.Env.BroadcastSchedulerEnabled() {
		go broadcastService.RunEvery(ctx, pollBroadcastInterval())
		logger.Info("background services started (broadcast)")
	} else {
		logger.Info("background services skipped (set APP_ENV=live or APP_ENV=test to enable broadcast scheduler)")
	}

	logger.Info("LikeSpotifyBot composition root initialized")
	<-ctx.Done()
	logger.Info("LikeSpotifyBot shutting down")
}

type telegramBroadcaster struct {
	bot *tgbotapi.BotAPI
}

func (t *telegramBroadcaster) SendOutbound(chatID int64, msg handlemessage.OutboundMessage) (int64, error) {
	cfg := tgbotapi.NewMessage(chatID, msg.Text)
	if utils.LooksLikeTelegramHTML(msg.Text) {
		cfg.ParseMode = "HTML"
	}
	if msg.ReplyMarkup != nil {
		cfg.ReplyMarkup = msg.ReplyMarkup
	}
	sent, err := t.bot.Send(cfg)
	if err != nil {
		return 0, err
	}
	return int64(sent.MessageID), nil
}

func (t *telegramBroadcaster) SendQuiz(userID int64, question string, options []string, correctIndex int) (int64, error) {
	quiz := tgbotapi.NewPoll(userID, question, options...)
	quiz.Type = "quiz"
	quiz.CorrectOptionID = int64(correctIndex)
	quiz.IsAnonymous = false
	msg, err := t.bot.Send(quiz)
	if err != nil {
		return 0, err
	}
	return int64(msg.MessageID), nil
}

func pollBroadcastInterval() time.Duration {
	raw := strings.TrimSpace(os.Getenv("POLL_BROADCAST_INTERVAL_SECONDS"))
	secs, err := strconv.Atoi(raw)
	if err != nil || secs <= 0 {
		return time.Minute
	}
	return time.Duration(secs) * time.Second
}
