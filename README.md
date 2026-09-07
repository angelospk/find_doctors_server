# FindDoctors Aggregator Server (Golang) 🏥

A high-performance, type-safe backend aggregator for the Hellenic Health Appointment System (`finddoctors.gov.gr`). 

## Overview
This server acts as a specialized proxy that fixes the primary limitation of the official health portal: the inability to search across different entities (Public Hospitals vs. Primary Care Centers) and regions simultaneously.

### Key Features
- **Smart Search (Unified)**: Consolidates nationwide and proximity searches into a single `/api/search` endpoint with "Distance-Filtered Soonest" logic.
- **Parallel Cross-Entity Search**: Queries both Public Hospitals (`foreas: 1`) and Primary Health Centers (`foreas: 18`) concurrently, merging results into a unified view.
- **Actionable Capacity Reports**: Enhanced hospital load reports that include the earliest available appointment (`firstDate`) for every specialty.
- **Fast Scanner Engine**: Uses a concurrent worker pool (semaphore pattern) to probe multiple health units for their `firstavailableslot` with sub-second latency.
- **Type Safety**: End-to-end type safety from Go to TypeScript via `tygo`.
- **TDD-Backed**: 100% test coverage for core logic and aggregator sorting algorithms.

## The app

`GET /` serves a single Greek-language page: choose a specialty, optionally a
prefecture or your own location and a radius, and get every public hospital and
primary-care centre ranked by how soon they can see you — with the wait in
plain words ("σε 2 μέρες", "σε 3 μήνες"), the times for that date, and a way to
set an earlier-date alert.

It is one embedded HTML file (`internal/web/app.html`), no build step and no
node_modules. One person maintains this; a bundler that rots is worse than
markup that is a bit long.

Two things it says out loud, because both are easy to get wrong:

- A unit whose availability check failed is shown **without a date**, labelled
  "δεν απάντησε το σύστημα". That is not the same as "no appointments", and the
  banner above the results says how many were in that state.
- A watch created from the page has no Telegram id and no webhook, so **nothing
  is pushed anywhere**. The button says "κράτα το υπό παρακολούθηση" rather than
  "πες μου", and puts a "δες αν άλλαξε" button next to it that reads the watch
  back. Pushed alerts need `POST /api/watches` with a `telegramChatId`.
- The page does not book anything and does not hold a slot. Every result has a
  link to the official portal, where the booking actually happens.

## 🚀 API Endpoints

> **Requires Go 1.26+ to build** (see `go.mod`).

The public REST API listens on `:8080` (override with `LISTEN_ADDR`). A separate
admin server on `:9090` (`ADMIN_ADDR`) exposes Prometheus `/metrics` and a fast
`/healthz`. All responses are `application/json`.

### Conventions

**Error envelope** — every error (4xx/5xx) returns:
```json
{ "error": { "code": "missing_param", "message": "missing specialtyId" } }
```
Codes you'll see: `missing_param`, `invalid_param`, `not_found`,
`upstream_failure` (502 — the Ministry API failed or returned a non-200),
`feature_unavailable` (501), `upstream_unavailable` (503, health probe only).

**Pagination** — endpoints that return a `{ "data": [...], "meta": {...} }`
envelope accept `limit` (default 50, max 200) and `offset` (default 0):
```json
{ "data": [ /* ... */ ], "meta": { "total": 128, "limit": 50, "offset": 0 } }
```
Discovery/list endpoints (specialties, prefectures, foreas, machine types) return
a **bare array** instead.

**`foreasId`** (health-unit type) — `1` = Public Hospitals (ΕΣΥ), `18` = Primary
Care (ΠΦΥ / Κέντρα Υγείας), `19` = Private contracted with ΕΟΠΥΥ, `20` = Private.
Fetch the live list from `GET /api/foreas`.

---

### Health & Ops

| Endpoint | Purpose |
|----------|---------|
| `GET /healthz`, `GET /livez` | Liveness. Always `{"status":"ok"}`, no upstream dependency. |
| `GET /readyz` | Process readiness — `{"status":"ready"}`. **Does not** probe the Ministry API on purpose, so it won't flap during upstream hiccups. |
| `GET /healthz/upstream` | Live, cache-bypassing Ministry reachability probe. `{"upstream":"up","cached":false}` on success (cached 30s), or `503` + error envelope when down. |
| `GET /metrics` *(admin :9090)* | Prometheus metrics (request counts, upstream latency, retries, cache hits). |

---

### Search & Availability

#### Smart Search (Unified)
`GET /api/search?specialtyId=24&lat=37.9&lon=23.7&maxDistanceInKm=100&prefectureId=5`

Queries Public Hospitals and Primary Care in parallel, then ranks by soonest slot
within the distance filter. Params: `specialtyId` (**required**), `lat`/`lon`,
`maxDistanceInKm`, `prefectureId` (all optional). Supports pagination.

`distanceKm` is present only when `lat`/`lon` were given **and** the unit's
upstream record has usable coordinates. It is a great-circle distance, not a
driving distance. `scanOk: false` means the availability check for that unit
failed — the unit is kept, with a null `firstDate`, because "we could not ask"
is not "there is nothing".
```json
{ "data": [
  { "hunitId": "025", "hunit": 65, "hunittype": 1,
    "name": "Γ.Ν. ΑΣΚΛΗΠΙΕΙΟ ΒΟΥΛΑΣ", "city": "ΒΟΥΛΑ", "zip": "16673",
    "phone1": "", "address": "...", "foreasId": 1,
    "firstDate": "2026-06-09", "scanOk": true, "distanceKm": 12.4 }
], "meta": { "total": 30, "limit": 50, "offset": 0 } }
```

#### Nationwide Capacity Heatmap
`GET /api/heatmap?specialtyId=24`

Prefecture-level fill-rate aggregation for a specialty, sorted most-open first.
Failed upstream scans are excluded from the denominator and surfaced via
`failedScans`/`partial` so you can flag degraded data during an outage.
```json
{ "specialtyId": 24,
  "prefectures": [
    { "prefectureId": 5, "unitCount": 10, "avgFillRate": 0, "firstDate": "2026-06-09" },
    { "prefectureId": 6, "unitCount": 3,  "avgFillRate": 33.3, "firstDate": "2026-06-17" }
  ],
  "scannedUnits": 41, "failedScans": 0, "partial": false }
```

#### Hospital Capacity
`GET /api/hospitals/{hunitId}/capacity?specialtyId=24&foreasId=1&prefectureId=5`

Weekly fill-rate report per specialty for one unit. `specialtyId` optional (omit
for all departments); `foreasId` defaults to `1`. The `disabled`-day fix (#16)
adds `scheduleDays`, `activeGroups`, `disabledSlots` so the UI can explain how a
fill rate was computed.
```json
{ "hunitId": 70600, "scanned": 1,
  "specialties": [
    { "specialtyId": 24, "name": "ΑΚΤΙΝΟΔΙΑΓΝΩΣΤΗΣ", "fillRate": 0,
      "firstDate": null, "scheduleDays": [], "activeGroups": 0, "disabledSlots": 0 }
  ] }
```

#### Granular Appointment Slots
`GET /api/hospitals/{hunitId}/slots?specialtyId=24&date=2026-06-20&prefectureId=5&foreasId=1`

Detailed times, doctor names, and clinic metadata for a unit on a date.
Required: `specialtyId`, `date` (YYYY-MM-DD), `prefectureId`, `foreasId`.
Optional: `timeOfDay` (`morning`|`noon`|`afternoon`, #7), `cDoorId` (clinic door, #20).
Returns an array of slot objects (`time`, `date`, `docName`, `address`, `comments`, …).

#### Clinic Doors
`GET /api/hospitals/{hunitId}/doors?specialtyId=24`

Lists clinic doors (`[{ "cDoorId": 1, "name": "..." }]`) to feed into the
`cDoorId` slot filter. ⚠️ Returns `502` when the upstream door lookup has no data
for that unit/specialty.

---

### Doctors, Specialised Modes

#### Named Doctor Search
`GET /api/doctors/search?specialtyId=24&prefectureId=5&foreasId=19&firstName=&lastName=`

Named physicians (ΕΟΠΥΥ private by default, `foreasId=19`) with AMKA, address, and
coordinates. `specialtyId` **required**; `firstName`/`lastName` optional filters
(≤100 chars). Paginated `{data, meta}`. Empty result is `{"data":[],"meta":{...}}`,
not a placeholder row.

#### Doctor Nearby *(deprecated)*
`GET /api/doctors/nearby?specialtyId=24&lat=37.9&lon=23.7&distance=10&foreasId=19`

Geo variant via the Ministry `searchdoctors/currentlocation` endpoint. **It has
never returned a result**: upstream rejects every payload with a `400` or a
`500`, so this always surfaces a `502`. Responses carry an RFC 9745
`Deprecation: @1757203200` header and a `Link` header pointing at `/api/search`,
which takes `lat`/`lon` and does the distance filtering on this side. The wiring stays because it is correct and
costs nothing — but do not build against it.

#### Family / Personal Doctor
`GET /api/family-doctors/search?specialtyId=24&prefectureId=5`

Family-doctor mode (`isOnlyFd=1`). Returns a hybrid `{ "doctors": [...], "units": [...] }`.
Upstream is largely per-patient, so nationwide queries often come back empty.

#### COVID & Mental Health
- `GET /api/covid/search?specialtyId=&prefectureId=&foreasId=` — vaccination centres (`isCovid=1`).
- `GET /api/mental-health/search?specialtyId=&prefectureId=&foreasId=` — mental-health units (`rvtypeId=15`, `isMentalHealth=1`).

Both return a bare array of units.

#### Diagnostic Machines
- `GET /api/machines/types` — available exam types: `[{ "rvTypeId": 10, "name": "Αιματολογικές Εξετάσεις", "payType": 1, "isMachine": 1 }]`. `payType=1` means the exam is charged.
- `GET /api/machines/search?prefectureId=5&rvTypeId=10` — units offering that exam. Wrapped as `{ "disclaimer": "...", "units": [...] }`.

---

### Discovery / Metadata

| Endpoint | Returns |
|----------|---------|
| `GET /api/specialties` | `[{ "speciality": 24, "name": "ΑΚΤΙΝΟΔΙΑΓΝΩΣΤΗΣ" }]` |
| `GET /api/foreas` | `[{ "hUnitType": 1, "name": "Νοσοκομείο", "isActive": 1 }]` — source of truth for `foreasId`. |
| `GET /api/prefectures` | `[{ "id": 5, "name": "ΑΤΤΙΚΗΣ" }]` |
| `GET /api/prefectures/covid` | Prefectures with COVID vaccination centres. |
| `GET /api/prefectures/mental-health` | Prefectures with mental-health units. |

### Earlier-date alerts

Register a watch on a specific unit + specialty; a background poller re-checks the
first-available date and alerts you when an **earlier** date appears.

The name matters. This was called the "cancellation watchdog", which promises
more than it does: an earlier date usually comes from a newly released schedule
rather than from somebody cancelling, it watches one unit and one specialty
rather than searching for alternatives, and it does not notice a new time
opening up on a date it already knows about. Notifications go to Telegram and/or a webhook; either way the current
state is always readable via `GET`.

`POST /api/watches`
```json
{ "hunitId": 718, "specialtyId": 24, "foreasId": 1, "prefectureId": 5,
  "telegramChatId": "123456789", "webhookUrl": "https://example.com/hook",
  "expiresInDays": 14 }
```
`hunitId`, `specialtyId`, `foreasId` are **required**; `telegramChatId`/`webhookUrl`
are optional channels (set neither to use poll-only). `expiresInDays` is clamped to
1–30 (default 14). On create the server does one synchronous first-available check to
seed the baseline, then returns `201` with the watch:
```json
{ "id": "5931bdd8…", "hunitId": 718, "specialtyId": 24, "foreasId": 1,
  "currentDate": "2026-09-01", "lastNotifiedDate": "2026-09-01",
  "status": "active", "createdAt": "…", "expiresAt": "…" }
```

`GET /api/watches/{id}` → current state (`currentDate`, `lastNotifiedDate`,
`lastCheckedAt`, `status`); `404` if unknown.
`DELETE /api/watches/{id}` → `204`, idempotent (sets `status: "cancelled"`).

**Webhook payload** (POST to your `webhookUrl`):
```json
{ "watchId": "…", "hunitId": 718, "specialtyId": 24,
  "newDate": "2026-07-01", "previousDate": "2026-09-01" }
```

Config (env): `WATCH_STATE_PATH` (default `./watchdog-state.json`),
`WATCH_POLL_INTERVAL` (default `5m`), `TELEGRAM_BOT_TOKEN` (enables Telegram). State
persists across restarts via an atomic JSON snapshot. Metrics:
`watch_poll_failures_total`, `watch_notify_failures_total`.

## 🏗️ Development

### Running Tests (TDD)
We follow a strict TDD workflow. Run tests to verify search and sorting logic:
```bash
go test -v -race ./...
```

### Type Generation
To sync types with the frontend:
```bash
tygo generate
```

### Starting the Server
```bash
go run ./cmd/server/main.go
```
The server will be available at `http://localhost:8080`.

## Before this is exposed publicly

It runs on one machine, for one person. Three things would have to change first,
and none of them is urgent while that stays true:

- **`/api/doctors/search` returns each physician's AMKA**, because upstream does.
  A doctor's AMKA is still personal data, and republishing it to anyone who can
  reach the port is a different act from the ministry publishing it behind their
  own portal. The slot lookup for private ΕΟΠΥΥ doctors needs the identifier, so
  removing it means introducing an opaque server-side reference — worth doing
  before a public deployment, not before that.
- **`GET /api/doctors/{amka}/slots` puts that identifier in the URL**, which means
  in every access log and every proxy in front of it.
- **The TaxisNet bridge is single-user by construction.** It proxies calls through
  *Harold's* logged-in browser session. Sharing that session among visitors would
  be making requests to the ministry in his name; it must stay off any public
  deployment.

## 🔒 Security

- **Public Access**: Authentication is **not required** for read-only discovery endpoints:
    - `/api/search`
    - `/api/specialties`
    - `/api/hospitals/{hunitId}/capacity`
- **Ministry API Integration**: This aggregator interacts with the official portal using their public `Authorization: no-auth` protocol.
- **Privacy Model**: No user cookies or session data are stored. The system is designed for stateless discovery only.
