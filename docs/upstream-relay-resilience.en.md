[简体中文](upstream-relay-resilience.md) | [English](upstream-relay-resilience.en.md) | [繁體中文](upstream-relay-resilience.zh-TW.md)

# Upstream Relay Resilience: 402 Cooldown, Relay Circuit Breaker, Self-Heal on Success, Failover Time Budget, and All-Cooldown Fallback

This document describes the behavior, configuration, and operation of the P0 scheduling-resilience changes. It applies to API Key accounts connected to other relay stations / NewAPI / Sub2API, and also covers the 402 cooldown and failover budget shared by all platforms.

## 1. Overview

| Capability | Scope | Default | Config key |
| --- | --- | --- | --- |
| Recoverable 402 cooldown | All accounts (except OpenAI `deactivated_workspace`) | Enabled, 30 minutes | `rate_limit.payment_required_402_cooldown_minutes` |
| Upstream relay circuit breaker | API Key accounts with `extra.upstream_relay_policy` enabled | Enabled manually per account | `rate_limit.upstream_relay.*` |
| Half-open trial cap | Accounts / models whose circuit-breaker cooldown has just ended | Fixed at most 2 concurrent trials | None (built-in) |
| Self-heal on success | API Key accounts on all platforms | Enabled | None |
| Failover time budget | Failover loops of all gateway entry points | Disabled (0) | `gateway.failover_time_budget_seconds` |
| All-cooldown fallback | API Key accounts | Enabled, one probe every 10 seconds | `gateway.cooldown_fallback.*` |

## 2. Recoverable 402 Cooldown

Previously, an upstream HTTP 402 permanently marked the account as `error`, requiring manual recovery. Now:

- A 402 (OpenAI `deactivated_workspace` is still a permanent error) makes the account **temporarily unschedulable** for `payment_required_402_cooldown_minutes` (default 30 minutes). The reason field is structured JSON (status code 402 + the truncated upstream message).
- Accounts in account-pool mode are also put in cooldown on a 402 and no longer go through `pool_mode_error_skipped`; user-defined `temp_unschedulable_rules` take precedence.
- As soon as the upstream balance probe sees a positive balance, the 402 cooldown is lifted early.
- Account operations notifications classify a bare 402 as `balance_low`.
- 402 cooldowns are **never** selected by the all-cooldown fallback (a probe cannot fix an exhausted balance).

## 3. Upstream Relay Circuit Breaker (`upstream_relay_policy`)

### 3.1 How to Enable

In the admin console under "Account Management → Edit Account", API Key accounts have a new "Upstream relay circuit breaker policy" toggle, which maps to `accounts.extra.upstream_relay_policy=true`. It only takes effect for API Key accounts; turning it off writes `false`.

### 3.2 Rules

- Counted as failures: 429, all 5xx, timeouts / transient transport-layer errors (timeouts are counted at the account level).
- Not counted: 4xx client / parameter errors; 402, 401, and 403 keep their existing handling; when `temp_unschedulable_rules` or a custom error code matches, the rule still wins.
- Failures and successes are written to a Redis sliding window (shared across instances; falls back to in-process storage when the Redis extension is unavailable).
- The breaker trips when the number of failures in the window is ≥ `min_failures` **and** the failure rate is ≥ `failure_rate_percent`.
- Cooldown duration increases with the backoff level: 5 seconds → 15 seconds → 60 seconds → 5 minutes → doubling thereafter, capped at `max_cooldown_seconds`. Reset-time hints returned by the upstream are not used.
- Concurrent trips within 2 seconds raise the level only once, so a single burst cannot push the level up several steps.
- The level expires if no new trip occurs within `level_reset_seconds`; the next trip starts again from level one.
- Granularity: `scope: model` (default) cools down only the failing model (written to `extra.model_rate_limits`); `scope: account` cools down the whole account (written as temporarily unschedulable).

### 3.3 Half-Open State and Trial Cap

When the cooldown expires, the account enters the **half-open** state: the expired circuit-breaker reason stays on the account snapshot until a success clears it or a failure trips the breaker again.

- While half-open, the same account (or the same model scope) **admits at most 2 concurrent trial requests**. The counter is stored in Redis (key prefix `upstream_relay_half_open:`, same hash tag as the breaker window); without Redis, an in-process counter is used.
- When scheduling picks a half-open account that is already at its cap, it releases the concurrency slot it acquired, excludes that account, and selects again (at most 3 reselections per request).
- Trial outcomes:
  - Success → the breaker closes, the backoff level resets to zero, cooldown residue is cleared, and the counter is reset;
  - Failure (429/5xx/timeout) → the breaker immediately trips again at the next level, and the counter is reset;
  - Outcomes that are neither a success nor counted by the breaker (e.g. 400) → the counter expires automatically after `max(window_seconds, 30s)`.
- OpenAI `previous_response_id` continuation requests must hit the original account and do not take part in half-open reselection.
- On storage errors the request is let through (fail open), so Redis jitter never locks an account out.

### 3.4 Redis Keys

| Key | Description |
| --- | --- |
| `upstream_relay:{<account ID>\|<scope>}:fail` / `:all` / `:seq` | Sliding window (failures / all / sequence) |
| `upstream_relay:{<account ID>\|<scope>}:state` | Backoff level and last trip time |
| `upstream_relay_half_open:{<account ID>\|<scope>}` | Half-open trial count |
| `cooldown_fallback_probe:<key>` | All-cooldown fallback probe lock |

Here `<scope>` is `account` or `model:<model>`.

## 4. Self-Heal on Success

When an API Key account (any platform) receives a successful response and the usage is recorded:

- Residual rate-limit / overload / temporarily-unschedulable state is cleared, along with the entry for the served model in `model_rate_limits`;
- For accounts with the circuit breaker enabled, a tripped breaker is closed and the backoff level is reset;
- The database is written only when the account snapshot actually carries residue, so the healthy path has zero overhead;
- Pauses triggered by the admin console's "auto-pause threshold" are not cleared.

## 5. Failover Time Budget

`gateway.failover_time_budget_seconds` (default 0 = unlimited) caps the total time a single request may spend on failover. Once the budget is exceeded, the gateway stops switching accounts and stops retrying on the same account, and returns the last upstream error through the normal "retries exhausted" path (preserving the original status code). This covers the Anthropic / Gemini / Chat Completions / Responses / SystemOne loops as well as the OpenAI Responses / Chat / WebSocket loops.

Recommendation: when clients use short timeouts (e.g. 60 seconds), set the budget slightly below the client timeout so the gateway is not still switching accounts after the client has disconnected.

## 6. All-Cooldown Fallback

When account selection fails **solely** because every candidate API Key account is in cooldown (rate limit, overload, temporarily unschedulable, circuit breaker, model cooldown), rather than disabled, errored, expired, quota-exhausted, in a 402 balance cooldown, or paused by the auto-pause threshold:

- Instead of returning 503 outright, the gateway picks the account **whose cooldown ends earliest** and lets one probe request through (on a tie, the most recently used account is chosen);
- Only one probe is admitted per group / platform / model within `probe_interval_seconds` (default 10 seconds), enforced by a cross-instance Redis lock (`SETNX`);
- A successful probe clears the cooldown via self-heal on success; a failed probe goes through the normal error path;
- Deliberate rejections such as channel pricing restrictions or routing errors do not trigger the fallback;
- The OpenAI-compatible gateway applies the fallback only to plain HTTP requests (no compact, no capability requirements, no `previous_response_id` constraint, and a transport of Any or HTTP/SSE).

## 7. Admin Console Display and One-Click Recovery

The status column of the account list now shows a "Cooldown N" badge; hover over it to see each active cooldown:

- Type (temporarily unschedulable / rate limit / overload / model cooldown) and scope (whole account / specific model);
- Triggering status code and the truncated upstream message (up to 160 characters);
- Remaining time;
- Circuit-breaker level (`breaker_level` from the reason JSON).

The "One-click recovery" button in the tooltip calls `POST /api/v1/admin/accounts/:id/recover-state`, clears the recoverable state, and refreshes that row.

Backend API: account detail and list responses gain a `cooldowns` array (returned only when active cooldowns exist) with the fields `kind`, `scope`, `model`, `status_code`, `message`, `keyword`, `breaker_level`, `until`, `remaining_seconds`.

## 8. Configuration Example

```yaml
gateway:
  failover_time_budget_seconds: 0
  cooldown_fallback:
    enabled: true
    probe_interval_seconds: 10

rate_limit:
  payment_required_402_cooldown_minutes: 30
  upstream_relay:
    window_seconds: 60
    min_failures: 5
    failure_rate_percent: 50
    max_cooldown_seconds: 600
    level_reset_seconds: 1800
    scope: model
```

## 9. Troubleshooting

- Log keywords: `upstream_relay_breaker_tripped`, `upstream_relay_breaker_closed`, `upstream_relay_half_open_trial_capped` (debug), `cooldown_fallback_probe_selected`, `upstream_success_cleared_residual_cooldown`.
- Account stuck in half-open: check whether trial requests returned a status that is neither a success nor counted by the breaker (e.g. 400); the counter expires after the window duration.
- Still getting 503 with all accounts in cooldown: check for 402 cooldowns, exhausted quotas, or auto-pause threshold pauses, none of which take part in the fallback; or a probe may have just been admitted for the same group / model within the probe interval.
