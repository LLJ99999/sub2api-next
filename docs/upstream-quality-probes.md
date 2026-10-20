# 上游质量探测

上游质量探测针对单个 API Key 账号，直接向其上游发送一个很小的流式请求，记录现有工具看不到的质量指标：首字时间（TTFT）、输出 TPS、上游返回的输入 / 输出 / 缓存 token、上游实际模型名，以及异常提示。

## 1. 与现有工具的关系

| 工具 | 粒度 | 记录内容 |
| --- | --- | --- |
| 渠道监控 V2 | 平台 / 分组 / 模型（真实流量聚合，非单账号） | 可用率、延迟、TTFT、缓存命中率 |
| 定时测试 / Pelican 测试 | 单账号（走账号测试链路） | 总耗时与回答文本 |
| **上游质量探测** | 单账号（直连上游） | TTFT、输出 TPS、上游 token 用量、上游模型名、异常提示 |

质量探测是补充，不替代上述工具。

## 2. 成本与默认值

- 每次探测都会**消耗上游额度**（输出上限 64 token），因此：
  - 全局开关默认关闭（`settings` 表键 `upstream_quality_probe_settings`）；
  - 每个账号需要单独添加探测配置（显式 opt-in）。
- 手动「立即运行」不受全局开关限制。

## 3. 适用账号

- 状态为启用的 **API Key** 账号；
- Anthropic 平台使用 Messages 协议（`/v1/messages`），其他 OpenAI 兼容平台使用 Chat Completions（`/v1/chat/completions`）；
- Gemini、Antigravity 不支持。
- 请求经由账号测试同一套 HTTP 上游通道发送（遵循账号代理与 TLS 配置），关闭重定向，未配置 `base_url` 时 OpenAI / Anthropic 使用官方地址；`base_url` 需通过与账号测试相同的地址校验。
- 模型名会先按账号的模型映射转换成上游模型。

## 4. 入口与配置

后台侧边栏「上游质量探测」（`/admin/upstream-quality-probes`）。

### 4.1 全局设置

| 字段 | 默认 | 说明 |
| --- | --- | --- |
| `enabled` | false | 定时探测总开关 |
| `max_results_per_account` | 100 | 每个账号保留的结果条数（10–1000） |
| `input_token_tolerance_pct` | 200 | 输入 token 与本地估算的容差百分比（50–10000） |

### 4.2 账号配置

| 字段 | 说明 |
| --- | --- |
| `enabled` | 是否参与定时探测 |
| `model` | 探测模型（单个模型名，最多 128 字符，不能含空白） |
| `interval_minutes` | 探测间隔，5–1440 分钟，默认 60 |

保存配置后，下一次调度周期立即执行，不等待旧计划。

## 5. 调度

- 挂在定时测试执行器的同一个 tick 上，与渠道监控 Candy、Pelican 分组测试并行运行。
- 每个 tick 最多领取 10 个到期账号，4 个并发 worker；领取使用 `FOR UPDATE SKIP LOCKED` 并租约 5 分钟，多实例不会重复探测同一账号。
- 单次探测超时 90 秒；响应体读取上限 1 MiB。

## 6. 探测请求与异常判定

请求要求模型“只回答单词 pong，不要调用任何工具”，同时附带一个带随机名称（`qp_canary_xxxxxxxx`）的诱饵工具。异常提示（`anomalies`）：

| 值 | 含义 |
| --- | --- |
| `unexpected_tool_call` | 模型调用了诱饵工具（上游或中间层擅自调用工具） |
| `echoed_tool_config` | 回答中出现诱饵工具名或其描述（请求配置被回显 / 泄露） |
| `missing_usage` | 上游未返回输入或输出 token 用量 |
| `input_tokens_mismatch` | 上游上报的输入 token 超出本地估算的容差范围（过大，或小于估算值的一半再除以容差系数） |
| `upstream_model_mismatch` | 上游返回的模型名与请求模型不一致（疑似偷换模型） |
| `empty_output` | 没有任何文本输出且未调用工具 |
| `unexpected_answer` | 有输出但不含 `pong` |

说明：

- 本地输入 token 估算刻意粗略（约 4 字符 / token 加固定的消息与工具框架开销），因此默认容差较宽（200%）。
- Chat Completions 的缓存 token 已包含在 `prompt_tokens` 中；Messages 协议按 输入 + 缓存读 + 缓存写 合计比较。
- 异常是提示而非判决，请结合多次结果判断。

## 7. 指标口径

- **TTFT**：从发出请求到第一个输出增量的毫秒数。
- **输出 TPS**：输出 token 数 ÷（结束时间 − 首字时间），仅在有首字且上游报告了输出 token 时计算，保留 2 位小数。
- **总耗时**：请求开始到流结束。
- 失败时 `error_message` 为 `timeout`、`request_failed`、`http_<状态码>`、`stream_error: ...`、`missing_api_key`、`invalid_base_url` 等简短代码。
- 响应摘录最多 300 字，并会把账号自身的 API Key 从文本中抹除。

## 8. 数据表（迁移 `311_upstream_quality_probes.sql`）

- `upstream_quality_probe_configs`：每账号一行（主键 `account_id`），含启用、模型、间隔、下次 / 上次运行时间与运行租约。
- `upstream_quality_probe_results`：每次探测一行，含触发方式（`scheduled` / `manual`）、协议、模型 / 上游模型、状态、HTTP 状态、TTFT / 总耗时 / TPS、token 用量、估算输入 token、异常数组、响应摘录。

账号删除时级联删除；结果按 `max_results_per_account` 修剪。

## 9. 管理接口

均位于 `/api/v1/admin/upstream-quality-probes`，需要管理员权限：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET / PUT | `/settings` | 读取 / 更新全局设置 |
| GET | `/accounts` | 已配置账号列表（附最近一次结果） |
| GET | `/accounts/:id` | 单账号配置与结果历史，参数 `limit`（默认 100） |
| PUT | `/accounts/:id` | 保存账号配置 |
| DELETE | `/accounts/:id` | 删除账号配置 |
| POST | `/accounts/:id/run` | 立即运行一次（可选请求体 `{"model": "..."}`） |

## 10. 建议

- 只为需要关注质量的中转上游开启，间隔 30–60 分钟足够观察趋势。
- 若某上游持续出现 `upstream_model_mismatch` 或 `input_tokens_mismatch`，可结合「上游中转熔断」「上游变动提醒」一起排查是否被替换模型或倍率调整。
