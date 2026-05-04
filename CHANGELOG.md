# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### Added
- `caido_replay_request` now accepts `host`, `port`, and `tls` override fields to change the TCP connection target independently of the on-wire `Host` header (FR#2).

### Fixed
- `caido_replay_request`: transport errors from the Caido replay engine (e.g. connection reset, TLS failure) are now surfaced in the `error` field instead of timing out silently (FR#6 / batch parity).
- `caido_send_request`: same transport-error surfacing via the `error` field.
- `caido_batch_send` (DETAILED mode): transport errors from the replay engine are now captured in each result's `error` field; `statusCode` and `response` correctly remain absent rather than showing misleading defaults (FR#6).
- `caido_replay_request`: session-busy detection now uses the same typed check as `caido_send_request` instead of a string match (reliability fix).
- `internal/replay`: poll loop exits promptly on engine-reported errors, preventing unnecessary 15 s timeout on blocked connections.
- `caido_setup_program`: tamper rules are now automatically enabled after creation and update. The Caido API creates rules in a disabled state by default; an explicit `toggleTamperRule` call is now made immediately after each create/update (FR#5).
- `caido_create_tamper_rule`: same fix — rules are enabled on creation.

### Removed
- `caido_send_request`: removed `followRedirects` and `sslVerify` parameters — the Caido replay engine does not support these options and they were no-ops. Redirects return 3xx directly; TLS verification is enforced by the engine.

## [1.5.0] - 2026-04-09

### Added
- **`caido_batch_send` MCP tool** - send multiple HTTP requests in parallel via session pool. Supports BAC token sweeps, parameter fuzzing, endpoint sweeps. Max 50 requests per batch, configurable concurrency (default 5, max 20). One tool call replaces N sequential `caido_send_request` calls.
- **`caido batch` CLI subcommand** - parallel HTTP through Caido Replay API. Four modes: `sweep` (same endpoint, N tokens), `fuzz` (same endpoint, N values), `ep` (N URLs, same auth), `file` (JSON batch spec). Drop-in replacement for `burp-batch` with identical interface.
- **Session pool** (`internal/replay/pool.go`) - manages N replay sessions for concurrent sends. Pre-creates sessions in parallel, acquire/release pattern with channel-based semaphore.
- **Batch engine** (`internal/replay/batch.go`) - shared by MCP tool and CLI. Handles session acquisition, CRLF normalization, host resolution, parallel polling.
- Auth mode support in CLI batch: `bearer` (default), `cookie:NAME`, `header:NAME` - matches `burp-batch` interface.

### Changed
- Upgraded sdk-go to v0.4.0 (`use_struct_references: false`) -- fixes 53 NON_NULL fields across generated types, removes manual GraphQL workarounds for `delete_findings`, `export_findings`, and `create_tamper_rule`.

### Fixed
- `caido_create_tamper_rule` -- nested oneof serialization for section, operation, matcher, and replacer fields now correct at all 4 levels of nesting.
- `caido_create_tamper_rule` -- `sources` field sent as `[]` (empty array) instead of omitted -- Caido API requires non-null.
- `caido_get_sitemap` -- null entries in sitemap response no longer cause a panic.
- `caido_run_workflow` -- Blob-encoded workflow output decoded correctly.
- `caido_list_environments` -- description field populated correctly.

## [1.4.0] - 2026-04-09

### Added
- **PAT authentication** - set `CAIDO_PAT` env var to skip OAuth device flow entirely (recommended for automation)
- **14 new MCP tools** (20 -> 34 total):
  - `caido_run_workflow` - execute active or convert workflows
  - `caido_toggle_workflow` - enable/disable automation workflows
  - `caido_list_tamper_rules` - list Match & Replace rule collections
  - `caido_create_tamper_rule` - create tamper rules with HTTPQL conditions
  - `caido_toggle_tamper_rule` - enable/disable tamper rules
  - `caido_delete_tamper_rule` - delete tamper rules
  - `caido_intercept_status` - get intercept status (PAUSED/RUNNING)
  - `caido_intercept_control` - pause or resume intercept
  - `caido_list_intercept_entries` - list queued intercept entries with HTTPQL filtering
  - `caido_forward_intercept` - forward intercepted request with optional modifications
  - `caido_drop_intercept` - drop intercepted request
  - `caido_list_environments` - list environments and variables
  - `caido_select_environment` - switch active environment
  - `caido_list_filters` - list saved HTTPQL filter presets
- Sensitive header redaction (Authorization, Cookie, Set-Cookie, API keys) in all tool output
- Input length validation on all string parameters
- Request ID batch cap (max 20 per call)
- Test coverage for header redaction

### Changed
- Bumped sdk-go to v0.3.0 (tamper rules SDK, workflow execution, WebSocket fix)
- Removed WebSocket endpoint workaround (fixed upstream in sdk-go)
- README rewritten with PAT auth as recommended setup, security section added

## [1.1.0] - 2026-03-06

### Added
- `send_request` returns response inline (status code, headers, body) - no extra tool calls needed
- Response body polling with 10s timeout and fallback to `get_replay_entry`
- `get_replay_entry` now supports `bodyLimit` and `bodyOffset` parameters
- Token auto-refresh mid-session via callback (no more expired token failures)
- Replay session reuse - single session per server lifetime with automatic fallback
- IPv6 host support (`[::1]:8080`)

### Changed
- `send_request` output now includes `requestId`, `entryId`, `statusCode`, `roundtripMs`, parsed `request`/`response`
- `get_replay_entry` defaults to 2KB body limit (matching `get_request`)
- `ParsedHTTPMessage` and `parseHTTPMessage` extracted to shared `http_utils.go`

### Removed
- Unused `urlEncode` function from send_request
- Unused `RequestSummary` struct from types
- `TaskID` field from send_request output (not useful to LLM callers)

## [1.0.0] - 2026-01-30

### Added
- Initial release
- OAuth authentication with automatic token refresh
- 14 MCP tools for Caido integration:
  - `caido_list_requests` - List proxied requests with HTTPQL filtering
  - `caido_get_request` - Get request details with field selection
  - `caido_send_request` - Send HTTP requests via Replay
  - `caido_list_replay_sessions` - List Replay sessions
  - `caido_get_replay_entry` - Get Replay entry details
  - `caido_list_automate_sessions` - List Automate fuzzing sessions
  - `caido_get_automate_session` - Get Automate session details
  - `caido_get_automate_entry` - Get fuzzing results
  - `caido_list_findings` - List security findings
  - `caido_create_finding` - Create new findings
  - `caido_get_sitemap` - Browse sitemap hierarchy
  - `caido_list_scopes` - List target scopes
  - `caido_create_scope` - Create new scopes
- Pre-built binaries for macOS, Linux, Windows (amd64/arm64)
