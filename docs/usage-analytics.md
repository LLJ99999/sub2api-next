[简体中文](usage-analytics.md) | [English](usage-analytics.en.md) | [繁體中文](usage-analytics.zh-TW.md)

# 用量记录与报表：上游成本开关、TPS / 缓存命中率、聚合与毛利

本文说明管理端「使用记录」页（`/admin/usage`）新增的四项能力及其口径：

1. 上游成本显示开关（截图防泄露）
2. TPS 与缓存命中率独立列，支持服务端排序
3. 按上游账号 / 分组 / 模型的聚合分析
4. 毛利报表（收入 − 上游成本）

所有派生指标在后端 `backend/internal/pkg/usagestats/usage_metrics.go` 有唯一定义，
前端 `frontend/src/utils/usageTps.ts`、`frontend/src/utils/usageCacheRate.ts` 与
repository SQL 表达式（`backend/internal/repository/usage_log_metrics_sql.go`）必须保持一致。
修改任何阈值时请同步三处并更新本文。

---

## 1. 上游成本显示开关

- 位置：使用记录页顶部工具栏右上角（时间粒度选择器右侧）的眼睛图标。
- 状态保存在浏览器 `localStorage`，键名 `admin-usage-upstream-cost-visible`（`'1'` 显示 / `'0'` 隐藏），默认显示。
  仅影响当前浏览器，不影响其他管理员；后端接口不受影响（仍返回成本字段）。
- 关闭后**一次性隐藏**以下所有上游成本信息：
  - 明细表格「费用」列下方的 `A $…` 行；
  - 费用提示框中的「账号倍率」与「账号计费」段；
  - 统计卡片中的「账号成本」；
  - Excel 导出中的「账号倍率」「账号计费」两列（导出开始时按当前开关状态决定）；
  - 「分析」页签中的「上游成本」「毛利」「毛利率」三列（若正按这些列排序，自动回落到按收入降序）。
- 观察者模式（用户查看自己的用量）不显示该开关，展示行为保持不变。
- 实现：`frontend/src/composables/useUpstreamCostVisibility.ts`（模块级共享状态）。

> 注意：开关只是展示层隐藏，用于截图场景，不是权限控制。浏览器开发者工具中仍可看到接口返回的成本数据。

## 2. TPS 与缓存命中率

### 2.1 缓存命中率

```
cache_hit_rate = cache_read_tokens / (input_tokens + cache_read_tokens + cache_creation_tokens)
```

`usage_logs` 中三类提示 token 是**互斥桶**：

| 字段 | 含义 |
| --- | --- |
| `input_tokens` | 未命中缓存的普通输入 |
| `cache_read_tokens` | 缓存命中（读取） |
| `cache_creation_tokens` | 缓存写入 |

各协议在**落库时**已归一化，因此同一公式对所有协议都正确：

- **Anthropic**：`input_tokens` 本身不含缓存 token，直接落库。
- **OpenAI**（Chat Completions / Responses）：`prompt_tokens` / `input_tokens` **包含** `cached_tokens`。
  网关在记录用量时减去缓存读写：`input_tokens = 总输入 − cache_read − cache_creation`
  （见 `backend/internal/service/openai_gateway_usage.go`，`actualInputTokens`）。
  例：`prompt_tokens=10000, cached_tokens=8000` → 落库 `input=2000, cache_read=8000` → 命中率 80%。
- **Gemini / Antigravity**：`promptTokenCount` 包含 `cachedContentTokenCount`，转换时已减去
  （`gemini_messages_compat_service.go` 的 `extractGeminiUsage`、`pkg/antigravity/*_transformer.go`）。

分母为 0（无提示 token，例如纯图片请求）时命中率不适用，显示 `-`。
聚合场景（趋势图、分析页）使用 `SUM(read) / SUM(input + read + write)`，即按 token 加权，而非逐行命中率的平均值。

公共开关 `usage_show_cache_rate`（系统设置）关闭时，管理端不提供「缓存率」列。

### 2.2 输出 TPS（生成阶段吞吐）

```
TPS = output_tokens / ((duration_ms − first_token_ms) / 1000)
```

- `first_token_ms` 缺失（非流式请求）时按 0 处理，即以总耗时作为生成窗口。
- 以下情况不计算，显示 `-`，不参与 P50/P95：
  - 输出 token < **20**（`OutputTPSMinOutputTokens`）；
  - `duration_ms` 缺失，或生成窗口 < **100ms**（`OutputTPSMinGenerationMs`，自然包含 ≤ 0）；
  - 图片 / 视频生成请求（`image_count > 0`、`image_output_tokens > 0` 或 `billing_mode = 'video'`）。
- 100ms 下限的原因：部分上游把整段输出缓冲到最后才下发，首字到结束只有 1~10ms，
  直接相除会得到 7 万~11 万 t/s 的虚假尖峰（历史上报过 109500 t/s、73000 t/s）。
  这类记录没有可信的生成速度，因此隐藏而不是给出误导数值。
- 与旧口径的差异：旧版前端用「输出 ÷ 总耗时」（含排队与首字等待），新版扣除首字时间，
  数值通常更高，更接近模型生成速度。输出 token 中包含上游计入 output usage 的推理 token。

### 2.3 独立列与服务端排序

管理端明细表新增两列（可在「列设置」中隐藏）：

| 列 | 排序键 `sort_by` |
| --- | --- |
| TPS | `output_tps` |
| 缓存率 | `cache_hit_rate` |

接口：`GET /api/v1/admin/usage?sort_by=output_tps&sort_order=desc&start_date=…&end_date=…`

- 不适用的记录（NULL）无论升降序都排在最后（`NULLS LAST`），次级排序为 `id`。
- 按表达式排序无法使用索引，因此**必须带 `start_date`**（或 `request_id`），否则返回 400。
  管理端页面始终携带日期范围（默认最近 24 小时）。大范围排序会扫描范围内全部行，请尽量缩小时间范围。
- 原「状态」列中的首字 / 总耗时 / TPS / 缓存率展示保留不变。

## 3. 聚合分析（新接口）

### 3.1 接口

```
GET /api/v1/admin/usage/analytics
```

| 参数 | 说明 |
| --- | --- |
| `dimension` | `account`（默认，上游账号）/ `group`（分组）/ `model`（请求模型） |
| `start_date`, `end_date` | 必填，`YYYY-MM-DD`，按 `timezone` 解析，`end_date` 当天整天计入；跨度 ≤ 93 天 |
| `timezone` | IANA 时区（前端自动附带） |
| `group_id`, `account_id` | 可选过滤 |
| `model` | 可选，按请求模型（`requested_model`，缺省回退 `model`）过滤 |
| `limit` | 返回行数，默认 200，最大 1000；超出时按收入降序截断，`truncated=true`，合计行仍为全量 |

返回 `data`：

```jsonc
{
  "dimension": "account",
  "start_time": "...", "end_time": "...",
  "rows": [ /* UsageAnalyticsRow */ ],
  "totals": { /* 全量合计，同结构，key/name 为空 */ },
  "truncated": false,
  "error_count_available": true
}
```

`UsageAnalyticsRow` 字段：

| 字段 | 口径 |
| --- | --- |
| `key` / `id` / `name` / `platform` | 维度值；账号维度带账号名与平台，分组维度 `key="0"` 表示无分组，模型维度无 `id` |
| `requests` | `usage_logs` 行数（成功计费的请求） |
| `error_count` | `ops_error_logs` 中状态码 ≥ 400 的请求数，复用运维面板按请求去重的口径（排除 count_tokens）；查询失败时为 `null` 且 `error_count_available=false` |
| `input_tokens` / `output_tokens` / `cache_creation_tokens` / `cache_read_tokens` / `total_tokens` | 求和 |
| `cache_hit_rate` | `SUM(read) / SUM(input + read + write)`，无提示 token 为 `null` |
| `first_token_p50_ms` / `first_token_p95_ms` | `percentile_cont` 于 `first_token_ms`（忽略 NULL） |
| `output_tps_p50` / `output_tps_p95` | `percentile_cont` 于 2.2 定义的逐行 TPS（不适用的行不参与） |
| `revenue` | `SUM(actual_cost)`：向用户实际收取的金额 |
| `upstream_cost` | `SUM(COALESCE(account_stats_cost, total_cost) × COALESCE(account_rate_multiplier, 1))` |
| `gross_margin` | `revenue − upstream_cost` |
| `margin_pct` | `gross_margin / revenue`（0~1，可为负）；`revenue ≤ 0` 时为 `null` |

错误数说明：

- 只有错误、没有任何成功用量的维度值不会单独成行（无收入与成本），但计入合计行。
- 账号维度下未分配账号的错误（例如无可用账号）只计入合计。
- 模型维度按 `ops_error_logs.model` 匹配，与 `usage_logs` 的请求模型口径可能存在少量偏差。

### 3.2 SQL 与性能

- 一次扫描：`WITH scoped AS (… WHERE created_at ∈ [start, end) …)`，再
  `GROUP BY GROUPING SETS ((dim_key), ())` 同时得到分组行与合计行；名称在聚合之后 `LEFT JOIN accounts/groups`。
- 时间过滤命中现有索引 `idx_usage_logs_created_at`；带 `account_id` / `group_id` 时可用
  `idx_usage_logs_account_created_at` / `idx_usage_logs_group_created_at_not_null`。
  分位数需要对范围内行排序，无法预聚合，因此限制跨度 ≤ 93 天；**未新增索引迁移**。
- 错误数为第二条查询（`ops_error_logs`，`idx_ops_error_logs_created_at` 等索引），失败不影响主结果。

### 3.3 页面

使用记录页明细卡片新增「分析」页签（仅管理端，首次切换时才加载）：

- 「分组方式」切换上游账号 / 分组 / 模型；
- 跟随页面时间范围以及「分组 / 账号 / 模型」筛选（分析页隐藏不适用的用户、API Key、请求类型等筛选项）；
- 表头点击做客户端排序（默认按收入降序，空值排最后），底部为合计行；
- 上游成本开关关闭时隐藏「上游成本 / 毛利 / 毛利率」列。

## 4. 毛利报表

毛利 = 收入（`actual_cost`，用户实际扣费）− 上游成本，按上游账号 / 分组 / 模型三种维度查看，
即「分析」页签中的「收入 / 上游成本 / 毛利 / 毛利率」列，接口同 3.1。

上游成本口径与现有统计卡片「账号成本」完全一致：

```
upstream_cost = (account_stats_cost ?? total_cost) × (account_rate_multiplier ?? 1.0)
```

- `account_rate_multiplier` 为 NULL（历史数据）按 1.0；
- `account_stats_cost` 是账号统计定价的预计算费用，存在时优先于 `total_cost`；为 NULL 时即
  `total_cost × account_rate_multiplier`。

毛利为负的行以红色显示。上游成本开关关闭时，成本与毛利一并隐藏。

## 5. 相关代码

| 位置 | 内容 |
| --- | --- |
| `backend/internal/pkg/usagestats/usage_metrics.go` | 命中率 / TPS / 上游成本 / 毛利的唯一定义，派生排序键 |
| `backend/internal/pkg/usagestats/usage_analytics.go` | 聚合维度、过滤条件、结果结构 |
| `backend/internal/repository/usage_log_metrics_sql.go` | 对应 SQL 表达式 |
| `backend/internal/repository/usage_log_analytics.go` | 聚合查询与错误数合并 |
| `backend/internal/repository/usage_log_repo_query.go` | `usageLogOrderBy` 派生排序 |
| `backend/internal/handler/admin/usage_analytics_handler.go` | `GET /admin/usage/analytics` |
| `frontend/src/composables/useUpstreamCostVisibility.ts` | 上游成本开关 |
| `frontend/src/components/admin/usage/UsageAnalyticsPanel.vue` | 分析页签 |
| `frontend/src/utils/usageTps.ts`、`usageCacheRate.ts` | 前端口径 |
