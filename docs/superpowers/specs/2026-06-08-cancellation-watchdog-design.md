# Cancellation Watchdog (ICYMI) — Design

**Issue:** #5 — "Background worker to poll `/firstavailableslot` and notify user if an earlier date appears."
**Date:** 2026-06-08
**Status:** Approved design → ready for implementation plan

## Goal

Let a user register interest in a specific health unit + specialty and get pinged
when an **earlier** first-available appointment date appears (typically because
someone cancelled). The server already exposes `FirstAvailableSlot`; this feature
adds the registry, the background poller, and the notification last mile.

## Scope (MVP)

- **Per-unit watches only.** A watch tracks one `hunitId` + `specialtyId` (plus the
  `foreasId`/`prefectureId` that `FirstAvailableSlot` needs). Search-wide watches
  ("any unit for this specialty near me") are a noted future extension, not in scope.
- **Channels:** Telegram (human last mile) and webhook (for a frontend/integration).
  A watch may set either, both, or neither (neither = poll-only via `GET`).
- **Persistence:** SQLite (pure-Go `modernc.org/sqlite`), survives restarts.

Out of scope: user accounts/auth, email/web-push, multi-unit watches, booking.

## Architecture

New package `internal/watch`, plus three API handlers and `main.go` wiring.

```
POST /api/watches ─┐
GET  /api/watches/{id}   →  Store (SQLite)  ←──  Poller (ticker goroutine)
DELETE /api/watches/{id} ─┘                          │
                                                     ├─ aggregator.FirstAvailableSlot (rate-limited client)
                                                     └─ Notifier(s): Telegram, Webhook
```

### Components

**`Watch` (model)**
| field | type | notes |
|-------|------|-------|
| `ID` | string | server-generated (crypto-random hex) |
| `HUnitID` | int | target unit |
| `SpecialtyID` | int | target specialty |
| `ForeasID` | int | unit type (1/18/19…), needed for the upstream call |
| `PrefectureID` | *int | optional, passed through to the upstream payload |
| `BaselineDate` | *string | best (earliest) first-date seen so far; nil until first successful poll |
| `LastNotifiedDate` | *string | guards against duplicate notifications for the same date |
| `TelegramChatID` | *string | optional channel |
| `WebhookURL` | *string | optional channel (must be http/https, validated) |
| `Status` | string | `active` \| `expired` \| `cancelled` |
| `CreatedAt` / `ExpiresAt` / `LastCheckedAt` | time | lifecycle |

**`Store` interface** (SQLite-backed)
- `Create(ctx, Watch) (Watch, error)`
- `Get(ctx, id) (Watch, error)` — `ErrNotFound` when missing
- `Delete(ctx, id) error` — sets `status=cancelled`
- `ListActive(ctx) ([]Watch, error)` — `status=active AND expiresAt > now`
- `Update(ctx, Watch) error` — persists baseline/lastNotified/lastChecked/status

Single `*sql.DB` (`modernc.org/sqlite`), schema created on startup (idempotent
`CREATE TABLE IF NOT EXISTS`). DB path from `WATCH_DB_PATH` (default `./watchdog.db`).

**`Poller`**
- Started as a goroutine from `main.go`, bound to the existing signal context so it
  drains on shutdown.
- Every `WATCH_POLL_INTERVAL` (default `5m`, jittered ±20%): `ListActive`, then for
  each watch call `FirstAvailableSlot` through the aggregator's existing
  rate-limited client (respects `MINISTRY_MAX_CONCURRENCY`; bounded worker pool).
- Dates are `YYYY-MM-DD` strings, so lexicographic order == chronological order.
- **Notify state model** (unambiguous, retry-safe):
  - *First successful poll* sets **both** `BaselineDate = d` and `LastNotifiedDate = d`
    and sends **no** notification. Rationale: the user got the starting date from the
    search before creating the watch, so the baseline is already "known".
  - *Each later poll* with observed non-empty `d`:
    - `BaselineDate = d` (it always reflects the latest observed first-date).
    - If `d < LastNotifiedDate`: an improvement the user hasn't been told about →
      attempt notifiers. **Only on notifier success** set `LastNotifiedDate = d`.
  - This makes `LastNotifiedDate` the single retry guard: if notify fails,
    `LastNotifiedDate` lags `BaselineDate`, and the next tick (observing the same or
    an even earlier `d`) re-attempts because `d < LastNotifiedDate` still holds.
- Expiry: when `now > ExpiresAt`, set `status=expired` and stop polling it.
- Per-watch upstream error: log + metric, leave `BaselineDate`/`LastNotifiedDate`
  untouched, retry next tick.

**`Notifier` interface**: `Notify(ctx, Watch, newDate string) error`
- `TelegramNotifier` — POSTs to `https://api.telegram.org/bot<token>/sendMessage`
  with `chat_id` + a human message ("Βρέθηκε νωρίτερο ραντεβού: <date> …"). Token
  from `TELEGRAM_BOT_TOKEN`; if unset, Telegram notifications are skipped (logged).
- `WebhookNotifier` — POSTs JSON `{watchId, hunitId, specialtyId, newDate, previousDate}`
  to the watch's `WebhookURL`, 5s timeout, treats non-2xx as failure.
- Notifier failure → log + metric; retried next tick because baseline only advances
  after at least one notifier path is attempted (see Error Handling).

### API

`POST /api/watches` — body:
```json
{ "hunitId": 718, "specialtyId": 24, "foreasId": 1, "prefectureId": 5,
  "telegramChatId": "123456789", "webhookUrl": "https://example.com/hook",
  "expiresInDays": 14 }
```
Validates required ints, `expiresInDays` clamped to 1–30 (default 14), webhook URL
scheme http/https. Returns `201` with the created watch (envelope: bare object).

`GET /api/watches/{id}` → current state (`baselineDate`, `lastCheckedAt`, `status`).
`404` JSON envelope when unknown.

`DELETE /api/watches/{id}` → `204`, idempotent.

All errors use the existing `writeJSONError` envelope and codes.

## Error Handling

- **Upstream poll failure:** counted in a `watch_poll_failures_total` metric, logged
  at WARN; watch is skipped this tick (no baseline change, no notify).
- **Notifier failure:** logged + `watch_notify_failures_total`. `LastNotifiedDate`
  advances **only** when at least one configured notifier succeeds. If all fail,
  `LastNotifiedDate` stays behind `BaselineDate`, so the next tick re-attempts the
  notification (see the Notify state model above). A watch with neither channel set
  is poll-only: `LastNotifiedDate` simply tracks `BaselineDate` and the user reads
  state via `GET /api/watches/{id}`.
- **DB failure on startup:** fatal (server exits) — persistence is a hard dependency.
- **Bad input:** 400 with envelope, before any DB write.

## Configuration (env)

| var | default | meaning |
|-----|---------|---------|
| `WATCH_DB_PATH` | `./watchdog.db` | SQLite file |
| `WATCH_POLL_INTERVAL` | `5m` | poll cadence |
| `WATCH_MAX_DAYS` | `30` | hard cap on `expiresInDays` |
| `TELEGRAM_BOT_TOKEN` | (unset) | enables Telegram channel |

Watchdog is **opt-in**: with no watches it does nothing; Telegram is inert without a
token. No behavior change for existing endpoints.

## Testing (TDD)

- **Store:** CRUD round-trip + `ListActive` filtering on a temp SQLite file
  (`t.TempDir()`); expired/cancelled excluded.
- **Poller:** mock client returning later→earlier→same sequences. Assert: first poll
  seeds baseline with no notify; an earlier date fires the notifier exactly once with
  the new date and advances baseline; a later/equal date does nothing; upstream error
  leaves state intact.
- **Notifiers:** `httptest.Server` asserting the Telegram `sendMessage` request shape
  and the webhook JSON payload; non-2xx → error.
- **API:** create (happy + validation failures), get, delete, unknown-id 404.
- **Race:** poller + concurrent API access under `-race`.

## Decisions (locked during brainstorming)

1. Notify mechanism: webhook + polling endpoint (core), **Telegram** as the human channel.
2. Persistence: **SQLite** (pure-Go), survives restart.
3. Scope: **per-unit** watches for MVP; search-wide deferred.

## Future extensions (not now)

- Search-wide watches (specialty+prefecture across many units).
- Email / Web Push channels behind the same `Notifier` interface.
- Per-watch quiet hours; max-notifications cap.
