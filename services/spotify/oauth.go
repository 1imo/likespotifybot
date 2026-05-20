package spotify

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"likespotifybot/utils"
)

const (
	spotifyAuthURL  = "https://accounts.spotify.com/authorize"
	spotifyTokenURL = "https://accounts.spotify.com/api/token"
)

// tokenPair from Spotify token endpoint.
type tokenPair struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
}

// OAuthService handles Spotify Authorization Code flow.
type OAuthService struct {
	cfg  utils.SpotifyConfig
	repo *Repository
	hc   *http.Client
}

func NewOAuthService(cfg utils.SpotifyConfig, repo *Repository) *OAuthService {
	return &OAuthService{
		cfg:  cfg,
		repo: repo,
		hc:   &http.Client{Timeout: 15 * time.Second},
	}
}

func (o *OAuthService) Configured() bool {
	return o.cfg.Valid()
}

func (o *OAuthService) NewStateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (o *OAuthService) BeginAuth(ctx context.Context, telegramID int64) (authURL string, err error) {
	state, err := o.NewStateToken()
	if err != nil {
		return "", err
	}
	if err := o.repo.CreateOAuthState(ctx, state, telegramID, 15*time.Minute); err != nil {
		return "", err
	}
	q := url.Values{}
	q.Set("client_id", o.cfg.ClientID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", o.cfg.RedirectURI)
	q.Set("scope", utils.SpotifyScopes)
	q.Set("state", state)
	return spotifyAuthURL + "?" + q.Encode(), nil
}

func (o *OAuthService) ExchangeCode(ctx context.Context, code, state string) (telegramID int64, err error) {
	tid, ok, err := o.repo.ConsumeOAuthState(ctx, state)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("invalid or expired oauth state")
	}
	pair, err := o.requestToken(ctx, url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {o.cfg.RedirectURI},
	})
	if err != nil {
		return 0, err
	}
	exp := time.Now().UTC().Add(time.Duration(pair.ExpiresIn) * time.Second)
	acc := account{
		TelegramID:     tid,
		AccessToken:    pair.AccessToken,
		RefreshToken:   pair.RefreshToken,
		TokenExpiresAt: exp,
	}
	if err := o.repo.UpsertAccount(ctx, acc); err != nil {
		return 0, err
	}
	_ = o.repo.EnsureGestureSettings(ctx, tid)
	return tid, nil
}

func (o *OAuthService) RefreshAccessToken(ctx context.Context, acc *account) error {
	if acc.RefreshToken == "" {
		return fmt.Errorf("missing refresh token")
	}
	pair, err := o.requestToken(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {acc.RefreshToken},
	})
	if err != nil {
		return err
	}
	acc.AccessToken = pair.AccessToken
	if pair.RefreshToken != "" {
		acc.RefreshToken = pair.RefreshToken
	}
	acc.TokenExpiresAt = time.Now().UTC().Add(time.Duration(pair.ExpiresIn) * time.Second)
	return o.repo.UpsertAccount(ctx, *acc)
}

func (o *OAuthService) requestToken(ctx context.Context, form url.Values) (tokenPair, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, spotifyTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenPair{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(o.cfg.ClientID, o.cfg.ClientSecret)

	res, err := o.hc.Do(req)
	if err != nil {
		return tokenPair{}, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return tokenPair{}, fmt.Errorf("spotify token error status=%d body=%s", res.StatusCode, string(body))
	}
	var raw struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return tokenPair{}, err
	}
	return tokenPair{
		AccessToken:  raw.AccessToken,
		RefreshToken: raw.RefreshToken,
		ExpiresIn:    raw.ExpiresIn,
	}, nil
}
