[简体中文](upstream-quality-probes.md) | [English](upstream-quality-probes.en.md) | [繁體中文](upstream-quality-probes.zh-TW.md)

# Upstream Quality Probes

An upstream quality probe targets a single API Key account and sends a very small streaming request directly to its upstream, recording quality metrics that existing tools cannot see: time to first token (TTFT), output TPS, the input / output / cache tokens reported by the upstream, the actual model name returned by the upstream, and anomaly hints.

## 1. Relationship to existing tools

| Tool | Granularity | What it records |
| --- | --- | --- |
| Channel Monitor V2 | Platform / group / model (aggregated real traffic, not per account) | Availability, latency, TTFT, cache hit rate |
| Scheduled tests / Pelican tests | Single account (via the account test path) | Total duration and response text |
| **Upstream quality probe** | Single account (direct to upstream) | TTFT, output TPS, upstream token usage, upstream model name, anomaly hints |

Quality probes complement the tools above; they do not replace them.

## 2. Cost and defaults

- Every probe **consumes upstream quota** (output capped at 64 tokens), so:
  - The global switch is off by default (`settings` table key `upstream_quality_probe_settings`);
  - Each account must have its own probe configuration added (explicit opt-in).
- A manual "Run now" is not restricted by the global switch.

## 3. Eligible accounts

- Enabled **API Key** accounts;
- The Anthropic platform uses the Messages protocol (`/v1/messages`); other OpenAI-compatible platforms use Chat Completions (`/v1/chat/completions`);
- Gemini and Antigravity are not supported.
- Requests are sent through the same HTTP upstream channel used for account tests (honoring the account's proxy and TLS settings), with redirects disabled. When no `base_url` is configured, OpenAI / Anthropic use their official endpoints; `base_url` must pass the same address validation as account tests.
- The model name is first converted to the upstream model using the account's model mapping.

## 4. Entry point and configuration

Admin sidebar → "Upstream Quality Probes" (`/admin/upstream-quality-probes`).

### 4.1 Global settings

| Field | Default | Description |
| --- | --- | --- |
| `enabled` | false | Master switch for scheduled probes |
| `max_results_per_account` | 100 | Number of results retained per account (10–1000) |
| `input_token_tolerance_pct` | 200 | Tolerance percentage between input tokens and the local estimate (50–10000) |

### 4.2 Account configuration

| Field | Description |
| --- | --- |
| `enabled` | Whether the account participates in scheduled probes |
| `model` | Probe model (a single model name, up to 128 characters, no whitespace) |
| `interval_minutes` | Probe interval, 5–1440 minutes, default 60 |

After the configuration is saved, the probe runs in the very next scheduling cycle instead of waiting for the old schedule.

## 5. Scheduling

- Hooks into the same tick as the scheduled test runner and runs in parallel with Channel Monitor Candy and Pelican group tests.
- Each tick claims at most 10 due accounts, processed by 4 concurrent workers; claiming uses `FOR UPDATE SKIP LOCKED` with a 5-minute lease, so multiple instances never probe the same account twice.
- Each probe times out after 90 seconds; response body reads are capped at 1 MiB.

## 6. Probe request and anomaly detection

The request asks the model to "reply with only the word pong and do not call any tools", and also attaches a decoy tool with a random name (`qp_canary_xxxxxxxx`). Anomaly hints (`anomalies`):

| Value | Meaning |
| --- | --- |
| `unexpected_tool_call` | The model called the decoy tool (the upstream or an intermediate layer invoked a tool on its own) |
| `echoed_tool_config` | The decoy tool's name or description appears in the response (request configuration echoed / leaked) |
| `missing_usage` | The upstream did not return input or output token usage |
| `input_tokens_mismatch` | Input tokens reported by the upstream fall outside the tolerance range of the local estimate (too large, or smaller than half the estimate further divided by the tolerance factor) |
| `upstream_model_mismatch` | The model name returned by the upstream differs from the requested model (possible model substitution) |
| `empty_output` | No text output at all and no tool call |
| `unexpected_answer` | Output is present but does not contain `pong` |

Notes:

- The local input token estimate is deliberately rough (about 4 characters per token plus a fixed overhead for message and tool framing), so the default tolerance is wide (200%).
- In Chat Completions, cache tokens are already included in `prompt_tokens`; for the Messages protocol, the comparison uses input + cache read + cache write combined.
- Anomalies are hints, not verdicts; judge them across multiple results.

## 7. Metric definitions

- **TTFT**: milliseconds from sending the request to the first output delta.
- **Output TPS**: output tokens ÷ (end time − first-token time); computed only when there is a first token and the upstream reported output tokens, rounded to 2 decimal places.
- **Total duration**: from request start to end of stream.
- On failure, `error_message` is a short code such as `timeout`, `request_failed`, `http_<status code>`, `stream_error: ...`, `missing_api_key`, or `invalid_base_url`.
- The response excerpt is at most 300 characters, and the account's own API Key is scrubbed from the text.

## 8. Data tables (migration `311_upstream_quality_probes.sql`)

- `upstream_quality_probe_configs`: one row per account (primary key `account_id`), containing enabled flag, model, interval, next / last run time, and run lease.
- `upstream_quality_probe_results`: one row per probe, containing trigger type (`scheduled` / `manual`), protocol, model / upstream model, status, HTTP status, TTFT / total duration / TPS, token usage, estimated input tokens, anomaly array, and response excerpt.

Rows are cascade-deleted when the account is deleted; results are pruned according to `max_results_per_account`.

## 9. Admin API

All endpoints live under `/api/v1/admin/upstream-quality-probes` and require admin privileges:

| Method | Path | Description |
| --- | --- | --- |
| GET / PUT | `/settings` | Read / update global settings |
| GET | `/accounts` | List of configured accounts (with the most recent result) |
| GET | `/accounts/:id` | Single account configuration and result history; parameter `limit` (default 100) |
| PUT | `/accounts/:id` | Save account configuration |
| DELETE | `/accounts/:id` | Delete account configuration |
| POST | `/accounts/:id/run` | Run once immediately (optional request body `{"model": "..."}`) |

## 10. Recommendations

- Enable probes only for relay upstreams whose quality you need to watch; an interval of 30–60 minutes is enough to observe trends.
- If an upstream keeps showing `upstream_model_mismatch` or `input_tokens_mismatch`, use it together with "Upstream Relay Circuit Breaker" and "Upstream Change Alerts" to investigate whether the model has been swapped or the rate multiplier adjusted.

## 11. Custom prompt rotation, model picker and bulk enable

- **Prompt rotation**: an account config may set `prompts` (up to 20, each ≤ 8000 characters, blank lines ignored). Each probe (scheduled and "Run now") then uses the next prompt round-robin; the cursor `prompt_rotation` is persisted (migration `322_upstream_quality_probe_prompts.sql`), so restarts and multiple replicas keep the order. With custom prompts the reply is no longer checked for `pong` (no `unexpected_answer`) and the output cap rises to 512 tokens; the canary tool stays, so `unexpected_tool_call` / `echoed_tool_config` checks still apply. Empty `prompts` keeps the original ping/pong probe. Saving a config without the `prompts` field keeps the stored prompts (bulk toggles never wipe them).
- **Fetch models from upstream**: the "Fetch from upstream" button next to the model field queries the account's live upstream `/v1/models`; the list is searchable and a click fills the field. The same picker is used by Pelican tests, channel monitor, group model allowlist, account test and scheduled tests. Group-scoped pages call `POST /api/v1/admin/accounts/models/sync-upstream-by-group` (`{"group_id": N}`), which merges the models of the group's active accounts (one request per base URL, at most 8 upstreams); failing accounts only produce warnings and never expose keys.
- **Bulk enable**: select accounts in Account Management and use "Upstream Quality Probe" in the bulk bar to enable / disable probes at once, optionally with one model and interval (5–1440 minutes; an empty model keeps each account's configured model). When enabling, only the first account per upstream base URL is enabled; the rest are marked "Skipped (duplicate upstream)" with the first account named. Disabling applies to every selected account. Each row shows its result (enabled / disabled / skipped / failed). API: `POST /api/v1/admin/upstream-quality-probes/accounts/batch` with `{"account_ids": [...], "enabled": true, "model": "...", "interval_minutes": 60}`, 1–500 accounts per call.
