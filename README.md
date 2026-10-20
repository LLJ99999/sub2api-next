<div align="center">

<img src="assets/logo.svg" alt="Sub2API" width="112" />

# Sub2API Next

**更适合外接 API 的 Sub2API 版本**

[![CI](https://github.com/LLJ99999/sub2api-next/actions/workflows/backend-ci.yml/badge.svg?branch=main)](https://github.com/LLJ99999/sub2api-next/actions/workflows/backend-ci.yml)
[![Go](https://img.shields.io/badge/Go-1.27.2-00ADD8.svg)](https://golang.org/)
[![Vue](https://img.shields.io/badge/Vue-3-4FC08D.svg)](https://vuejs.org/)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-15+-336791.svg)](https://www.postgresql.org/)
[![Redis](https://img.shields.io/badge/Redis-7+-DC382D.svg)](https://redis.io/)
[![License](https://img.shields.io/badge/License-LGPL--3.0-blue.svg)](LICENSE)

由 [api.zhigeng.io](https://api.zhigeng.io/) 维护 · 中文 | [English](README_EN.md)

</div>

---

## 简介

Sub2API Next 是更适合**外接 API 上游**（其他中转站、官方 API Key）的 Sub2API 版本。外接 API 账号使用独立的熔断、冷却与运维策略，**与号池隔离**：号池原有能力（OAuth、质量运维、Mihomo 出口、Excel / BPS 等）完整保留、互不干扰。

## 核心能力

### 🛡️ 外接 API 调度与容错

- **熔断器**：滑动窗口统计上游错误，达到阈值才熔断，按模型隔离；半开状态最多放行 2 个探测请求，反复失败指数退避。
- **402 可恢复冷却**：上游余额不足时冷却 30 分钟后自动恢复；OpenAI `deactivated_workspace` 仍永久停用。
- **成功自愈**：API Key 账号请求成功后自动清除残留冷却。
- **全冷却兜底**：所有候选账号都在冷却时执行受控半开探测，而非直接返回 503。
- **故障转移时间预算**：限制单次请求的总重试时长。

管理端可直接看到冷却原因、状态码、剩余时间和熔断等级，支持一键恢复；每个账号可单独开关 `upstream_relay_policy`。
→ [上游中转韧性说明](docs/upstream-relay-resilience.md)

### 📊 使用记录与报表

- **TPS、缓存命中率**独立成列，支持服务端排序；类型徽章下直接显示缓存率与 TPS。
- **上游成本一键隐藏**：表格、tooltip、统计卡、导出同步隐藏，方便截图。
- **多维聚合报表**：按账号 / 分组 / 模型查看 TTFT 与 TPS 的 P50/P95、错误数、收入、上游成本、**毛利与毛利率**。

→ [使用分析说明](docs/usage-analytics.md)

### 🔭 上游运维

- **倍率 / 余额变化告警**：自动记录上游倍率与余额历史，支持合并窗口、涨跌方向过滤、按账号静音，并推送通知。→ [说明](docs/upstream-change-alerts.md)
- **上游质量实测**：按账号直连上游发极小的流式请求，测 TTFT、输出 TPS、真实 token 用量与上游实际模型名。默认关闭，按账号开启。→ [说明](docs/upstream-quality-probes.md)
- **公开中转快照**：通过 `/.well-known/ai-transit.json`、`/api/public/transit/v1/snapshot` 和 `/public/transit` 对外展示分组、价格与可用率，只输出聚合匿名数据。→ [说明](docs/public-transit-snapshot.md)

### 🔧 其他改进

- 支持 **Cap 自建验证码**（`cap:<key>` 或 `cap:<base>|<key>`），CSP 已放行 `'wasm-unsafe-eval'`。
- 监控分组按 平台顺序 → `sort_order` → 请求量 排序。
- 监控缓存率不再把零缓存请求计入分母，Token / TPM 统计不受影响；附[历史数据重算 SQL](docs/sql/channel_monitor_v2_recompute_cache.sql)。

## 快速开始

暂未提供预构建镜像，请从源码构建：

```bash
git clone https://github.com/LLJ99999/sub2api-next.git
cd sub2api-next
docker build -t sub2api-next:latest .
```

然后按 [部署说明](deploy/README.md) 使用 Docker Compose 部署，把 `deploy/docker-compose.yml` 中 `sub2api` 服务的 `image` 改为 `sub2api-next:latest` 即可。首次启动若未指定管理员账号，会在日志 `Generated admin` 中输出随机邮箱和密码。

**从上游版本升级**：备份数据库后替换镜像重启即可。启动时自动执行新增迁移 `310`（上游变化历史）、`311`（质量实测）、`320`（缓存率口径）。

更多文档：[新人入门](docs/新人入门.md) · [质量运维](docs/account-quality-operations.md) · [Excel / BPS](docs/excel-bps.md) · [支付配置](docs/PAYMENT_CN.md)

## 技术栈

| 组件 | 技术 |
| --- | --- |
| 后端 | Go 1.27.2 · Gin · Ent |
| 前端 | Vue 3 · Vite · TailwindCSS |
| 存储 | PostgreSQL 15+ · Redis 7+ |
| 协议 | OpenAI Responses / Chat Completions · Anthropic Messages · Gemini · SSE · WebSocket |
| 交付 | Docker Compose · GitHub Actions（Go 单测 / 集成测试 / golangci-lint / 前端测试 / wire 校验） |

## 已知限制

- 上游质量实测暂不支持 Gemini / Antigravity。
- 故障转移时间预算尚未覆盖 Grok 媒体、图片和 embeddings 等少数重试循环。
- 半开探测遇到 400 时，探测槽位可能保留到安全过期时间才释放。

## 项目来源

`Wei-Shaw/sub2api` → `ranxi2001/sub2api` → **`LLJ99999/sub2api-next`**

感谢 [Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api) 与 [ranxi2001/sub2api](https://github.com/ranxi2001/sub2api) 及其贡献者的工作。原有功能（多账号管理、计费、并发控制、支付、管理后台等）的完整说明请参考上游文档。

## 贡献

欢迎 Issue 与 PR。提交时请说明影响的请求路径、账号类型、默认值和兼容性，并附上实际运行过的测试。**不要提交 Token、OAuth 导出、代理凭据或任何生产配置。**

## ⚠️ 免责声明

- 使用本项目可能违反 Anthropic、OpenAI 等上游服务商的服务条款，风险由使用者自行承担。
- 请在遵守所在地法律法规的前提下使用，严禁用于违法用途。
- 本项目仅供技术学习与研究，作者不对账号封禁、服务中断、数据丢失等任何损失负责。

## 许可证

[GNU 宽通用公共许可证 v3.0](LICENSE)（或更高版本），沿用上游许可。
