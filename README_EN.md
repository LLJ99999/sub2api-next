<div align="center">

<img src="assets/logo.svg" alt="Sub2API" width="112" />

# Sub2API Next

**A Sub2API edition built for external API upstreams**

[![CI](https://github.com/LLJ99999/sub2api-next/actions/workflows/backend-ci.yml/badge.svg?branch=main)](https://github.com/LLJ99999/sub2api-next/actions/workflows/backend-ci.yml)
[![Go](https://img.shields.io/badge/Go-1.27.2-00ADD8.svg)](https://golang.org/)
[![Vue](https://img.shields.io/badge/Vue-3-4FC08D.svg)](https://vuejs.org/)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-15+-336791.svg)](https://www.postgresql.org/)
[![Redis](https://img.shields.io/badge/Redis-7+-DC382D.svg)](https://redis.io/)
[![License](https://img.shields.io/badge/License-LGPL--3.0-blue.svg)](LICENSE)

Maintained by [api.zhigeng.io](https://api.zhigeng.io/)

[简体中文](README.md) | English | [繁體中文](README_TW.md)

</div>

---

## Overview

Sub2API Next is a Sub2API edition better suited to **external API upstreams** (other relay stations and official API keys). External API accounts run on their own circuit-breaker, cooldown and operations policies, **isolated from the account pool**: all existing pool features (OAuth, quality operations, Mihomo egress, Excel / BPS, and more) are kept intact and unaffected.

## Key features

### 🛡️ Scheduling and fault tolerance for external APIs

- **Circuit breaker**: sliding-window error tracking that trips only past a threshold, scoped per model; the half-open state admits at most 2 probe requests, with exponential backoff on repeated failures.
- **Recoverable 402 cooldown**: when an upstream runs out of balance, the account cools down for 30 minutes and then recovers automatically; OpenAI `deactivated_workspace` is still disabled permanently.
- **Self-healing on success**: a successful request clears any leftover cooldown on API-key accounts.
- **All-cooled fallback**: when every candidate account is cooling down, a controlled half-open probe is attempted instead of returning 503 right away.
- **Failover time budget**: caps the total retry time of a single request.

The admin panel shows the cooldown reason, status code, time remaining and breaker level, with one-click restore; `upstream_relay_policy` can be toggled per account.
→ [Upstream relay resilience](docs/upstream-relay-resilience.en.md)

### 📊 Usage records and reports

- **TPS and cache hit rate** as dedicated, server-side sortable columns; both also appear as badges under the request type.
- **One-click upstream cost hiding**: hides cost in the table, tooltips, stats cards and exports, handy for screenshots.
- **Aggregated reports** by account / group / model: TTFT and TPS P50/P95, error count, revenue, upstream cost, **gross profit and margin**.

→ [Usage analytics](docs/usage-analytics.en.md)

### 🔭 Upstream operations

- **Rate / balance change alerts**: records upstream rate-multiplier and balance history, with merge windows, direction filters, per-account muting and notifications. → [Docs](docs/upstream-change-alerts.en.md)
- **Upstream quality probes**: sends a tiny streaming request straight to an account's upstream and records TTFT, output TPS, real token usage and the model the upstream actually served. Off by default, enabled per account. → [Docs](docs/upstream-quality-probes.en.md)
- **Public transit snapshot**: publishes groups, prices and availability via `/.well-known/ai-transit.json`, `/api/public/transit/v1/snapshot` and the `/public/transit` page, exposing only aggregated, anonymous data. → [Docs](docs/public-transit-snapshot.en.md)

### 🔧 Other improvements

- **Self-hosted Cap captcha** (`cap:<key>` or `cap:<base>|<key>`); CSP now allows `'wasm-unsafe-eval'`.
- Monitor groups are ordered by platform → `sort_order` → request volume.
- Requests with zero cache no longer count toward the monitor cache-rate denominator; token / TPM stats are unaffected. A [recompute SQL](docs/sql/channel_monitor_v2_recompute_cache.sql) is included for historical data.

## Quick start

No prebuilt image is published yet, so build from source:

```bash
git clone https://github.com/LLJ99999/sub2api-next.git
cd sub2api-next
docker build -t sub2api-next:latest .
```

Then deploy with Docker Compose following the [deployment guide](deploy/README.md), changing the `image` of the `sub2api` service in `deploy/docker-compose.yml` to `sub2api-next:latest`. If no admin account is set, a random email and password are printed in the startup log under `Generated admin`.

**Upgrading from upstream**: back up the database, swap the image and restart. New migrations `310` (upstream change history), `311` (quality probes) and `320` (cache-rate accounting) run automatically on startup.

## Tech stack

| Component | Technology |
| --- | --- |
| Backend | Go 1.27.2 · Gin · Ent |
| Frontend | Vue 3 · Vite · TailwindCSS |
| Storage | PostgreSQL 15+ · Redis 7+ |
| Protocols | OpenAI Responses / Chat Completions · Anthropic Messages · Gemini · SSE · WebSocket |
| Delivery | Docker Compose · GitHub Actions (Go unit / integration tests, golangci-lint, frontend tests, wire check) |

## Known limitations

- Quality probes do not support Gemini / Antigravity yet.
- The failover time budget does not yet cover a few retry loops (Grok media, images, embeddings).
- A half-open probe that hits a 400 may hold its slot until the safety expiry.

## Lineage

`Wei-Shaw/sub2api` → `ranxi2001/sub2api` → **`LLJ99999/sub2api-next`**

Thanks to [Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api), [ranxi2001/sub2api](https://github.com/ranxi2001/sub2api) and their contributors. For the full documentation of the original features (multi-account management, billing, concurrency control, payments, admin panel and more), see the upstream projects.

## Contributing

Issues and PRs are welcome. Please describe the affected request paths, account types, defaults and compatibility, and list the tests you actually ran. **Never commit tokens, OAuth exports, proxy credentials or any production configuration.**

## ⚠️ Disclaimer

- Using this project may violate the terms of service of upstream providers such as Anthropic and OpenAI; you bear all risk.
- Use it only in compliance with the laws of your jurisdiction; any illegal use is prohibited.
- This project is for technical learning and research only. The authors are not liable for account bans, service interruptions, data loss or any other damages.

## License

[GNU Lesser General Public License v3.0](LICENSE) (or later), inherited from upstream.
