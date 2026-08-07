# RESULT — characterization tests for `slotInTimeWindow`

## What changed

Added `internal/aggregator/slot_time_window_test.go` — a table-driven
characterization suite for the unexported `slotInTimeWindow(t, band string) bool`
helper (`internal/aggregator/search.go:449`), which gates the `TimeOfDay` filter in
`SmartSearch` slot expansion and previously had no direct test.

No production code was touched. Semantics are pinned exactly as they are today,
quirks included.

### Behavior pinned (34 subtests)

- **Empty band = pass-through** — returns `true` for a valid time, a malformed time,
  and an empty time. The band check happens before any parsing.
- **Band boundaries** (half-open intervals, hour-only):
  - `morning` = `[06:00, 09:00)` → 05:59 false, 06:00 true, 08:59 true, 09:00 false
  - `noon` = `[09:00, 12:00)` → 08:59 false, 09:00 true, 11:59 true, 12:00 false
  - `afternoon` = `[12:00, 15:00)` → 11:59 false, 12:00 true, 14:59 true, 15:00 false
- **Minutes are ignored** — only the hour is parsed, so `08:00` and `08:59:59` are
  both morning while `09:59` is not.
- **`H:MM` vs `HH:MM` vs `HH:MM:SS`** — all accepted; the seconds field is never read.
- **Malformed input → `false`** (rejected before the band switch):
  - no colon (`"0900"`, `"9"`)
  - colon at index 0 (`":30"`, `":"`) — `colon <= 0` is the guard, so a leading colon
    is treated the same as a missing one
  - non-numeric hour (`"ab:30"`), empty string, whitespace-padded hour (`" 9:30"`)
- **Unknown band → `true`** (no filtering; caller validates), but only *after* the
  time parses — `slotInTimeWindow("garbage", "evening")` is `false`, not `true`.
  Band matching is case-sensitive, so `"Morning"` falls into the unknown-band
  pass-through branch.

## Test output

```
$ go test ./...
?   	github.com/angelospk/find_doctors_server/cmd/server	[no test files]
ok  	github.com/angelospk/find_doctors_server/internal/aggregator	0.006s
ok  	github.com/angelospk/find_doctors_server/internal/api	(cached)
ok  	github.com/angelospk/find_doctors_server/internal/ministry	(cached)
ok  	github.com/angelospk/find_doctors_server/internal/watch	(cached)
```

`go test ./internal/aggregator/ -run TestSlotInTimeWindow -v` → all 34 subtests PASS.

## Follow-ups (not done — would change semantics, out of scope)

These are observations the suite now locks in, not bugs fixed here:

1. **`" 9:30"` and other whitespace-padded times are silently dropped** when a band
   is set. If the ministry API ever emits padded times, slots vanish with no signal.
2. **Malformed times are dropped silently but only when a band is set** — with no
   band they pass through into results. The two paths disagree on what a bad time
   means.
3. **Unknown bands silently disable the filter.** Nothing in `SmartSearch` validates
   `opts.TimeOfDay` against the three known values, so a typo (`"Morning"`,
   `"evening"`) returns unfiltered results rather than an error.
4. **`morning` starts at 06:00**, so a 05:30 slot matches no band at all and is
   unreachable via `TimeOfDay`; same for anything at or after 15:00.
