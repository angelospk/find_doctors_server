# FindDoctors Aggregator Server (Go)

A backend aggregator for the Greek health appointment system (`finddoctors.gov.gr`).

## Overview
The official portal searches one entity type (public hospitals or primary care
centres) and one region at a time. This server searches both types and all regions
in one request.

### What it does
- `/api/search` handles both nationwide and proximity searches: it ranks units
  by the soonest appointment, within an optional distance limit.
- It queries public hospitals (`foreas: 1`) and primary care centres (`foreas: 18`)
  in parallel and merges the results.
- Hospital capacity reports include the earliest available appointment
  (`firstDate`) for every specialty.
- A worker pool with a concurrency limit probes many units for their
  `firstavailableslot` at once.
- Go types are shared with TypeScript through `tygo`.

## The app

`GET /` serves a single Greek-language page. The user picks a specialty and,
optionally, a prefecture or their own location and a radius. The page lists every
public hospital and primary-care centre, ranked by how soon it can see them. Each
result shows the wait in plain words ("σε 2 μέρες", "σε 3 μήνες"), the times for
that date, and a way to set an earlier-date alert.

It is one embedded HTML file (`internal/web/app.html`), with no build step and no
node_modules. One person maintains this, and a bundler that rots is worse than
markup that is a bit long.

The page is explicit about points that are easy to get wrong:

- A unit whose availability check failed is shown **without a date**, labelled
  "δεν απάντησε το σύστημα". That is not the same as "no appointments", and the
  banner above the results says how many units were in that state.
- A watch created from the page has no Telegram id and no webhook, so **nothing
  is pushed anywhere**. The button says "κράτα το υπό παρακολούθηση" rather than
  "πες μου", and a "δες αν άλλαξε" button next to it reads the watch back. Pushed
  alerts need `POST /api/watches` with a `telegramChatId`.
- The page does not book anything and does not hold a slot. Every result links to
  the official portal, where the booking happens.

## API Endpoints

> **Requires Go 1.26+ to build** (see `go.mod`).

The public REST API listens on `:8080` (override with `LISTEN_ADDR`). A separate
admin server on `:9090` (`ADMIN_ADDR`) exposes Prometheus `/metrics` and a fast
`/healthz`. All responses are `application/json`.

### Conventions

**Error envelope.** Every error (4xx/5xx) returns:
```json
{ "error": { "code": "missing_param", "message": "missing specialtyId" } }
```
The codes are `missing_param`, `invalid_param`, `not_found`,
`upstream_failure` (502: the Ministry API failed or returned a non-200),
`feature_unavailable` (501), and `upstream_unavailable` (503, health probe only).

**Pagination.** Endpoints that return a `{ "data": [...], "meta": {...} }`
envelope accept `limit` (default 50, max 200) and `offset` (default 0):
```json
{ "data": [ /* ... */ ], "meta": { "total": 128, "limit": 50, "offset": 0 } }
```
Discovery/list endpoints (specialties, prefectures, foreas, machine types) return
a **bare array** instead.

**`foreasId`** (health-unit type): `1` = public hospitals (ΕΣΥ), `18` = primary
care (ΠΦΥ / Κέντρα Υγείας), `19` = private, contracted with ΕΟΠΥΥ, `20` = private.
`GET /api/foreas` returns the live list.

---

### Health & Ops

| Endpoint | Purpose |
|----------|---------|
| `GET /healthz`, `GET /livez` | Liveness. Always `{"status":"ok"}`, no upstream dependency. |
| `GET /readyz` | Process readiness: `{"status":"ready"}`. It does **not** probe the Ministry API, so it stays steady during upstream outages. |
| `GET /healthz/upstream` | Live, cache-bypassing probe of the Ministry API. `{"upstream":"up","cached":false}` on success (cached 30s), or `503` + error envelope when down. |
| `GET /metrics` *(admin :9090)* | Prometheus metrics (request counts, upstream latency, retries, cache hits). |

---

### Search & Availability

#### Smart Search (Unified)
`GET /api/search?specialtyId=24&lat=37.9&lon=23.7&maxDistanceInKm=100&prefectureId=5`

Queries public hospitals and primary care in parallel, then ranks by soonest slot
within the distance filter. Params: `specialtyId` (**required**), `lat`/`lon`,
`maxDistanceInKm`, `prefectureId` (all optional). Supports pagination.

`maxProbes` caps how many units one search asks the Ministry about (nearest first
when `lat`/`lon` are given). Without it the server default holds (`UNIT_PROBE_LIMIT`,
60); `maxProbes=0` asks for every unit, as a nationwide directory scan must. A ceiling
(`UNIT_PROBE_MAX`, 1000) bounds every search, `maxProbes=0` included.
`meta.candidates` counts the units within the radius, `meta.probed` those checked,
and `meta.truncated: true` says the cap left some out, so the result is not every
unit that offers the specialty. `meta.failedSectors` counts the sectors (ΕΣΥ, ΠΦΥ)
whose Ministry search failed: the rest still answer, but the list is short.

`distanceKm` is present only when `lat`/`lon` were given **and** the unit's
upstream record has usable coordinates. It is a great-circle distance, not a
driving distance. `scanOk: false` means the availability check for that unit
failed. The unit stays in the list with a null `firstDate`, because "we could not
ask" is not "there is nothing".
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

Fill rate per prefecture for one specialty, most open first. Failed upstream scans
are left out of the denominator and reported in `failedScans`/`partial`, so a
client can flag degraded data during an outage.
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

Weekly fill rate per specialty for one unit. `specialtyId` is optional (omit it
for all departments); `foreasId` defaults to `1`. The `disabled`-day fix (#16)
adds `scheduleDays`, `activeGroups` and `disabledSlots`, so the UI can explain how
a fill rate was computed.
```json
{ "hunitId": 70600, "scanned": 1,
  "specialties": [
    { "specialtyId": 24, "name": "ΑΚΤΙΝΟΔΙΑΓΝΩΣΤΗΣ", "fillRate": 0,
      "firstDate": null, "scheduleDays": [], "activeGroups": 0, "disabledSlots": 0 }
  ] }
```

#### Appointment Slots
`GET /api/hospitals/{hunitId}/slots?specialtyId=24&date=2026-06-20&prefectureId=5&foreasId=1`

Times, doctor names and clinic details for one unit on one date.
Required: `specialtyId`, `date` (YYYY-MM-DD), `prefectureId`, `foreasId`.
Optional: `timeOfDay` (`morning`|`noon`|`afternoon`, #7), `cDoorId` (clinic door, #20).
Returns an array of slot objects (`time`, `date`, `docName`, `address`, `comments`, …).

#### Clinic Doors
`GET /api/hospitals/{hunitId}/doors?specialtyId=24`

Lists clinic doors (`[{ "cDoorId": 1, "name": "..." }]`) for the `cDoorId` slot
filter. An upstream failure returns `502`.

---

### Doctors, Specialised Modes

#### Named Doctor Search
`GET /api/doctors/search?specialtyId=24&prefectureId=5&foreasId=19&firstName=&lastName=`

Named physicians (private ΕΟΠΥΥ by default, `foreasId=19`) with AMKA, address and
coordinates. `specialtyId` is **required**; `firstName`/`lastName` are optional
filters (≤100 chars). Paginated `{data, meta}`. An empty result is
`{"data":[],"meta":{...}}`, not a placeholder row.

#### Doctor Nearby *(deprecated)*
`GET /api/doctors/nearby?specialtyId=24&lat=37.9&lon=23.7&distance=10&foreasId=19`

Geo variant through the Ministry `searchdoctors/currentlocation` endpoint. **It has
never returned a result**: upstream rejects every payload with a `400` or a
`500`, so this always returns a `502`. Responses carry an RFC 9745
`Deprecation: @1757203200` header and a `Link` header pointing at `/api/search`,
which takes `lat`/`lon` and filters by distance on this side. The wiring stays
because it is correct and costs nothing, but do not build against it.

#### Family / Personal Doctor
`GET /api/family-doctors/search?specialtyId=24&prefectureId=5`

Family-doctor mode (`isOnlyFd=1`). Returns `{ "doctors": [...], "units": [...] }`.
Upstream results depend largely on the patient, so nationwide queries often come
back empty.

#### COVID & Mental Health
- `GET /api/covid/search?specialtyId=&prefectureId=&foreasId=`: vaccination centres (`isCovid=1`).
- `GET /api/mental-health/search?specialtyId=&prefectureId=&foreasId=`: mental-health units (`rvtypeId=15`, `isMentalHealth=1`).

Both return a bare array of units.

#### Diagnostic Machines
- `GET /api/machines/types`: available exam types, e.g. `[{ "rvTypeId": 10, "name": "Αιματολογικές Εξετάσεις", "payType": 1, "isMachine": 1 }]`. `payType=1` means the exam is charged.
- `GET /api/machines/search?prefectureId=5&rvTypeId=10`: units that offer that exam, wrapped as `{ "disclaimer": "...", "units": [...] }`.

---

### Discovery / Metadata

| Endpoint | Returns |
|----------|---------|
| `GET /api/specialties` | `[{ "speciality": 24, "name": "ΑΚΤΙΝΟΔΙΑΓΝΩΣΤΗΣ" }]` |
| `GET /api/foreas` | `[{ "hUnitType": 1, "name": "Νοσοκομείο", "isActive": 1 }]`, the source for `foreasId`. |
| `GET /api/prefectures` | `[{ "id": 5, "name": "ΑΤΤΙΚΗΣ" }]` |
| `GET /api/prefectures/covid` | Prefectures with COVID vaccination centres. |
| `GET /api/prefectures/mental-health` | Prefectures with mental-health units. |

### Earlier-date alerts

A watch covers one unit and one specialty. A background poller re-checks the
first available date and sends an alert when an **earlier** date appears.

The old name, "cancellation watchdog", promised too much: an earlier date usually comes from a newly released schedule, not from a
cancellation. The watch does not search for alternatives in other units, and it
does not notice a new time on a date it already knows about. Notifications go to
Telegram and/or a webhook, and `GET` always returns the current state.

`POST /api/watches`
```json
{ "hunitId": 718, "specialtyId": 24, "foreasId": 1, "prefectureId": 5,
  "telegramChatId": "123456789", "webhookUrl": "https://example.com/hook",
  "expiresInDays": 14 }
```
`hunitId`, `specialtyId` and `foreasId` are **required**; `telegramChatId`/`webhookUrl`
are optional channels (set neither for poll-only). `expiresInDays` is clamped to
1–30 (default 14). On create, the server makes one synchronous first-available
check to set the baseline, then returns `201` with the watch:
```json
{ "id": "5931bdd8…", "hunitId": 718, "specialtyId": 24, "foreasId": 1,
  "currentDate": "2026-09-01", "lastNotifiedDate": "2026-09-01",
  "status": "active", "createdAt": "…", "expiresAt": "…" }
```

`GET /api/watches/{id}` returns the current state (`currentDate`, `lastNotifiedDate`,
`lastCheckedAt`, `status`), or `404` if the id is unknown.
`DELETE /api/watches/{id}` returns `204` and is idempotent (it sets `status: "cancelled"`).

**Webhook payload** (POST to the `webhookUrl`):
```json
{ "watchId": "…", "hunitId": 718, "specialtyId": 24,
  "newDate": "2026-07-01", "previousDate": "2026-09-01" }
```

Config (env): `WATCH_STATE_PATH` (default `./watchdog-state.json`),
`WATCH_POLL_INTERVAL` (default `5m`), `TELEGRAM_BOT_TOKEN` (enables Telegram). State
survives restarts through an atomic JSON snapshot. Metrics:
`watch_poll_failures_total`, `watch_notify_failures_total`.

## Development

### Running tests
```bash
go test -v -race ./...
```

### Type generation
To sync types with the frontend:
```bash
tygo generate
```

### Starting the server
```bash
go run ./cmd/server/main.go
```
The server listens on `http://localhost:8080`.

## Before this is exposed publicly

It runs on one machine, for one person. A public deployment would first need
these changes:

- **`/api/doctors/search` returns each physician's AMKA**, because upstream does.
  A doctor's AMKA is still personal data. Republishing it to anyone who can reach
  the port is a different act from the ministry publishing it behind its own
  portal. The slot lookup for private ΕΟΠΥΥ doctors needs the identifier, so
  removing it means an opaque server-side reference.
- **`GET /api/doctors/{amka}/slots` puts that identifier in the URL**, so it ends
  up in every access log and every proxy in front of the server.
- **The TaxisNet session is single-user by construction.** The server logs in to
  TaxisNet with *Harold's* credentials (`TAXISNET_USERNAME`, `TAXISNET_PASSWORD`,
  `MINISTRY_AMKA`), so every Ministry request is made in his name. A public
  deployment must not share that session among visitors.

## Security

- **Access**: The API has no authentication of its own. Read-only endpoints such as
  `/api/search`, `/api/specialties` and `/api/hospitals/{hunitId}/capacity` are open
  to anyone who can reach the port.
- **Ministry API**: Every upstream call needs a TaxisNet session (`JSESSIONID`)
  next to the `Authorization: no-auth` header; without the session the Ministry
  answers `403`. The server logs in and renews the session itself, and keeps it in
  `MINISTRY_SESSION_PATH` (default `./ministry-session.txt`) across restarts.
- **Visitor data**: The server sets no cookies and keeps no visitor sessions. The
  only state it stores is the watches (`WATCH_STATE_PATH`) and the Ministry session.
