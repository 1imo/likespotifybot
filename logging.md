# LikeSpotifyBot logging (`stdout`)

Runtime logs use **`utils.Logger`** (`utils/logger.go`) with async buffering via **`NewAsyncLogger`** in `app.go`.

Structured lines use:

- `event=<name>`
- `status=<ok|slow|error|started|stopped|...>`
- `duration_ms=<int>` when timing matters

## Event catalog

### `event=update-handle`

- **Source:** `routes/root.go` (`UpdateRouter.Run`)
- **When:** Each Telegram update (`message`, `command`, `callback_query`, `other`).
- **Fields:** `kind`, `status`, `duration_ms` (+ `err` on error)

### `event=raw-update`

- **Source:** `routes/root.go` (`HandleUpdate`)
- **When:** Full update JSON logged at info (trim in production if noisy).

### `event=http-server`

- **Source:** `bg-services/handle-http.go`
- **When:** HTTP server start, stop, or fatal listen error.
- **Fields:** `status` (`starting` / `stopped` / `error`), `addr` (on start)

### `event=oauth-callback`

- **Source:** `bg-services/handle-http.go`
- **When:** Spotify redirect exchange fails.
- **Fields:** `status=error`, `err`

### `event=polling-worker`

- **Source:** `bg-services/handle-polling.go`
- **When:** Playback polling worker starts or stops.
- **Fields:** `status`, `playing_ms`, `paused_ms`, `inactive_ms`, `tick_ms` (on start)

Adaptive per-user intervals (see `services/polling/schedule.go`): **2s** playing / first 15s paused, **5s** paused 15s+, **5s** recently inactive, **60s** inactive 180s+.

### `event=poll-run`

- **Source:** `services/polling/coordinator.go`
- **When:** Listing connected users fails for a sweep.
- **Fields:** `status=error`, `step=list_users`, `err`

### `event=poll-user`

- **Source:** `services/polling/coordinator.go`
- **When:** Each due Spotify poll for a connected user.
- **`status=snapshot`:** `device_active`, `playing`, `track_id`, `progress_ms`, `prev_*`, `wall_ms`, `progress_delta_ms`
- **`status=done`:** `outcome` (`no_like`, `like_edge`, `like_stall` if `GESTURE_STALL_ENABLED=true`), `next_poll_ms`, `schedule_reason` (`playing`, `paused_fresh`, `paused_under_15s`, `paused_15s_plus`, `inactive_under_180s`, `inactive_180s_plus`)
- **`status=error`:** `step` (`load_state`, `spotify_api`, `gesture_eval`, `save_state`), `err`

### `event=gesture-eval`

- **Source:** `services/gesture/engine.go`
- **When:** Gesture decision on each poll (`outcome`, `detail`, optional `track_id` / timing fields).
- **Outcomes include:** `no_device`, `gesture_check_error`, `like_gesture_disabled`, `pause_started`, `still_paused`, `like_edge`, `like_stall`, `resume_too_slow`, `stall_too_long`, `like_skipped`, `no_gesture`

### `event=gesture-like`

- **Source:** `services/polling/coordinator.go`
- **When:** Track saved (or save failed).
- **Fields:** `telegram_id`, `track_id`, `source` (`edge` / `stall`), `status` (`ok` / `skipped` / `error`), `reason` on skip (`gesture_disabled`, `already_in_library`)

### `event=spotify-api`

- **Source:** `services/spotify/client.go`
- **When:** Spotify API rate limit during retries.
- **Fields:** `status=rate_limited`, `attempt`

### `event=deferred-write` / `event=deferred-write-worker`

- **Source:** `utils/db/deferred.go`

### `event=analytics-worker` / `event=analytics-insert`

- **Source:** `use-cases/handle-events/root.go`

### `event=broadcast-run`

- **Source:** `use-cases/handle-broadcast/root.go`
- **When:** One broadcast scheduler pass completes.

## Route warnings

- `command route: ensure user failed`
- `message route: ensure user failed`
- `callback: ensure user failed`

## Analytics pairing

| Log `event=` | Analytics `event_name` (when applicable) |
|--------------|------------------------------------------|
| `gesture-like` ok/error | `gesture-like` |
| `oauth-callback` error | `spotify-oauth-callback` error |

Persisted analytics live in **`app_analytics`**; see **`analytics.md`**.
