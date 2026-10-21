[简体中文](upstream-change-alerts.md) | [English](upstream-change-alerts.en.md) | [繁體中文](upstream-change-alerts.zh-TW.md)

# Upstream Rate Multiplier / Balance Change Alerts

This feature persists the upstream group rate multipliers and wallet balances already read by "Upstream Billing Probes" as history, detects the moments when a rate multiplier changes or a balance drops below a threshold, and can send alerts through the notification channels already configured in Account Ops.

> It makes **no** additional requests to upstreams: it only observes the snapshots written by each upstream billing probe. All switches are off by default.

## 1. Prerequisites

- The account is an API Key account and has "Upstream billing auto-probe" enabled under "Edit Account" (rate multiplier / balance data comes from probe snapshots).
- To push notifications, at least one notification channel must be configured in "Account Ops"; otherwise events are recorded as `failed`.

## 2. Where to find it

"Upstream Changes" in the admin sidebar (`/admin/upstream-changes`):

- **Settings**: history recording, rate multiplier alerts, balance alerts, muted accounts;
- **Events**: a list of rate multiplier changes and balance threshold crossings, filterable by site, kind, direction, and account;
- **Snapshots**: rate multiplier / balance chart data for a single account over the last N days.

## 3. Settings (`settings` table key `upstream_change_alert_settings`)

| Field | Default | Description |
| --- | --- | --- |
| `record_history` | false | Write a snapshot on every successful probe and record all change events |
| `retention_days` | 30 | Days to retain snapshots / events (1–365); cleanup runs at most once per hour |
| `rate_alert_enabled` | false | Rate multiplier change alerts |
| `rate_alert_direction` | `both` | `both` / `up` / `down`, evaluated on the **net change** within the merge window |
| `rate_min_change_percent` | 0 | No alert when the relative change is below this percentage (the event is still recorded), 0–1000 |
| `merge_window_minutes` | 10 | Window in which consecutive changes on the same account are merged into one notification (0–1440) |
| `cooldown_minutes` | 60 | Minimum interval between two rate multiplier notifications for the same account (0–10080) |
| `balance_alert_enabled` | false | Alert when the balance drops below the threshold |
| `balance_threshold` | 5 | Global balance threshold (≥0); applies only to accounts without their own balance rule |
| `balance_unit` | `USD` | The threshold is compared only when the snapshot unit matches this value |
| `balance_cooldown_minutes` | 360 | Minimum interval between two balance alerts for the same account (5–10080) |
| `muted_account_ids` | [] | Muted accounts (up to 1000); their events are recorded as `muted` |

The feature is active as soon as any one of "history recording / rate multiplier alerts / balance alerts" is enabled; when all three are off, nothing is written to the database.

## 4. Detection rules

### 4.1 Rate multiplier changes

- The comparison uses the **effective billing rate multiplier**: the user override rate multiplier takes precedence, otherwise the upstream group rate multiplier; time-of-day peak rate multipliers are not included.
- An event is generated when both the old and new rate multipliers exist, the rate multiplier from this probe is fresh, and the difference exceeds 1e-9; the direction is `up` / `down`.
- Notifications are sent together during the background cycle (see 4.3). Multiple changes on the same account within the merge window are merged into one notification, which shows "first old value → latest value" and the number of changes; the merged older events are marked `merged`.
- A net change of 0 is recorded as `suppressed`; a direction mismatch or a relative change below the threshold is recorded as `filtered`.

### 4.2 Balance below threshold

- When the balance goes from above the threshold to ≤ the threshold, a `low` event is recorded; when it goes from ≤ the threshold back above it, a `recovered` event is recorded (`recovered` is recorded only, not pushed).
- If Account Ops already has a dedicated balance rule for the account, this feature does not send a duplicate alert (`suppressed`); the Account Ops rule takes precedence.
- A repeat drop below the threshold during the cooldown period is recorded as `suppressed`.

### 4.3 Sending notifications

- At the start of each cycle, the upstream billing probe service flushes pending events once, using a leader lock (`upstream:change:flush:leader`) so that only one instance sends in a multi-instance deployment.
- Notifications are sent via the Account Ops broadcast interface to its configured channels; if no channel is available or sending fails, the event is recorded as `failed`.

### 4.4 Event state `notify_state`

| State | Meaning |
| --- | --- |
| `none` | Recorded only, no notification needed (alert switch off, or `recovered`) |
| `pending` | Waiting for the next flush |
| `sent` / `failed` | Sent / failed to send |
| `muted` | The account is muted |
| `suppressed` | Suppressed by cooldown, a dedicated rule, or a net change of 0 |
| `filtered` | Did not meet the direction or minimum change percentage |
| `merged` | Merged into the notification of the latest event in the same window |

## 5. Data tables (migration `310_upstream_change_alerts.sql`)

- `upstream_rate_snapshots`: account, site, provider, upstream group, group rate multiplier / resolved rate multiplier / effective rate multiplier, balance and unit, observation time.
- `upstream_change_events`: account, account name, site, kind (`rate_multiplier` / `balance_low`), direction, old value / new value / threshold, unit, notification state and time.

The site key is derived by normalizing the account's `base_url` (`scheme://host[:port][/path]`, with credentials, query string, and a trailing `/v1` removed). The tables store **no** credentials, secrets, or tokens. Rows are cascade-deleted when the account is deleted.

## 6. Admin API

All endpoints live under `/api/v1/admin` and require admin privileges:

| Method | Path | Description |
| --- | --- | --- |
| GET / PUT | `/upstream-changes/settings` | Read / update settings |
| GET | `/upstream-changes/events` | Paginated events; parameters `site`, `kind`, `direction`, `account_id`, `page`, `page_size` (default 50) |
| GET | `/upstream-changes/snapshots` | Snapshots for one account; parameters `account_id` (required), `days` (default 7) |
| GET | `/upstream-changes/sites` | List of sites seen so far |

## 7. Notes

- This feature depends on the freshness of probe snapshots: no events are generated when probes fail or are not enabled.
- When first enabled there is no "previous snapshot", so no rate multiplier events are generated; comparison begins after the second successful probe.
- If you also use Account Ops balance rules, we recommend keeping only one alerting method to avoid confusion (this feature already deduplicates automatically).

## Bulk enable upstream change monitoring

Rate / balance data comes from "upstream billing auto-probe". Select accounts in Account Management and use "Upstream Change Monitor" in the bulk bar to turn the auto-probe on or off for all of them; the dialog shows a result per row. When enabling, only the first account per upstream base URL is enabled and the rest are marked "Skipped (duplicate upstream)" with the first account named, so one upstream is not probed twice; disabling applies to every selected account. API: `POST /api/v1/admin/accounts/upstream-billing-probe/batch-enable` with `{"account_ids": [...], "enabled": true}`, 1–500 accounts per call, returning a `status` per account (`enabled` / `disabled` / `skipped_duplicate` / `failed`). The existing "Probe Upstream Rate & Balance" button (one immediate probe, up to 20 accounts) is unchanged.
