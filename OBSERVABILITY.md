# Logging and observability audit

Audit status: in progress. This is a command-line Go application, not a server.

## Runtime and operation model

`cmd/bonpreu` dispatches one command per process. The client performs synchronous
HTTP calls to Bonpreu; the `brands --update` command walks categories
sequentially. Session and configuration state live in local files. There is no
database, queue, scheduler, long-running worker, or service-to-service trace
path. User-facing data goes to stdout and diagnostics go to stderr so
`--json`/`--toon` remain usable in pipelines.

## Architecture

The CLI uses the Go standard library `log/slog`. `newLogger` selects text or JSON
with `BONPREU_LOG_FORMAT`, applies `BONPREU_LOG_LEVEL`, and converts timestamps to
UTC. It writes to stderr. The API client accepts an optional `*slog.Logger`; nil
keeps library use silent and independent of process I/O. CLI INFO/WARN helpers
attach `service`, `component`, stable `event_name`, and event-specific attributes
where useful. No logging service or third-party logging dependency is required.

Client events currently emitted:

| Event | Level | Context |
|---|---|---|
| `http.retry` | WARN | method, response status, attempt, retry limit, wait duration |
| `transport.fallback` | WARN | error type when the Chrome TLS transport falls back |
| `auth.session_cache.read_failed` | WARN | error type; never the cookie or raw error text |
| `security.cookie_external_host` | WARN | parsed host; never cookie contents |
| `cart.response.shape_mismatch` | WARN | error type; response body is omitted |
| `config.load_failed` | WARN | `error_type`; defaults are used |
| `auth.har.region_missing` | WARN | static remediation message |
| `auth.cookie.may_be_anonymous` | WARN | static verification guidance |
| `catalog.category.skipped` | WARN | `category_id`, `error_type` |
| `cart.guard.price_unavailable` | WARN | `error_type`; says the spending guard could not price the line |
| `catalog.brand_crawl.progress` | INFO | category index/total and distinct brand count |
| `catalog.brand_crawl.completed` | INFO | total brand count |

No full request URL, query, request/response body, cookie, or raw remote error
string is emitted by these events. `APIError.Body` remains available to callers,
but `APIError.Error()` only includes the HTTP status.

## Configuration and operation

```sh
BONPREU_LOG_FORMAT=json BONPREU_LOG_LEVEL=warn bonpreu whoami
BONPREU_LOG_FORMAT=text BONPREU_LOG_LEVEL=info bonpreu brands --update
```

Supported formats are `text` (default) and `json`. Supported levels are `debug`,
`info` (default), `warn`/`warning`, and `error`. Logs are synchronous stderr
writes; for this short-lived CLI, asynchronous buffering, rotation, retention,
sampling, and external exporters would add infrastructure without a resident
process to manage them.

## Coverage and verification evidence

At the initial audit (`cae37cf`), `go test ./... -cover` reported 90.1% total
statement coverage. Client diagnostics were a free-form `Logf` hook for throttle
retries and TLS fallback. CLI warnings used a separate free-form stderr function.
There were no structured fields, environment level/format controls, or stable
event names. `APIError.Error()` included up to 300 characters of remote response
body.

At the current audit revision, the measured total is 98.9% statements:

| Package | Coverage |
|---|---:|
| `cmd/bonpreu` | 98.3% |
| `internal/client` | 99.8% |
| `internal/config` | 97.4% |
| `internal/branddata` | no executable statements |

The current verification set passes `make check`, `go test -race ./...`, and
`go test -tags browserlogin ./...`. Coverage is statement coverage, not proof
that every operation emits an event.

## Scores

Scores use 0–10. The initial scores refer to the code at `cae37cf`; current scores
are intermediate, not a completion claim.

| Dimension | Initial | Current | Evidence / remaining gap |
|---|---:|---:|---|
| Architecture and decoupling | 4 | 8 | Standard `slog` and optional client logger; CLI still has separate formatted helpers. |
| Functional coverage | 2 | 5 | Retries, fallbacks, auth/cache/security warnings, cart mismatch, and brand crawl are observable; ordinary successful API operations are not consistently logged. |
| Event quality | 4 | 8 | Stable event names and useful retry/progress fields; some static warnings have no event-specific attributes. |
| Structured consistency | 0 | 9 | JSON/text `slog`, UTC time, service/component/event fields, and typed attributes for contextual warnings. |
| Context and correlation | 0 | 3 | No request/correlation ID propagation. The CLI is single-process, but one brand crawl can span multiple HTTP calls and lacks a shared operation ID. |
| Security and privacy | 4 | 8 | No cookies/bodies/raw remote error strings in emitted client events; error output paths still need a whole-command privacy review. |
| Performance and resilience | 8 | 8 | Synchronous standard-library writes; no network exporter or logger failure path. Volume is low and command lifetime is short. |
| Configuration and portability | 1 | 8 | Standard environment configuration, stderr destination, no service dependency; no file rotation is needed for this process model. |
| Error handling | 5 | 8 | Status-only API error string and typed event errors; some operation failures are only returned to the CLI caller, not logged with operation context. |
| Testing and verification | 6 | 9 | Logger format/level/escaping/UTC, event paths, privacy, race detector, both builds; 98.0% statement coverage is below the requested 100%. |

Initial mean: **3.4/10**. Current mean: **7.4/10**. No dimension is excluded.

## Remaining work

- Add operation context across multi-request CLI flows where it materially helps diagnosis; do not invent distributed trace/span IDs in this single-process tool.
- Replace remaining formatted warning context with typed, minimal structured attributes where useful.
- Close the remaining statement coverage gaps where deterministic tests are safe. Some gaps are operating-system/browser integrations; tests must not read real user cookie stores.
- Re-run security and full validation checks, then recalculate scores from current evidence. The 10/10 target has not been met.
