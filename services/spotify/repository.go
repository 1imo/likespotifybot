package spotify

import (
	"context"
	"database/sql"
	"time"

	"likespotifybot/utils/db"
)

// account is a persisted Spotify link for a Telegram user.
type account struct {
	TelegramID     int64
	SpotifyUserID  string
	AccessToken    string
	RefreshToken   string
	TokenExpiresAt time.Time
}

// PollState is the last known playback snapshot for gesture detection.
type PollState struct {
	TrackID         string
	IsPlaying       bool
	ProgressMs      int
	PausedAt        *time.Time
	PauseConfirmed  bool // true after a poll saw still_paused (not just pause_started)
	LastPolledAt    time.Time
	InactiveSince   *time.Time
}

// Repository persists Spotify auth and playback state.
type Repository struct {
	store db.DB
}

func NewRepository(store db.DB) *Repository {
	return &Repository{store: store}
}

func (r *Repository) CreateOAuthState(ctx context.Context, state string, telegramID int64, ttl time.Duration) error {
	exp := time.Now().UTC().Add(ttl)
	_, err := r.store.ExecContext(ctx,
		`INSERT INTO spotify_oauth_states (state, telegram_id, expires_at) VALUES ($1, $2, $3)`,
		state, telegramID, exp,
	)
	return err
}

func (r *Repository) ConsumeOAuthState(ctx context.Context, state string) (telegramID int64, ok bool, err error) {
	row := r.store.QueryRowContext(ctx,
		`DELETE FROM spotify_oauth_states
		 WHERE state = $1 AND expires_at > NOW()
		 RETURNING telegram_id`,
		0, state,
	)
	if err := row.Scan(&telegramID); err != nil {
		if err == sql.ErrNoRows {
			return 0, false, nil
		}
		return 0, false, err
	}
	return telegramID, true, nil
}

func (r *Repository) UpsertAccount(ctx context.Context, acc account) error {
	_, err := r.store.ExecContext(ctx,
		`INSERT INTO spotify_accounts (telegram_id, spotify_user_id, access_token, refresh_token, token_expires_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, NOW())
		 ON CONFLICT (telegram_id) DO UPDATE SET
		   spotify_user_id = COALESCE(NULLIF(EXCLUDED.spotify_user_id, ''), spotify_accounts.spotify_user_id),
		   access_token = EXCLUDED.access_token,
		   refresh_token = COALESCE(NULLIF(EXCLUDED.refresh_token, ''), spotify_accounts.refresh_token),
		   token_expires_at = EXCLUDED.token_expires_at,
		   updated_at = NOW()`,
		acc.TelegramID, acc.SpotifyUserID, acc.AccessToken, acc.RefreshToken, acc.TokenExpiresAt,
	)
	return err
}

func (r *Repository) GetAccount(ctx context.Context, telegramID int64) (*account, error) {
	row := r.store.QueryRowContext(ctx,
		`SELECT telegram_id, COALESCE(spotify_user_id, ''), access_token, refresh_token, token_expires_at
		 FROM spotify_accounts WHERE telegram_id = $1`,
		0, telegramID,
	)
	var acc account
	if err := row.Scan(&acc.TelegramID, &acc.SpotifyUserID, &acc.AccessToken, &acc.RefreshToken, &acc.TokenExpiresAt); err != nil {
		return nil, err
	}
	return &acc, nil
}

func (r *Repository) DeleteAccount(ctx context.Context, telegramID int64) error {
	_, err := r.store.ExecContext(ctx, `DELETE FROM spotify_accounts WHERE telegram_id = $1`, telegramID)
	return err
}

func (r *Repository) IsConnected(ctx context.Context, telegramID int64) (bool, error) {
	row := r.store.QueryRowContext(ctx,
		`SELECT 1 FROM spotify_accounts WHERE telegram_id = $1`,
		0, telegramID,
	)
	var one int
	if err := row.Scan(&one); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (r *Repository) ListConnectedTelegramIDs(ctx context.Context) ([]int64, error) {
	rows, err := r.store.QueryContext(ctx,
		`SELECT telegram_id FROM spotify_accounts ORDER BY telegram_id`,
		0,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *Repository) EnsureGestureSettings(ctx context.Context, telegramID int64) error {
	_, err := r.store.ExecContext(ctx,
		`INSERT INTO gesture_settings (telegram_id) VALUES ($1)
		 ON CONFLICT (telegram_id) DO NOTHING`,
		telegramID,
	)
	return err
}

func (r *Repository) NotifyOnGesture(ctx context.Context, telegramID int64) (bool, error) {
	_ = r.EnsureGestureSettings(ctx, telegramID)
	row := r.store.QueryRowContext(ctx,
		`SELECT notify_on_gesture FROM gesture_settings WHERE telegram_id = $1`,
		0, telegramID,
	)
	var notify bool
	if err := row.Scan(&notify); err != nil {
		if err == sql.ErrNoRows {
			return true, nil
		}
		return true, err
	}
	return notify, nil
}

func (r *Repository) QuickPauseLikeEnabled(ctx context.Context, telegramID int64) (bool, error) {
	_ = r.EnsureGestureSettings(ctx, telegramID)
	row := r.store.QueryRowContext(ctx,
		`SELECT quick_pause_like_enabled FROM gesture_settings WHERE telegram_id = $1`,
		0, telegramID,
	)
	var enabled bool
	if err := row.Scan(&enabled); err != nil {
		if err == sql.ErrNoRows {
			return true, nil
		}
		return true, err
	}
	return enabled, nil
}

func (r *Repository) ToggleQuickPauseLike(ctx context.Context, telegramID int64) (bool, error) {
	_ = r.EnsureGestureSettings(ctx, telegramID)
	row := r.store.QueryRowContext(ctx,
		`UPDATE gesture_settings
		 SET quick_pause_like_enabled = NOT quick_pause_like_enabled,
		     updated_at = NOW()
		 WHERE telegram_id = $1
		 RETURNING quick_pause_like_enabled`,
		0, telegramID,
	)
	var enabled bool
	if err := row.Scan(&enabled); err != nil {
		return false, err
	}
	return enabled, nil
}

func (r *Repository) LoadPollState(ctx context.Context, telegramID int64) (PollState, error) {
	row := r.store.QueryRowContext(ctx,
		`SELECT COALESCE(track_id, ''), is_playing, progress_ms, paused_at,
		        COALESCE(pause_confirmed, false), last_polled_at, inactive_since
		 FROM playback_poll_state WHERE telegram_id = $1`,
		0, telegramID,
	)
	var s PollState
	var pausedAt, inactiveSince sql.NullTime
	if err := row.Scan(&s.TrackID, &s.IsPlaying, &s.ProgressMs, &pausedAt, &s.PauseConfirmed, &s.LastPolledAt, &inactiveSince); err != nil {
		if err == sql.ErrNoRows {
			return PollState{}, nil
		}
		return PollState{}, err
	}
	if pausedAt.Valid {
		t := pausedAt.Time
		s.PausedAt = &t
	}
	if inactiveSince.Valid {
		t := inactiveSince.Time
		s.InactiveSince = &t
	}
	return s, nil
}

func (r *Repository) SavePollState(ctx context.Context, telegramID int64, s PollState) error {
	var paused, inactive any
	if s.PausedAt != nil {
		paused = *s.PausedAt
	}
	if s.InactiveSince != nil {
		inactive = *s.InactiveSince
	}
	lastPolled := s.LastPolledAt
	if lastPolled.IsZero() {
		lastPolled = time.Now().UTC()
	}
	_, err := r.store.ExecContext(ctx,
		`INSERT INTO playback_poll_state (telegram_id, track_id, is_playing, progress_ms, paused_at, pause_confirmed, inactive_since, last_polled_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
		 ON CONFLICT (telegram_id) DO UPDATE SET
		   track_id = EXCLUDED.track_id,
		   is_playing = EXCLUDED.is_playing,
		   progress_ms = EXCLUDED.progress_ms,
		   paused_at = EXCLUDED.paused_at,
		   pause_confirmed = EXCLUDED.pause_confirmed,
		   inactive_since = EXCLUDED.inactive_since,
		   last_polled_at = EXCLUDED.last_polled_at,
		   updated_at = NOW()`,
		telegramID, nullIfEmpty(s.TrackID), s.IsPlaying, s.ProgressMs, paused, s.PauseConfirmed, inactive, lastPolled,
	)
	return err
}

func (r *Repository) RecordLikeDebounce(ctx context.Context, telegramID int64, trackID string) error {
	_, err := r.store.ExecContext(ctx,
		`INSERT INTO gesture_like_debounce (telegram_id, track_id, liked_at) VALUES ($1, $2, NOW())
		 ON CONFLICT (telegram_id, track_id) DO UPDATE SET liked_at = NOW()`,
		telegramID, trackID,
	)
	return err
}

func (r *Repository) RecentlyLiked(ctx context.Context, telegramID int64, trackID string, since time.Time) (bool, error) {
	row := r.store.QueryRowContext(ctx,
		`SELECT 1 FROM gesture_like_debounce
		 WHERE telegram_id = $1 AND track_id = $2 AND liked_at > $3`,
		0, telegramID, trackID, since,
	)
	var one int
	if err := row.Scan(&one); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
