# LikeSpotifyBot

Telegram bot that connects to Spotify via OAuth and saves the current track when you perform a **quick pause → resume** while listening. I wanted a seamless way to add songs to my liked whilst working and not having to navigate to the Spotify UI / think much about it. Initially I researched headphones with programmable buttons - like a Steamdeck. There were none on the market. Thought about bolting an ESP32 or a bluetooth button to my headphones, and then wondered if I could hijack the existing buttons instead. This was the most seamless solution to serve many users.

Built on a Go Telegram bot boilerplate (routes → controllers → use-cases, Postgres, background workers).

## Features

- `/start` with inline keyboards (Connect / Connected / Disconnect)
- `/toggle` to enable or disable gesture detection
- Spotify Authorization Code OAuth with secure state tokens
- Playback polling with configurable intervals
- Gesture engine: quick pause then resume → save track
- Optional Telegram notification on successful save
- Broadcast scheduler (when `APP_ENV=live` or `test`)

## Environment

See `.env.example` for the full list. Key variables:

- `TELEGRAM_BOT_TOKEN`, `DATABASE_URL`
- `SPOTIFY_CLIENT_ID`, `SPOTIFY_CLIENT_SECRET`, `SPOTIFY_REDIRECT_URI`
- `HTTP_LISTEN_ADDR` (default `:8080`, OAuth callback at `/spotify/callback`)
- `POLL_INTERVAL_PLAYING_MS`, `POLL_INTERVAL_IDLE_MS`, `GESTURE_QUICK_PAUSE_MAX_MS`
- `NOTIFY_ON_GESTURE`, `GESTURE_COOLDOWN_SECONDS`

## Run locally

```bash
go run ./zz-ops/create-db.go
go run .
```

## Architecture

- `routes/` — Telegram update routing (commands, messages, inline callbacks)
- `controllers/` — Command dispatch
- `use-cases/handle-command/` — Start menu, toggle, callbacks, policies
- `bg-services/` — Analytics, polling, HTTP (OAuth), broadcast
- `services/spotify/` — OAuth, API client, repository
- `services/gesture/` — Gesture detection
- `services/polling/` — Playback polling coordinator
