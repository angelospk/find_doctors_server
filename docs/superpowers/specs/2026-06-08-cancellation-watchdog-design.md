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
| `CurrentDate` | *string | **latest observed** first-available date (can move up as slots fill, or down on a cancellation); nil until the first successful observation |
| `LastNotifiedDate` | *string | earliest date the user has been successfully told about; the retry/dedup guard |
| `TelegramChatID` | *string | optional channel |
| `WebhookURL` | *string | optional channel (must be http/https, validated) |
| `Status` | string | `active` \| `expired` \| `cancelled` |
| `CreatedAt` / `ExpiresAt` / `LastCheckedAt` | time | lifecycle |

**`Store` interface** (SQLite-backed)
- `Create(ctx, Watch) (Watch, error)`
- `Get(ctx, id) (Watch, error)` — `ErrNotFound` when missing
- `Delete(ctx, id) error` — sets `status=cancelled`
- `ListActive(ctx) ([]Watch, error)` — `status=active AND expiresAt > now`
- `Update(ctx, Watch) error` — persists currentDate/lastNotified/lastChecked/status
  via **conditional SQL** (`WHERE id=? AND status='active'`) so it can't resurrect a
  cancelled/expired row from a stale in-memory snapshot.

Single `*sql.DB` (`modernc.org/sqlite`), schema created on startup (idempotent
`CREATE TABLE IF NOT EXISTS`). DB path from `WATCH_DB_PATH` (default `./watchdog.db`).

**Concurrency policy** (poller writes + HTTP reads/writes share the DB):
- Open with WAL + busy timeout pragmas: `_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)`.
- `db.SetMaxOpenConns(1)` for the MVP — write volume is tiny (one batch per poll
  interval) and a single connection sidesteps SQLite writer-lock contention entirely.
- Transactions are short and never wrap an upstream or notifier call — the poller
  reads the active set, releases the DB, does network I/O, then writes results back.

**`Poller`**
- Started as a goroutine from `main.go`, bound to the existing signal context.
- **Shutdown is bounded, not just "ctx-cancelled":** on ctx cancel the ticker stops
  accepting new ticks; an in-flight tick's worker pool drains against a deadline
  (`WATCH_SHUTDOWN_GRACE`, default 10s); every upstream and notifier call uses its own
  per-call timeout (5s) derived from the tick context, so a wedged webhook/upstream
  can't hang shutdown. `main.go` coordinates HTTP-server and poller shutdown via an
  `errgroup`/`WaitGroup` so the process exits cleanly within the grace window.
- Every `WATCH_POLL_INTERVAL` (default `5m`, jittered ±20%): `ListActive`, then for
  each watch call `FirstAvailableSlot` through the aggregator's existing
  rate-limited client (respects `MINISTRY_MAX_CONCURRENCY`; bounded worker pool).
- Dates are `YYYY-MM-DD` strings, so lexicographic order == chronological order.
- **Seed at creation (no swallow window).** `POST /api/watches` performs one
  synchronous `FirstAvailableSlot` call and seeds `CurrentDate = LastNotifiedDate =`
  that date. This closes the gap Codex flagged: a cancellation appearing between
  create-time and the first poll would otherwise be silently absorbed into the
  baseline. If the seed call fails (or returns empty), both stay nil and the poller
  seeds on its first successful observation instead.
- **Notify state model** (unambiguous, retry-safe):
  - *Seeding* (create-time, or the poller's first successful observation if seeding
    failed) sets `CurrentDate = LastNotifiedDate = d` and sends **no** notification.
  - *Each later poll* with observed non-empty `d`:
    - `CurrentDate = d` (always the latest observed first-date — may be later or earlier).
    - If `d < LastNotifiedDate`: an earlier slot the user hasn't been told about →
      attempt notifiers. **Only when at least one configured notifier succeeds** set
      `LastNotifiedDate = d`.
  - `LastNotifiedDate` is the single dedup + retry guard: if every notifier fails it
    lags `CurrentDate`, so the next tick re-attempts (the condition `d < LastNotifiedDate`
    still holds). Per-channel delivery state is **not** tracked in MVP — "at least one
    channel delivered" advances the guard; per-channel retry is a future extension.
- Expiry: when `now > ExpiresAt`, set `status=expired` and stop polling it.
- Per-watch upstream error: log + metric, leave `CurrentDate`/`LastNotifiedDate`
  untouched, retry next tick. First poll returning empty → leave nil, retry.
- **Cancel/notify race:** the poller re-reads (or conditionally updates) watch status
  immediately before notifying. State writes use conditional SQL
  (`UPDATE ... WHERE id=? AND status='active'`) so a watch `DELETE`d mid-tick never
  fires a notification and never resurrects.

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
Validates required ints, `expiresInDays` clamped to 1–`WATCH_MAX_DAYS` (default 14),
webhook URL scheme http/https. Performs the synchronous seed poll (above), then
returns `201` with the created watch (envelope: bare object). A failed seed call
does not fail the request — the watch is created with `currentDate=null` and the
poller seeds later.

`GET /api/watches/{id}` → current state (`currentDate`, `lastNotifiedDate`,
`lastCheckedAt`, `status`). `404` JSON envelope when unknown.

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
| `WATCH_SHUTDOWN_GRACE` | `10s` | max drain time for an in-flight poll tick on shutdown |
| `TELEGRAM_BOT_TOKEN` | (unset) | enables Telegram channel |

Watchdog is **opt-in**: with no watches it does nothing; Telegram is inert without a
token. No behavior change for existing endpoints.

## Testing (TDD)

- **Store:** CRUD round-trip + `ListActive` filtering on a temp SQLite file
  (`t.TempDir()`); expired/cancelled excluded.
- **Poller (primary eval criterion):** mock client returning seed→later→earlier→same
  sequences. Assert: seeding sets `LastNotifiedDate` with no notify; an earlier date
  fires the notifier **exactly once** with the new date and advances
  `LastNotifiedDate`; a later/equal date does nothing; an upstream error leaves state
  intact and retries.
- **Notify retry (eval criterion):** notifier fails on first attempt → `LastNotifiedDate`
  does **not** advance → next tick with the same earlier date re-fires; succeeds →
  advances and stops re-firing.
- **Cancel/notify race (eval criterion):** `DELETE` between `ListActive` and the
  notify step → conditional update is a no-op and **no** notification is sent.
- **Seed-at-creation:** `POST` performs the synchronous seed; a date appearing only
  after create is detected; a date already present at create is the baseline and does
  not notify.
- **Store:** CRUD round-trip + `ListActive` filtering (expired/cancelled excluded) on
  a `t.TempDir()` SQLite file, WAL enabled.
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
