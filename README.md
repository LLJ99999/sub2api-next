<div align="center">

<img src="assets/logo.svg" alt="Sub2API Logo" width="128" />

# Sub2API Next

[![CI](https://github.com/LLJ99999/sub2api-next/actions/workflows/backend-ci.yml/badge.svg?branch=main)](https://github.com/LLJ99999/sub2api-next/actions/workflows/backend-ci.yml)
[![Go](https://img.shields.io/badge/Go-1.27.2-00ADD8.svg)](https://golang.org/)
[![Vue](https://img.shields.io/badge/Vue-3.4+-4FC08D.svg)](https://vuejs.org/)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-15+-336791.svg)](https://www.postgresql.org/)
[![Redis](https://img.shields.io/badge/Redis-7+-DC382D.svg)](https://redis.io/)
[![License](https://img.shields.io/badge/License-LGPL--3.0-blue.svg)](LICENSE)

**[api.zhigeng.io](https://api.zhigeng.io/) 维护的 Sub2API 分支**

面向「外接中转来水」场景

更稳的上游调度 · 看得清的成本与毛利 · 可观测的上游运维

</div>

## 这是什么

Sub2API Next 由 [api.zhigeng.io](https://api.zhigeng.io/) 维护，基于 [ranxi2001/sub2api](https://github.com/ranxi2001/sub2api)（其本身为 [Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api) 的分支）继续演进，保留全部账号池能力（OAuth、质量运维、Mihomo 出口、Excel / BPS 等），重点补齐**上游大量来自其他中转站**时的调度韧性、成本核算和上游运维。

血缘关系：`Wei-Shaw/sub2api` → `ranxi2001/sub2api` → `LLJ99999/sub2api-next`

## 相比上游新增

### 调度与故障处理

- **402 可恢复冷却**：余额不足等 HTTP 402 默认冷却 30 分钟后自动恢复，池模式同样生效；OpenAI `deactivated_workspace` 仍永久停用。
- **外接中转熔断器**：Redis 滑动窗口统计、模型级冷却、指数退避、半开状态最多 2 个探测请求，取代"一错就临时禁用"。
- **成功自愈**：API Key 账号请求成功后自动清除残留冷却。
- **全冷却兜底**：所有候选账号都在冷却时执行受控半开探测，而不是直接返回 503。
- **故障转移时间预算**：限制单次请求的总重试时长。
- **冷却可见与一键恢复**：管理端显示冷却原因、状态码、剩余时间和熔断等级；账号编辑可开关 `upstream_relay_policy`。

详见 [上游中转韧性](docs/upstream-relay-resilience.md)。

### 使用记录与报表

- **上游成本显隐开关**：一键隐藏表格、tooltip、统计卡和导出中的全部成本信息，方便截图分享。
- **TPS / 缓存命中率**独立列，支持服务端排序；类型徽章下同时显示缓存率和 TPS。
- **聚合分析**：按账号 / 分组 / 模型统计 TTFT 与 TPS 的 P50/P95、错误数、收入、上游成本、毛利与毛利率。

详见 [使用分析](docs/usage-analytics.md)。

### 上游运维

- **倍率 / 余额历史与告警**：记录上游倍率和余额变化，支持合并窗口、方向过滤、静音和通知推送。[说明](docs/upstream-change-alerts.md)
- **公开中转快照**：`/.well-known/ai-transit.json`、`/api/public/transit/v1/snapshot` 和 `/public/transit` 页面，只输出聚合、匿名的数据。[说明](docs/public-transit-snapshot.md)
- **上游质量实测**：按账号直连上游发送极小的流式请求，记录 TTFT、输出 TPS、上游 token 用量、实际模型名和异常提示（默认关闭，按账号开启）。[说明](docs/upstream-quality-probes.md)

### 其他

- **Cap 自建验证码**：`cap:<key>` 或 `cap:<base>|<key>`；CSP 已加入 `'wasm-unsafe-eval'`。
- **监控分组排序**：按平台顺序 → `sort_order` → 请求量。
- **缓存率口径修正**：零缓存请求不计入监控缓存率分母，Token / TPM 统计不受影响；附 [重算 SQL](docs/sql/channel_monitor_v2_recompute_cache.sql)。

新增数据库迁移：`310`（上游变化历史）、`311`（质量实测）、`320`（缓存率口径），升级时自动执行。

## 快速开始

部署方式与上游一致，参见 [部署说明](deploy/README.md) 和 [新人入门](docs/新人入门.md)。从上游版本升级时直接替换二进制或镜像，启动时会自动执行新增迁移，建议先备份数据库。

账号质量检测与恢复见 [质量运维](docs/account-quality-operations.md)；BPS 配置见 [Excel / BPS 协议](docs/excel-bps.md)。

## 已知限制

- 上游质量实测暂不支持 Gemini / Antigravity 平台。
- 故障转移时间预算尚未覆盖 Grok 媒体、图片和 embeddings 等少数重试循环。
- 半开探测遇到 400 时，探测槽位可能保留到安全过期时间才释放。

## 项目概述

Sub2API 是一个 AI API 网关平台，用于分发和管理 AI 产品订阅的 API 配额。用户通过平台生成的 API Key 调用上游 AI 服务，平台负责鉴权、计费、负载均衡和请求转发。

## 核心功能

- **多账号管理** - 支持多种上游账号类型（OAuth、API Key）
- **API Key 分发** - 为用户生成和管理 API Key
- **精确计费** - Token 级别的用量追踪和成本计算
- **智能调度** - 智能账号选择，支持粘性会话
- **并发控制** - 用户级、账号级和 API Key 级并发限制，支持可配置的 Key 等待队列
- **速率限制** - 可配置的请求和 Token 速率限制
- **内置支付系统** - 支持 EasyPay 易支付、支付宝官方、微信官方、Stripe，用户自助充值，无需独立部署支付服务（[配置指南](docs/PAYMENT_CN.md)）
- **管理后台** - Web 界面进行监控和管理
- **外部系统集成** - 支持通过 iframe 嵌入外部系统（如工单等），扩展管理后台功能

### 首次初始化管理员

全新自动安装时，`ADMIN_EMAIL` 和 `ADMIN_PASSWORD` 留空会生成随机登录邮箱和密码，可从首次启动日志中的 `Generated admin` 获取。显式指定时，邮箱必须符合登录格式，密码必须为 8–72 字节。已有管理员或已有用户的部署跳过创建与此项校验。详细配置见 [部署说明](deploy/README.md)。

## API Key 并发等待队列

当 API Key 设置了大于 `0` 的 `concurrency_limit` 时，达到上限后的新请求会在原连接上等待空闲槽位。默认值 `0` 不增加 Key 级并发限制。等待策略是全局配置，进程启动时读取：

```yaml
gateway:
  api_key_queue:
    max_waiting: 5
    timeout_seconds: 30
```

| 环境变量 | 默认值 | 说明 |
|----------|--------|------|
| `GATEWAY_API_KEY_QUEUE_MAX_WAITING` | `5` | 每个受限 Key 允许额外等待的请求数；`0` 关闭 Key 排队。 |
| `GATEWAY_API_KEY_QUEUE_TIMEOUT_SECONDS` | `30` | 单个请求最长等待秒数，必须为正整数。 |

- 等待名额按 Key 独立计算，不保证 FIFO；`concurrency_limit: 0` 的 Key 不进入队列。
- 队列用于 HTTP/SSE、OpenAI Responses WebSocket 每轮请求和 Live 创建的 Key 准入，与用户级、账号级等待限制独立。
- 等待期间复核 Key、用户及当前请求的模型/能力权限。Key 换组、平台或计费模式变化返回可重试的 `503` / `API_KEY_GROUP_CHANGED`。WebSocket 鉴权/权限失败以 `1008` 关闭，容量或临时服务错误以 `1013` 关闭。
- 关闭排队时达到上限返回 `429` / `gateway_concurrency_limit`；队列满返回 `429` / `api_key_queue_full`；等待超时返回 `429` / `api_key_queue_timeout`。
- 两个配置值必须为整数；负数、小数、非法字符串或超出范围会阻止启动。即使关闭排队，超时也必须为正数。
- 修改 Compose `.env` 后需要重建容器以更新环境变量。调大等待时间时，需要确认客户端及反向代理的首字节超时。
- 升级自动应用 `237_add_api_key_concurrency_limit.sql`，旧 Key 默认为 `0`。回退二进制不会撤销数据库新增字段。

## 技术栈

| 组件 | 技术 |
|------|------|
| 后端 | Go 1.27.2, Gin, Ent |
| 前端 | Vue 3.4+, Vite 5+, TailwindCSS |
| 数据库 | PostgreSQL 15+ |
| 缓存/队列 | Redis 7+ |
| API 协议 | OpenAI Responses / Chat Completions、Anthropic Messages、Gemini、SSE、WebSocket |
| 调度与可靠性 | 粘性会话、并发控制、限流、故障转移、模型隔离与冷却 |
| 观测与运维 | 结构化日志、请求分段耗时、健康检查、Mihomo 出口与 systemd |
| 交付与质量 | Docker Compose、GitHub Actions、Go 单元测试、前端类型检查与构建 |

## 贡献

欢迎提交 Issue 和 PR。请在 PR 中说明影响的请求路径、账号类型、配置默认值和兼容边界，并列出实际运行过的测试；不要提交 Token、OAuth 导出、代理凭据或生产配置。推送和 PR 会自动运行 Go 单元测试、集成测试、golangci-lint 和前端测试。

上游的贡献者与社区请见 [ranxi2001/sub2api](https://github.com/ranxi2001/sub2api) 和 [Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api)，感谢他们的工作。

---

## ⚠️ 重要提醒

使用本项目前，请务必仔细阅读以下内容：

- **🚨 服务条款风险**：使用本项目可能违反 Anthropic 等上游服务商的服务条款。请在使用前仔细阅读相关服务商的用户协议，由此产生的一切风险由用户自行承担。
- **⚖️ 合规使用**：请在符合您所在国家或地区法律法规的前提下使用本项目，严禁将其用于任何违法违规用途。
- **📖 免责声明**：本项目仅供技术学习与研究使用，作者不对因使用本项目导致的账户封禁、服务中断、数据丢失或其他任何直接或间接损失承担责任。
- **🚫 无商业授权**：本项目从未授权任何个人或组织基于本项目开展任何形式的商业化运营。任何以本项目名义或基于本项目从事的商业行为均与本项目及其开发者无关，由此产生的一切纠纷、损失和法律责任由行为主体自行承担。

## 许可证

本项目基于 [GNU 宽通用公共许可证 v3.0](LICENSE)（或更高版本）授权，沿用上游许可。

Copyright (c) 2026 Wesley Liddick 及各分支贡献者
