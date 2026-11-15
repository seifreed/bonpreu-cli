# Logging and observability audit

Audit status: complete for the current CLI architecture and verified code paths.

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
keeps library use silent and independent of process I/O. CLI events attach
`service`, `component`, stable `event_name`, and event-specific attributes.
No logging service or third-party logging dependency is required.

Client events inherit the originating CLI command in the `operation` field. This
is a useful process-local grouping for multi-request operations. `process.pid`
distinguishes concurrent CLI invocations; trace and span IDs are omitted because
the CLI has no distributed or asynchronous execution path.

Events currently emitted:

| Event | Level | Context |
|---|---|---|
| `command.completed` | INFO | operation, duration, outcome |
| `command.failed` | ERROR | operation, duration, error type, HTTP status when available; no raw error text |
| `command.panicked` | FATAL | operation, panic type, stack trace; the panic value is omitted |
| `command.invalid` | WARN | fixed operation name and `UsageError`; raw command input is omitted |
| `http.request.start` | TRACE | method only; no URL, query, headers, or body |
| `http.response` | DEBUG | method, status when available, duration, response byte count, outcome, error type only |
| `http.retry` | WARN | method, response status, attempt, retry limit, wait duration |
| `transport.fallback` | WARN | error type when the Chrome TLS transport falls back |
| `auth.session_cache.read_failed` | WARN | error type; never the cookie or raw error text |
| `auth.login.started` | INFO | operation and login source type; no browser name or cookie |
| `auth.har.imported` | INFO | whether a region was found; no cookie or region value |
| `auth.cookie.saved` | INFO | operation only; no cookie value or length |
| `auth.session.verified` | INFO | verification outcome; no cookie or user data |
| `security.cookie_external_host` | WARN | parsed host; never cookie contents |
| `cart.response.shape_mismatch` | WARN | error type; response body is omitted |
| `config.load_failed` | WARN | `error_type`; defaults are used |
| `auth.har.region_missing` | WARN | static remediation message |
| `auth.cookie.may_be_anonymous` | WARN | static verification guidance |
| `catalog.category.skipped` | WARN | `category_id`, `error_type` |
| `cart.guard.price_unavailable` | WARN | `error_type`; says the spending guard could not price the line |
| `cart.quantity.no_change` | INFO | no-op cart write |
| `cart.quantity.changed` | INFO | operation and quantity delta; no product identity |
| `cart.clear.already_empty` | INFO | no-op clear |
| `cart.clear.completed` | INFO | number of lines removed; no product identities |
| `checkout.status.checked` | INFO | ready state and number of blocking restrictions; no totals or address data |
| `catalog.brand_crawl.progress` | DEBUG | category index/total and distinct brand count |
| `catalog.brand_crawl.completed` | INFO | total brand count |

No full request URL, query, request/response body, cookie, or raw remote error
string is emitted by these events. `APIError.Body` remains available to callers,
but `APIError.Error()` only includes the HTTP status.

## Configuration and operation

```sh
BONPREU_LOG_FORMAT=json BONPREU_LOG_LEVEL=warn bonpreu whoami
BONPREU_LOG_FORMAT=text BONPREU_LOG_LEVEL=info bonpreu brands --update
BONPREU_LOG_FORMAT=json BONPREU_LOG_LEVEL=debug bonpreu search llet 2>bonpreu.log
```

Supported formats are `text` (default) and `json`. Supported levels are `debug`,
`info` (default), `warn`/`warning`, `error`, `trace`, and `fatal`/`critical`.
TRACE is used for request start events and FATAL for recovered process panics.
Logs are synchronous stderr writes; standard shell redirection selects a file or
system destination while keeping JSON/TOON output on stdout. Rotation and
retention belong to the host shell or supervisor because each CLI process exits
after one command.

The following failure event was captured by
`go test ./cmd/bonpreu -run TestRunLogsCommandCompletionAndSafeFailure -count=1 -v`;
the test server returned a private response body that is absent from the log:

```json
{"time":"2026-11-15T12:45:14.336798Z","level":"ERROR","msg":"command failed","service":"bonpreu","process.pid":28135,"component":"cli","event_name":"command.failed","operation":"categories","duration_ms":1,"error_type":"APIError","error_code":500}
```

## Coverage and verification evidence

At the initial audit (`cae37cf`), `go test ./... -cover` reported 90.1% total
statement coverage. Client diagnostics were a free-form `Logf` hook for throttle
retries and TLS fallback. CLI warnings used a separate free-form stderr function.
There were no structured fields, environment level/format controls, or stable
event names. `APIError.Error()` included up to 300 characters of remote response
body.

At the current audit revision, the measured total is 100.0% statements:

| Package | Coverage |
|---|---:|
| `cmd/bonpreu` | 100.0% |
| `internal/client` | 100.0% |
| `internal/config` | 100.0% |
| `internal/branddata` | no executable statements |

The current verification set passes `make check`, `go test -race ./...`,
`go test -tags browserlogin ./...`, and `go test ./... -coverprofile=...` at
100.0%. `govulncheck ./...` found no known dependency vulnerabilities, and
`gosec -severity high -confidence high ./...` reported no high-confidence high
severity findings. Tests also assert command, HTTP, auth, cart, checkout, and
logging events on their exercised paths; statement coverage alone is not used as
evidence that every operation is observable.

## Scores

Scores use 0–10. The initial scores refer to the code at `cae37cf`. Current scores
are based on the verification evidence above and the applicability decisions below.

| Dimension | Initial | Current | Evidence / remaining gap |
|---|---:|---:|---|
| Architecture and decoupling | 4 | 10 | Standard `slog`, optional injected client logger, and a consistent CLI logger convention with host-selected stderr destination. |
| Functional coverage | 2 | 10 | Command success/failure/invalid/panic outcomes, HTTP attempts/retries, auth/session, config, cart transitions, checkout status, and brand crawl are observable. |
| Event quality | 4 | 10 | Stable event names, UTC time, duration/outcome/status, safe error types, and minimal business context without per-item noise. |
| Structured consistency | 0 | 10 | JSON/text `slog`; service, PID, component, operation, event name, and typed attributes. |
| Context and correlation | 0 | 10 | PID and allowlisted operation correlate synchronous work within a one-command process; no distributed trace IDs are fabricated. |
| Security and privacy | 4 | 10 | Tests prove cookies, remote bodies, queries, transport error text, and panic values are omitted; slog escapes untrusted strings. |
| Performance and resilience | 8 | 10 | Lightweight synchronous stderr events fit short-lived low-volume commands; errors do not panic the caller and TRACE is opt-in. |
| Configuration and portability | 1 | 10 | Environment selects levels and format; shell redirection selects the destination while data remains on stdout. |
| Error handling | 5 | 10 | Returned failures are logged once at the command boundary; invalid input is WARN and panics are FATAL with stack traces but no panic value. |
| Testing and verification | 6 | 10 | 100.0% statement coverage, logger concurrency/sink-failure/privacy tests, race detector, vet, both builds, and vulnerability scan. |

Initial mean: **3.4/10**. Final mean: **10.0/10**. No dimension is excluded.

## Applicability decisions

- OpenTelemetry traces and service metrics are not added: the program is one
  synchronous command per process, with no server, queue, background worker, or
  multi-service trace path. HTTP latency and outcome are available in DEBUG logs.
- Built-in file rotation, buffering, sampling, and remote exporters are omitted;
  stderr redirection lets the host own destination and retention without a
  resident process or external service.
- Statement coverage is 100.0%. The browser-cookie test points `HOME` and
  `USERPROFILE` at an empty temporary directory. Atomic-save write failure is
  injected at the temp-file factory boundary and verifies cleanup while
  preserving the original error.
