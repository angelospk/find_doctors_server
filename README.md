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
```json
{ "data": [
  { "hunitId": "025", "hunit": 65, "hunittype": 1,
    "name": "Γ.Ν. ΑΣΚΛΗΠΙΕΙΟ ΒΟΥΛΑΣ", "city": "ΒΟΥΛΑ", "zip": "16673",
    "phone1": "", "address": "...", "foreasId": 1,
    "firstDate": "2026-06-09", "distanceKm": 12.4 }
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

#### Doctor Nearby *(experimental)*
`GET /api/doctors/nearby?specialtyId=24&lat=37.9&lon=23.7&distance=10&foreasId=19`

Geo variant via the Ministry `searchdoctors/currentlocation` endpoint. ⚠️ The
upstream geo endpoint currently rejects requests (`400`/`500`) regardless of
payload, so this surfaces a `502` — left wired for when upstream is fixed.

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

## 🔒 Security

- **Public Access**: Authentication is **not required** for read-only discovery endpoints:
    - `/api/search`
    - `/api/specialties`
    - `/api/hospitals/{hunitId}/capacity`
- **Ministry API Integration**: This aggregator interacts with the official portal using their public `Authorization: no-auth` protocol.
- **Privacy Model**: No user cookies or session data are stored. The system is designed for stateless discovery only.
