CREATE TABLE IF NOT EXISTS users (
  id BIGSERIAL PRIMARY KEY,
  telegram_id BIGINT NOT NULL UNIQUE,
  username TEXT,
  first_name TEXT,
  last_name TEXT,
  total_requests INTEGER NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS responses (
  id BIGSERIAL PRIMARY KEY,
  key TEXT NOT NULL UNIQUE,
  message TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS whitelist (
  id BIGSERIAL PRIMARY KEY,
  domain TEXT NOT NULL UNIQUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS subscriptions (
  id BIGSERIAL PRIMARY KEY,
  telegram_id BIGINT NOT NULL UNIQUE,
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS group_chats (
  telegram_id BIGINT PRIMARY KEY,
  first_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS broadcasts (
  id TEXT PRIMARY KEY,
  type TEXT NOT NULL CHECK (type IN ('message', 'quiz')),
  payload JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  created_db_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  frequency INTEGER CHECK (frequency IS NULL OR frequency >= 0),
  sent_at TIMESTAMPTZ,
  audience TEXT,
  env TEXT NOT NULL DEFAULT 'live'
);

CREATE TABLE IF NOT EXISTS broadcast_outgoing (
  id BIGSERIAL PRIMARY KEY,
  broadcast_id TEXT NOT NULL REFERENCES broadcasts(id) ON DELETE CASCADE,
  user_id BIGINT NOT NULL,
  scheduled_at TIMESTAMPTZ,
  sent_time TIMESTAMPTZ,
  telegram_message_id BIGINT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (broadcast_id, user_id)
);

CREATE TABLE IF NOT EXISTS app_analytics (
  id BIGSERIAL PRIMARY KEY,
  event_name TEXT NOT NULL,
  user_id BIGINT,
  entity_id TEXT,
  event_status TEXT,
  event_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  meta JSONB NOT NULL DEFAULT '{}'::jsonb
);

-- Spotify OAuth state → Telegram user (short-lived).
CREATE TABLE IF NOT EXISTS spotify_oauth_states (
  state TEXT PRIMARY KEY,
  telegram_id BIGINT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_spotify_oauth_states_expires ON spotify_oauth_states(expires_at);

-- Linked Spotify accounts (one per Telegram user).
CREATE TABLE IF NOT EXISTS spotify_accounts (
  telegram_id BIGINT PRIMARY KEY,
  spotify_user_id TEXT,
  access_token TEXT NOT NULL,
  refresh_token TEXT NOT NULL,
  token_expires_at TIMESTAMPTZ NOT NULL,
  connected_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Per-user gesture and notification preferences.
CREATE TABLE IF NOT EXISTS gesture_settings (
  telegram_id BIGINT PRIMARY KEY,
  quick_pause_like_enabled BOOLEAN NOT NULL DEFAULT TRUE,
  notify_on_gesture BOOLEAN NOT NULL DEFAULT TRUE,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Last known playback snapshot for polling / gesture detection.
CREATE TABLE IF NOT EXISTS playback_poll_state (
  telegram_id BIGINT PRIMARY KEY,
  track_id TEXT,
  is_playing BOOLEAN NOT NULL DEFAULT FALSE,
  progress_ms INTEGER NOT NULL DEFAULT 0,
  paused_at TIMESTAMPTZ,
  inactive_since TIMESTAMPTZ,
  last_polled_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE playback_poll_state ADD COLUMN IF NOT EXISTS inactive_since TIMESTAMPTZ;

-- Debounce duplicate likes on the same track (optional persistence).
CREATE TABLE IF NOT EXISTS gesture_like_debounce (
  telegram_id BIGINT NOT NULL,
  track_id TEXT NOT NULL,
  liked_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (telegram_id, track_id)
);

CREATE INDEX IF NOT EXISTS idx_users_last_seen_at ON users(last_seen_at);
CREATE INDEX IF NOT EXISTS idx_subscriptions_enabled ON subscriptions(enabled, telegram_id);
CREATE INDEX IF NOT EXISTS idx_app_analytics_event_at ON app_analytics(event_at);
CREATE INDEX IF NOT EXISTS idx_spotify_accounts_updated ON spotify_accounts(updated_at);
