# RESULT — focused table test for `computeFillRate`

## What changed

- **Added** `internal/aggregator/fill_rate_test.go` (test-only, new file).

No production code was touched — `git status` shows only the new test file.

The new `TestComputeFillRate` is a table test calling the pure function directly
(previously the rule was only covered indirectly via `HospitalCapacity` with a
mocked ministry client). Cases:

1. **Fully-disabled day excluded from denominator** — day 1 all `disabled`, day 2
   one of two disabled → 50%, `ScheduleDays=[2]`, `ActiveGroups=2`.
2. **Mixed enabled/disabled** — 2 disabled out of 4 active groups over days 1 and 3 → 50%.
3. **Empty `GroupColor` skipped** — colorless groups don't count toward totals, and a
   day made up only of colorless groups never enters `ScheduleDays` at all.
4. **`activeGroups == 0`** — all days fully disabled / colorless → `FillRate = 0.0`,
   empty `ScheduleDays`, no divide-by-zero.
5. **No groups at all** — same zero-valued result.
6. **`ScheduleDays` sorted and deduped** — days 5,1,5,3,1,3 → `[1,3,5]`, fill rate 1/6.

Each case also asserts `ActiveGroups`/`DisabledSlots` and that the passed-in row's
identity fields (`ID`, `Name`) survive untouched.

## TDD note

The spec is test-only for behavior that already exists, so there is no red→green
implementation step. Instead I verified the test actually constrains the rule by
temporarily deleting the `if s.disabled == s.total { continue }` guard in
`computeFillRate` and re-running: cases 1 and 4 failed (`FillRate = 75, want 50`;
`FillRate = 100, want 0`). The guard was then restored — confirmed clean via
`git status` and a passing suite.

## Test output

```
$ go test ./...
?   	github.com/angelospk/find_doctors_server/cmd/server	[no test files]
ok  	github.com/angelospk/find_doctors_server/internal/aggregator	0.006s
ok  	github.com/angelospk/find_doctors_server/internal/api	(cached)
ok  	github.com/angelospk/find_doctors_server/internal/ministry	(cached)
ok  	github.com/angelospk/find_doctors_server/internal/watch	(cached)
```

## Follow-ups (not done, out of scope)

- `HospitalCapacity` short-circuits on `len(groups) == 1 && groups[0].ResponseCode == 2`
  before ever calling `computeFillRate`; that branch stays covered only at the
  `HospitalCapacity` level.
- `computeFillRate` ignores `ResponseCode` entirely — if a non-zero response code is ever
  meant to invalidate a group, that rule currently has no home in the pure function.
