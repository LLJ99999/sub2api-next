[简体中文](upstream-relay-resilience.md) | [English](upstream-relay-resilience.en.md) | [繁體中文](upstream-relay-resilience.zh-TW.md)

# 上游中转韧性：402 冷却、中转熔断、成功自愈、换号时间预算与全冷却兜底

本文说明 P0 调度韧性改动的行为、配置与运维方式，适用于对接其他中转站 / NewAPI / Sub2API 的 API Key 账号，也覆盖所有平台通用的 402 冷却与换号预算。

## 1. 总览

| 能力 | 作用范围 | 默认 | 配置键 |
| --- | --- | --- | --- |
| 402 可恢复冷却 | 所有账号（OpenAI `deactivated_workspace` 除外） | 开启，30 分钟 | `rate_limit.payment_required_402_cooldown_minutes` |
| 上游中转熔断 | 开启 `extra.upstream_relay_policy` 的 API Key 账号 | 账号级手动开启 | `rate_limit.upstream_relay.*` |
| 半开试探上限 | 熔断冷却刚结束的账号 / 模型 | 固定最多 2 个并发试探 | 无（内置） |
| 成功自愈 | 所有平台的 API Key 账号 | 开启 | 无 |
| 换号时间预算 | 所有网关入口的 failover 循环 | 关闭（0） | `gateway.failover_time_budget_seconds` |
| 全冷却兜底 | API Key 账号 | 开启，10 秒一次探测 | `gateway.cooldown_fallback.*` |

## 2. 402 可恢复冷却

过去上游返回 HTTP 402 会把账号永久标记为 `error`，需要人工恢复。现在：

- 402（OpenAI `deactivated_workspace` 仍为永久错误）会把账号置为**临时不可调度**，时长为 `payment_required_402_cooldown_minutes`（默认 30 分钟），原因字段为结构化 JSON（状态码 402 + 截断后的上游消息）。
- 号池模式账号遇到 402 同样冷却，不再走 `pool_mode_error_skipped`；用户自定义的 `temp_unschedulable_rules` 优先级更高。
- 上游余额探测一旦看到正余额，会提前解除 402 冷却。
- 账号运维通知把裸 402 归类为 `balance_low`。
- 402 冷却**不会**被全冷却兜底选中（探测无法解决余额耗尽）。

## 3. 上游中转熔断（`upstream_relay_policy`）

### 3.1 开启方式

在后台「账号管理 → 编辑账号」中，API Key 账号新增「上游中转熔断策略」开关，对应 `accounts.extra.upstream_relay_policy=true`。只有 API Key 账号生效；关闭时写入 `false`。

### 3.2 判定规则

- 计入失败：429、所有 5xx、超时 / 传输层瞬时错误（超时按账号级计）。
- 不计入：4xx 客户端 / 参数错误；402、401、403 保持各自原有处理；命中 `temp_unschedulable_rules` 或自定义错误码时仍以规则为准。
- 失败与成功写入 Redis 滑动窗口（跨实例共享，无 Redis 扩展时退化为进程内存储）。
- 窗口内失败数 ≥ `min_failures` **且** 失败率 ≥ `failure_rate_percent` 时触发熔断。
- 冷却时长按退避级别递增：5 秒 → 15 秒 → 60 秒 → 5 分钟 → 之后翻倍，封顶 `max_cooldown_seconds`；不采用上游返回的重置时间提示。
- 2 秒内的并发触发只升一级，避免一次突发把级别连升多级。
- 级别在 `level_reset_seconds` 内没有新的熔断则过期，下次从第一级开始。
- 粒度：`scope: model`（默认）只冷却出错的模型（写入 `extra.model_rate_limits`），`scope: account` 冷却整个账号（写入临时不可调度）。

### 3.3 半开与试探上限

冷却到期后账号进入**半开**状态：账号快照上仍保留已过期的熔断原因，直到一次成功把它清除或一次失败重新熔断。

- 半开期间，同一账号（或同一模型作用域）**最多放行 2 个并发试探请求**。计数器存放在 Redis（键前缀 `upstream_relay_half_open:`，与熔断窗口同一哈希标签），无 Redis 时使用进程内计数。
- 调度选中一个已满额的半开账号时，会释放已占的并发槽位、把该账号排除后重新选号（每次请求最多重选 3 次）。
- 试探结果：
  - 成功 → 关闭熔断、退避级别清零、清除冷却残留，计数器重置；
  - 失败（429/5xx/超时）→ 立即按下一级重新熔断，计数器重置；
  - 既不成功也不计入熔断的结果（例如 400）→ 计数器在 `max(window_seconds, 30s)` 后自动过期。
- OpenAI `previous_response_id` 续链请求必须命中原账号，不参与半开重选。
- 存储出错时放行（fail open），不会因为 Redis 抖动把账号锁死。

### 3.4 Redis 键

| 键 | 说明 |
| --- | --- |
| `upstream_relay:{<账号ID>\|<scope>}:fail` / `:all` / `:seq` | 滑动窗口（失败 / 全部 / 序号） |
| `upstream_relay:{<账号ID>\|<scope>}:state` | 退避级别与上次熔断时间 |
| `upstream_relay_half_open:{<账号ID>\|<scope>}` | 半开试探计数 |
| `cooldown_fallback_probe:<key>` | 全冷却兜底探测锁 |

其中 `<scope>` 为 `account` 或 `model:<模型>`。

## 4. 成功自愈

API Key 账号（任意平台）收到成功响应并记账时：

- 清除残留的限流 / 过载 / 临时不可调度状态，以及本次服务模型在 `model_rate_limits` 中的条目；
- 对开启熔断的账号关闭已触发的熔断并重置退避级别；
- 仅在账号快照上确有残留时写库，健康路径零开销；
- 后台「自动停调阈值」触发的暂停不会被清除。

## 5. 换号时间预算

`gateway.failover_time_budget_seconds`（默认 0 = 不限制）限制单个请求在 failover 上花费的总时间。超出预算后不再切换账号、不再同号重试，按正常的“重试耗尽”路径返回最后一次上游错误（保留原始状态码）。覆盖 Anthropic / Gemini / Chat Completions / Responses / SystemOne 及 OpenAI Responses / Chat / WebSocket 循环。

建议：客户端超时较短（如 60 秒）时，把预算设为略小于客户端超时，避免客户端已断开而网关仍在换号。

## 6. 全冷却兜底

当选号失败**仅仅**是因为所有候选 API Key 账号都在冷却（限流、过载、临时不可调度、熔断、模型冷却），而不是停用、错误、过期、配额耗尽、402 余额问题或自动停调阈值暂停时：

- 网关挑选**最早结束冷却**的账号放行一次探测请求（同时结束时取最近使用的账号），而不是直接返回 503；
- 同一 分组 / 平台 / 模型 在 `probe_interval_seconds`（默认 10 秒）内只放行一次探测（Redis 跨实例锁，`SETNX`）；
- 探测成功由成功自愈清除冷却；失败按正常错误路径处理；
- 渠道定价限制、路由错误等主动拒绝不会触发兜底；
- OpenAI 兼容网关仅对普通 HTTP 请求兜底（无 compact、能力要求、`previous_response_id` 约束，传输方式为 Any 或 HTTP/SSE）。

## 7. 后台展示与一键恢复

账号列表的状态列新增「冷却 N」徽标，悬停可查看每条生效中的冷却：

- 类型（临时不可调度 / 限流 / 过载 / 模型冷却）与作用域（整个账号 / 具体模型）；
- 触发状态码与截断后的上游消息（最多 160 字）；
- 剩余时间；
- 熔断级别（来自原因 JSON 的 `breaker_level`）。

提示框内「一键恢复」调用 `POST /api/v1/admin/accounts/:id/recover-state`，清除可恢复状态后刷新该行。

后端接口：账号详情与列表响应新增 `cooldowns` 数组（仅在存在生效冷却时返回），字段为 `kind`、`scope`、`model`、`status_code`、`message`、`keyword`、`breaker_level`、`until`、`remaining_seconds`。

## 8. 配置示例

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

## 9. 排查

- 日志关键字：`upstream_relay_breaker_tripped`、`upstream_relay_breaker_closed`、`upstream_relay_half_open_trial_capped`（debug）、`cooldown_fallback_probe_selected`、`upstream_success_cleared_residual_cooldown`。
- 账号持续半开：确认试探请求是否返回了既不成功也不计入熔断的状态（如 400），计数器会在窗口时长后过期。
- 全冷却仍返回 503：检查是否存在 402 冷却、配额耗尽或自动停调阈值暂停，这些情况不参与兜底；或同一分组 / 模型刚在探测间隔内放行过一次。
