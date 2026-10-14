# Usage contract v3

`GET /v3/usage` requires `Authorization: Bearer <dedicated meter token>`. A response is UTF-8 JSON, at most 4096 bytes, with no unknown or duplicate fields. Only `resets_available` may be omitted for compatibility with earlier v3 snapshots. The Go API and device decoder validate it independently. See [normal.json](fixtures/normal.json) for a complete synthetic example.

| Field | Meaning |
| --- | --- |
| `version` | Integer 3 |
| `updated_at` | Unix seconds of the collector publication |
| `as_of` | API response time in Unix seconds; used to advance countdowns on-device |
| `stale_after_seconds` | 30–3600; normally 900 |
| `claude`, `codex` | Independent provider objects |

Each provider always contains `scope`, `observed_at`, `source_error`, `five_hour`, and `weekly`.

- `observed_at` is the last successful source observation, or zero before any success. It cannot exceed `updated_at`.
- `scope` is a 64-character lowercase installation-keyed HMAC, or empty before success. It separates account observations without exposing credentials or account IDs. Claude token changes also change scope.
- `source_error` is empty on success, otherwise one of `auth_missing`, `auth_failed`, `source_timeout`, `source_unavailable`, `source_invalid`, or `rate_limited`.
- Each window is null (absent/unsupported), or `{ "remaining_percent": 0..100, "reset_at": UNIX_SECONDS }`. Reset zero means the window has not started. All other epochs are 1–4102444800 inclusive.
- `resets_available` is null/omitted when unsupported or unknown, otherwise an integer from 0 to 2147483647. It must be null/omitted before any successful observation. Positive, fresh counts render as a reload icon and number at the right of the provider title; zero, unknown, and stale counts are hidden.
- Before any success, scope is empty and both windows are null. Successful responses may also have null windows when the source reports no supported limits.

The collector converts source utilization into percent remaining. Codex windows are selected by their 18000/604800-second durations, regardless of primary/secondary position; additional model-specific buckets are ignored. Claude uses `five_hour` and overall `seven_day`. Percent used above 100 becomes zero remaining; malformed values fail the observation.

Codex reset counts come only from `rate_limit_reset_credits.available_count`; malformed optional counts become unknown without discarding valid quota readings. Never infer counts from credit balances or detail-list length. The Claude usage endpoint currently supplies no supported reset count, so the collector leaves it null.

Source errors retain the previous observation unless a different account scope is known. The device marks failed, old, or clock-inconsistent observations stale, and keeps cached readings if the API loses its cache. A repeated response cannot reset the observation's age; older publications cannot roll the display back. A missing API snapshot returns both providers unavailable, while an invalid snapshot returns HTTP 503. Health checks never imply current provider data.

Other retained routes: unauthenticated `GET /healthz`, authenticated `POST /v2/battery`, and `GET /v2/battery/history`. Battery telemetry remains version 2 and is independent of usage freshness. There is no route for native credentials or remote device settings.
