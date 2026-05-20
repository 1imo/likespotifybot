/*
bg-services/handle-http runs the HTTP server (health, Spotify OAuth callback).
Single-file service matching the boilerplate pattern used in sibling projects.
*/

package bgservices

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"likespotifybot/services/spotify"
	"likespotifybot/utils"
)

// HandleHTTPService serves OAuth callback and health endpoints.
type HandleHTTPService struct {
	addr      string
	oauth     *spotify.OAuthService
	bot       *tgbotapi.BotAPI
	analytics *utils.Analytics
	log       *utils.Logger
	srv       *http.Server
}

func NewHandleHTTPService(addr string, oauth *spotify.OAuthService, bot *tgbotapi.BotAPI, analytics *utils.Analytics, log *utils.Logger) *HandleHTTPService {
	return &HandleHTTPService{addr: addr, oauth: oauth, bot: bot, analytics: analytics, log: log}
}

func (s *HandleHTTPService) Run(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	// Root matches SPOTIFY_REDIRECT_URI=http://127.0.0.1:8080 in the Spotify dashboard.
	mux.HandleFunc("/", s.handleSpotifyCallback)
	mux.HandleFunc("/spotify/callback", s.handleSpotifyCallback)

	s.srv = &http.Server{Addr: s.addr, Handler: mux}
	if s.log != nil {
		s.log.Info("event=http-server status=starting addr=%s", s.addr)
	}

	go func() {
		if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			if s.log != nil {
				s.log.Error("event=http-server status=error err=%v", err)
			}
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = s.srv.Shutdown(shutdownCtx)
	if s.log != nil {
		s.log.Info("event=http-server status=stopped")
	}
}

func (s *HandleHTTPService) handleSpotifyCallback(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/spotify/callback" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path == "/" && strings.TrimSpace(r.URL.Query().Get("code")) == "" && strings.TrimSpace(r.URL.Query().Get("error")) == "" {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("LikeSpotifyBot OK"))
		return
	}

	q := r.URL.Query()
	if errMsg := strings.TrimSpace(q.Get("error")); errMsg != "" {
		s.trackOAuth(r.Context(), 0, "error", map[string]any{"reason": errMsg})
		http.Error(w, "Spotify authorization denied", http.StatusBadRequest)
		return
	}
	code := strings.TrimSpace(q.Get("code"))
	state := strings.TrimSpace(q.Get("state"))
	if code == "" || state == "" {
		s.trackOAuth(r.Context(), 0, "invalid", map[string]any{"reason": "missing_code_or_state"})
		http.Error(w, "missing code or state", http.StatusBadRequest)
		return
	}

	reqCtx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	telegramID, err := s.oauth.ExchangeCode(reqCtx, code, state)
	if err != nil {
		if s.log != nil {
			s.log.Warn("event=oauth-callback status=error err=%v", err)
		}
		s.trackOAuth(reqCtx, 0, "error", map[string]any{"error": err.Error()})
		http.Error(w, "authorization failed", http.StatusBadRequest)
		return
	}

	s.trackOAuth(reqCtx, telegramID, "ok", nil)

	if s.bot != nil {
		msg := tgbotapi.NewMessage(telegramID, "✅ Spotify connected!")
		_, _ = s.bot.Send(msg)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, `<!DOCTYPE html><html><body><h1>Spotify connected</h1><p>You can close this window and return to Telegram.</p></body></html>`)
}

func (s *HandleHTTPService) trackOAuth(ctx context.Context, userID int64, status string, meta map[string]any) {
	if s.analytics == nil {
		return
	}
	_ = s.analytics.TrackEvent(ctx, utils.AnalyticsEvent{
		Name:   "spotify-oauth-callback",
		UserID: userID,
		Status: status,
		Meta:   meta,
	})
}
