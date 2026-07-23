# RESULT

## Task
Fix `internal/aggregator` silently dropping units whose upstream record lacks
geo coordinates (`Latitude==0 && Longitude==0`) when `SmartSearch` applies a
`MaxDistance` filter. Add a `hasCoords` predicate, keep coordinate-less units in
the results (sorted last), and add the missing tests.

## Changes
- `internal/aggregator/geo.go`
  - Added `hasCoords(lat, lon float64) bool`. Returns `false` for 0/0 records
    (the upstream default for a missing location) and for any `NaN` component;
    `true` otherwise.
- `internal/aggregator/search.go` (`SmartSearch`)
  - Distance filter now keeps units without usable coordinates instead of
    computing a bogus large distance and excluding them.
  - Sort tie-breaker: units with an unknown location sort after units that can
    be ranked by real distance (i.e. coordinate-less units go last).
- `internal/aggregator/geo_test.go` (new)
  - `TestDistance`: table test — Athens→Thessaloniki ≈ 300 km (±20),
    identical points = 0.
  - `TestHasCoords`: valid pair, 0/0, NaN lat, NaN lon, valid negative coords.
- `internal/aggregator/search_test.go`
  - Added subtest `Zero-Coordinate Unit Survives Restrictive MaxDistance`:
    proves a 0/0 unit is retained under `MaxDistance: 10` and is sorted last.

Functions modified by `feat/private-eopyy-slots` were not touched; only
`SmartSearch` (explicitly noted safe) and geo helpers were changed.

## Test output
`go test ./...`
```
?   github.com/angelospk/find_doctors_server/cmd/server	[no test files]
ok  github.com/angelospk/find_doctors_server/internal/aggregator	0.012s
ok  github.com/angelospk/find_doctors_server/internal/api	0.023s
ok  github.com/angelospk/find_doctors_server/internal/ministry	(cached)
ok  github.com/angelospk/find_doctors_server/internal/watch	(cached)
```
`go vet ./internal/aggregator/` — clean.

## Follow-ups
- The `sort.Slice` comparator is not a strict weak ordering when `FirstDate` is
  nil on both sides / one side (pre-existing behavior, unchanged here). If a
  fully deterministic ordering is needed, consider `sort.SliceStable` with a
  total-order comparator. Left as-is to stay within scope.
