[简体中文](usage-analytics.md) | [English](usage-analytics.en.md) | [繁體中文](usage-analytics.zh-TW.md)

# Usage Records and Reports: Upstream Cost Toggle, TPS / Cache Hit Rate, Aggregation, and Gross Profit

This document describes four new capabilities on the admin "Usage Records" page (`/admin/usage`) and how each metric is defined:

1. Upstream cost visibility toggle (prevents leaks in screenshots)
2. Dedicated TPS and cache hit rate columns, with server-side sorting
3. Aggregated analytics by upstream account / group / model
4. Gross profit report (revenue − upstream cost)

Every derived metric has a single canonical definition in the backend at `backend/internal/pkg/usagestats/usage_metrics.go`.
The frontend (`frontend/src/utils/usageTps.ts`, `frontend/src/utils/usageCacheRate.ts`) and the
repository SQL expressions (`backend/internal/repository/usage_log_metrics_sql.go`) must stay consistent with it.
When changing any threshold, update all three places and this document.

---

## 1. Upstream Cost Visibility Toggle

- Location: the eye icon at the top right of the Usage Records page toolbar (to the right of the time granularity selector).
- The state is stored in the browser's `localStorage` under the key `admin-usage-upstream-cost-visible` (`'1'` shown / `'0'` hidden); shown by default.
  It only affects the current browser, not other admins; backend APIs are unaffected (they still return the cost fields).
- Turning it off **hides all of the following upstream cost information at once**:
  - The `A $…` line under the "Cost" column in the detail table;
  - The "Account rate multiplier" and "Account billing" sections in the cost tooltip;
  - "Account cost" in the stat cards;
  - The "Account rate multiplier" and "Account billing" columns in the Excel export (decided by the toggle state when the export starts);
  - The "Upstream cost", "Gross profit", and "Gross margin" columns in the "Analytics" tab (if the table is currently sorted by one of these columns, it automatically falls back to revenue descending).
- In observer mode (a user viewing their own usage) the toggle is not shown, and display behavior is unchanged.
- Implementation: `frontend/src/composables/useUpstreamCostVisibility.ts` (module-level shared state).

> Note: the toggle only hides data in the presentation layer, for screenshot scenarios; it is not access control. The cost data returned by the API is still visible in the browser's developer tools.

## 2. TPS and Cache Hit Rate

### 2.1 Cache Hit Rate

```
cache_hit_rate = cache_read_tokens / (input_tokens + cache_read_tokens + cache_creation_tokens)
```

The three kinds of prompt tokens in `usage_logs` are **mutually exclusive buckets**:

| Field | Meaning |
| --- | --- |
| `input_tokens` | Regular input that missed the cache |
| `cache_read_tokens` | Cache hits (reads) |
| `cache_creation_tokens` | Cache writes |

Each protocol is normalized **at write time**, so the same formula is correct for all protocols:

- **Anthropic**: `input_tokens` itself excludes cache tokens and is stored as-is.
- **OpenAI** (Chat Completions / Responses): `prompt_tokens` / `input_tokens` **include** `cached_tokens`.
  When recording usage, the gateway subtracts cache reads and writes: `input_tokens = total input − cache_read − cache_creation`
  (see `backend/internal/service/openai_gateway_usage.go`, `actualInputTokens`).
  Example: `prompt_tokens=10000, cached_tokens=8000` → stored as `input=2000, cache_read=8000` → hit rate 80%.
- **Gemini / Antigravity**: `promptTokenCount` includes `cachedContentTokenCount`, which is subtracted during conversion
  (`extractGeminiUsage` in `gemini_messages_compat_service.go`, and `pkg/antigravity/*_transformer.go`).

When the denominator is 0 (no prompt tokens, e.g. pure image requests), the hit rate does not apply and `-` is shown.
Aggregate views (trend charts, Analytics tab) use `SUM(read) / SUM(input + read + write)`, i.e. token-weighted, rather than the average of per-row hit rates.

When the public setting `usage_show_cache_rate` (system settings) is off, the admin side does not offer the "Cache rate" column.

### 2.2 Output TPS (Generation-Phase Throughput)

```
TPS = output_tokens / ((duration_ms − first_token_ms) / 1000)
```

- When `first_token_ms` is missing (non-streaming requests), it is treated as 0, i.e. the total duration is used as the generation window.
- In the following cases TPS is not computed, `-` is shown, and the row is excluded from P50/P95:
  - Output tokens < **20** (`OutputTPSMinOutputTokens`);
  - `duration_ms` is missing, or the generation window is < **100ms** (`OutputTPSMinGenerationMs`, which naturally covers ≤ 0);
  - Image / video generation requests (`image_count > 0`, `image_output_tokens > 0`, or `billing_mode = 'video'`).
- Why the 100ms floor: some upstreams buffer the entire output and only send it at the end, so the time from first token to completion is just 1–10ms.
  Dividing directly yields bogus spikes of 70,000–110,000 t/s (values of 109500 t/s and 73000 t/s have been reported in the past).
  Such records have no trustworthy generation speed, so they are hidden rather than shown with a misleading value.
- Difference from the old definition: the old frontend used "output ÷ total duration" (including queueing and time to first token); the new one subtracts time to first token,
  so values are usually higher and closer to the model's actual generation speed. Output tokens include reasoning tokens that the upstream counts toward output usage.

### 2.3 Dedicated Columns and Server-Side Sorting

Two new columns in the admin detail table (can be hidden via "Column settings"):

| Column | Sort key `sort_by` |
| --- | --- |
| TPS | `output_tps` |
| Cache rate | `cache_hit_rate` |

API: `GET /api/v1/admin/usage?sort_by=output_tps&sort_order=desc&start_date=…&end_date=…`

- Records where the metric does not apply (NULL) are always sorted last regardless of direction (`NULLS LAST`); the secondary sort is `id`.
- Sorting by an expression cannot use an index, so **`start_date` is required** (or `request_id`); otherwise the API returns 400.
  The admin page always sends a date range (last 24 hours by default). Sorting over a wide range scans every row in that range, so keep the time range as narrow as possible.
- The time to first token / total duration / TPS / cache rate display in the existing "Status" column is unchanged.

## 3. Aggregated Analytics (New API)

### 3.1 API

```
GET /api/v1/admin/usage/analytics
```

| Parameter | Description |
| --- | --- |
| `dimension` | `account` (default, upstream account) / `group` (group) / `model` (requested model) |
| `start_date`, `end_date` | Required, `YYYY-MM-DD`, parsed in `timezone`; the whole day of `end_date` is included; span ≤ 93 days |
| `timezone` | IANA time zone (sent automatically by the frontend) |
| `group_id`, `account_id` | Optional filters |
| `model` | Optional; filters by requested model (`requested_model`, falling back to `model` when absent) |
| `limit` | Number of rows returned, default 200, max 1000; when exceeded, rows are truncated by revenue descending and `truncated=true`; the totals row still covers everything |

Returned `data`:

```jsonc
{
  "dimension": "account",
  "start_time": "...", "end_time": "...",
  "rows": [ /* UsageAnalyticsRow */ ],
  "totals": { /* grand totals, same structure, key/name empty */ },
  "truncated": false,
  "error_count_available": true
}
```

`UsageAnalyticsRow` fields:

| Field | Definition |
| --- | --- |
| `key` / `id` / `name` / `platform` | Dimension value; the account dimension includes the account name and platform; in the group dimension `key="0"` means no group; the model dimension has no `id` |
| `requests` | Number of `usage_logs` rows (successfully billed requests) |
| `error_count` | Number of requests in `ops_error_logs` with status code ≥ 400, reusing the ops dashboard's per-request dedup logic (excluding count_tokens); `null` with `error_count_available=false` if the query fails |
| `input_tokens` / `output_tokens` / `cache_creation_tokens` / `cache_read_tokens` / `total_tokens` | Sums |
| `cache_hit_rate` | `SUM(read) / SUM(input + read + write)`; `null` when there are no prompt tokens |
| `first_token_p50_ms` / `first_token_p95_ms` | `percentile_cont` over `first_token_ms` (NULLs ignored) |
| `output_tps_p50` / `output_tps_p95` | `percentile_cont` over the per-row TPS defined in 2.2 (rows where it does not apply are excluded) |
| `revenue` | `SUM(actual_cost)`: the amount actually charged to users |
| `upstream_cost` | `SUM(COALESCE(account_stats_cost, total_cost) × COALESCE(account_rate_multiplier, 1))` |
| `gross_margin` | `revenue − upstream_cost` |
| `margin_pct` | `gross_margin / revenue` (0–1, can be negative); `null` when `revenue ≤ 0` |

Notes on error counts:

- A dimension value that has only errors and no successful usage does not get its own row (it has no revenue or cost), but is counted in the totals row.
- In the account dimension, errors with no assigned account (e.g. no available account) are counted only in the totals.
- The model dimension matches on `ops_error_logs.model`, which may differ slightly from the requested-model definition used for `usage_logs`.

### 3.2 SQL and Performance

- Single scan: `WITH scoped AS (… WHERE created_at ∈ [start, end) …)`, then
  `GROUP BY GROUPING SETS ((dim_key), ())` produces the group rows and the totals row together; names are joined after aggregation via `LEFT JOIN accounts/groups`.
- The time filter uses the existing index `idx_usage_logs_created_at`; with `account_id` / `group_id`,
  `idx_usage_logs_account_created_at` / `idx_usage_logs_group_created_at_not_null` can be used.
  Percentiles require sorting the rows in range and cannot be pre-aggregated, hence the span limit of ≤ 93 days; **no index migration was added**.
- Error counts come from a second query (`ops_error_logs`, using `idx_ops_error_logs_created_at` and similar indexes); a failure there does not affect the main result.

### 3.3 Page

The detail card on the Usage Records page gains an "Analytics" tab (admin only; data loads on first switch):

- "Group by" switches between upstream account / group / model;
- Follows the page's time range and the "Group / Account / Model" filters (the Analytics tab hides filters that don't apply, such as user, API key, and request type);
- Clicking a column header sorts client-side (default: revenue descending, empty values last); a totals row sits at the bottom;
- When the upstream cost toggle is off, the "Upstream cost / Gross profit / Gross margin" columns are hidden.

## 4. Gross Profit Report

Gross profit = revenue (`actual_cost`, the amount actually deducted from users) − upstream cost, viewable by three dimensions: upstream account / group / model.
These are the "Revenue / Upstream cost / Gross profit / Gross margin" columns in the "Analytics" tab; the API is the same as in 3.1.

The upstream cost definition is exactly the same as the existing "Account cost" stat card:

```
upstream_cost = (account_stats_cost ?? total_cost) × (account_rate_multiplier ?? 1.0)
```

- A NULL `account_rate_multiplier` (historical data) is treated as 1.0;
- `account_stats_cost` is the precomputed cost under the account's stats pricing and takes precedence over `total_cost` when present; when it is NULL, the result is
  `total_cost × account_rate_multiplier`.

Rows with negative gross profit are shown in red. When the upstream cost toggle is off, cost and gross profit are hidden together.

## 5. Related Code

| Location | Contents |
| --- | --- |
| `backend/internal/pkg/usagestats/usage_metrics.go` | Canonical definitions of hit rate / TPS / upstream cost / gross profit; derived sort keys |
| `backend/internal/pkg/usagestats/usage_analytics.go` | Aggregation dimensions, filters, result structures |
| `backend/internal/repository/usage_log_metrics_sql.go` | Corresponding SQL expressions |
| `backend/internal/repository/usage_log_analytics.go` | Aggregation query and error count merging |
| `backend/internal/repository/usage_log_repo_query.go` | `usageLogOrderBy` derived sorting |
| `backend/internal/handler/admin/usage_analytics_handler.go` | `GET /admin/usage/analytics` |
| `frontend/src/composables/useUpstreamCostVisibility.ts` | Upstream cost toggle |
| `frontend/src/components/admin/usage/UsageAnalyticsPanel.vue` | Analytics tab |
| `frontend/src/utils/usageTps.ts`, `usageCacheRate.ts` | Frontend metric definitions |
