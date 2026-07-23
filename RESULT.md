# RESULT

## Task
Make the ministry client retry on HTTP 429 (Too Many Requests) and honor the
`Retry-After` header, so upstream rate-limiting backs off instead of aborting
the whole scan on the first throttle.

## Changes

### `internal/ministry/client.go`
- Added `http.StatusTooManyRequests` (429) to `retryableStatus`, so 429 is now
  treated as a transient failure worth retrying instead of falling through to
  the `unexpected status code` error.
- Added `parseRetryAfter(v string, now time.Time) (time.Duration, bool)`, which
  parses a `Retry-After` header in either form: delta-seconds (integer) or
  HTTP-date (`http.ParseTime`). Negative/past or unparseable values return
  `ok=false` so the caller falls back to the existing jittered backoff.
- Added `maxRetryAfter = 30s` constant to cap an upstream-supplied delay so a
  misbehaving server can't stall a fan-out scan for minutes.
- In `doJSON`'s retry loop: when a 429 response carries a usable `Retry-After`,
  that delay (capped at `maxRetryAfter`) is used for the next attempt's wait;
  otherwise the existing jittered exponential backoff is used unchanged. Other
  retryable statuses (500/502/503/504) and network errors keep the jittered
  backoff.
- New imports: `strconv`, `strings`.

### `internal/ministry/client_test.go`
- Added `TestClient_Retry429`, a table test using an `httptest.Server` that
  returns 429 on the first call then 200. Cases:
  - **without Retry-After** — retries via fast jittered backoff, succeeds
    (asserts elapsed <= 500ms with `RetryWait` set to 10ms).
  - **with Retry-After delta-seconds** (`Retry-After: 1`) — asserts the retry
    waited >= 1s.
  - **with Retry-After HTTP-date** (2s in the future) — asserts the retry
    waited >= 900ms.
  Each case asserts exactly 2 upstream calls (1 retry) and a successful decode.

## Test output
`go test ./...`:
```
?   github.com/angelospk/find_doctors_server/cmd/server	[no test files]
ok  github.com/angelospk/find_doctors_server/internal/aggregator	0.014s
ok  github.com/angelospk/find_doctors_server/internal/api	0.024s
ok  github.com/angelospk/find_doctors_server/internal/ministry	2.869s
ok  github.com/angelospk/find_doctors_server/internal/watch	0.020s
```
`go vet ./internal/ministry/` clean; `gofmt -l` reports no changes.

## Follow-ups
- None required. Possible future nicety: honor `Retry-After` on non-429
  retryable statuses too (some servers send it on 503), but the spec scoped
  this to 429 so it was left as-is.
