[简体中文](usage-analytics.md) | [English](usage-analytics.en.md) | [繁體中文](usage-analytics.zh-TW.md)

# 用量記錄與報表：上游成本開關、TPS / 快取命中率、聚合與毛利

本文說明管理端「使用記錄」頁（`/admin/usage`）新增的四項能力及其計算方式：

1. 上游成本顯示開關（截圖防洩露）
2. TPS 與快取命中率獨立列，支援伺服器端排序
3. 按上游帳號 / 分組 / 模型的聚合分析
4. 毛利報表（收入 − 上游成本）

所有派生指標在後端 `backend/internal/pkg/usagestats/usage_metrics.go` 有唯一定義，
前端 `frontend/src/utils/usageTps.ts`、`frontend/src/utils/usageCacheRate.ts` 與
repository SQL 表示式（`backend/internal/repository/usage_log_metrics_sql.go`）必須保持一致。
修改任何閾值時請同步三處並更新本文。

---

## 1. 上游成本顯示開關

- 位置：使用記錄頁頂部工具欄右上角（時間粒度選擇器右側）的眼睛圖示。
- 狀態儲存在瀏覽器 `localStorage`，鍵名 `admin-usage-upstream-cost-visible`（`'1'` 顯示 / `'0'` 隱藏），預設顯示。
  僅影響當前瀏覽器，不影響其他管理員；後端介面不受影響（仍回傳成本欄位）。
- 關閉後**一次性隱藏**以下所有上游成本資訊：
  - 明細表格「費用」列下方的 `A $…` 行；
  - 費用提示框中的「帳號倍率」與「帳號計費」段；
  - 統計卡片中的「帳號成本」；
  - Excel 匯出中的「帳號倍率」「帳號計費」兩列（匯出開始時按當前開關狀態決定）；
  - 「分析」頁籤中的「上游成本」「毛利」「毛利率」三列（若正按這些列排序，自動回落到按收入降序）。
- 觀察者模式（使用者檢視自己的用量）不顯示該開關，展示行為保持不變。
- 實現：`frontend/src/composables/useUpstreamCostVisibility.ts`（模組級共享狀態）。

> 注意：開關只是展示層隱藏，用於截圖場景，不是許可權控制。瀏覽器開發者工具中仍可看到介面回傳的成本資料。

## 2. TPS 與快取命中率

### 2.1 快取命中率

```
cache_hit_rate = cache_read_tokens / (input_tokens + cache_read_tokens + cache_creation_tokens)
```

`usage_logs` 中三類提示 token 是**互斥桶**：

| 欄位 | 含義 |
| --- | --- |
| `input_tokens` | 未命中快取的普通輸入 |
| `cache_read_tokens` | 快取命中（讀取） |
| `cache_creation_tokens` | 快取寫入 |

各協議在**落庫時**已歸一化，因此同一公式對所有協議都正確：

- **Anthropic**：`input_tokens` 本身不含快取 token，直接落庫。
- **OpenAI**（Chat Completions / Responses）：`prompt_tokens` / `input_tokens` **包含** `cached_tokens`。
  閘道器在記錄用量時減去快取讀寫：`input_tokens = 總輸入 − cache_read − cache_creation`
  （見 `backend/internal/service/openai_gateway_usage.go`，`actualInputTokens`）。
  例：`prompt_tokens=10000, cached_tokens=8000` → 落庫 `input=2000, cache_read=8000` → 命中率 80%。
- **Gemini / Antigravity**：`promptTokenCount` 包含 `cachedContentTokenCount`，轉換時已減去
  （`gemini_messages_compat_service.go` 的 `extractGeminiUsage`、`pkg/antigravity/*_transformer.go`）。

分母為 0（無提示 token，例如純圖片請求）時命中率不適用，顯示 `-`。
聚合場景（趨勢圖、分析頁）使用 `SUM(read) / SUM(input + read + write)`，即按 token 加權，而非逐行命中率的平均值。

公共開關 `usage_show_cache_rate`（系統設定）關閉時，管理端不提供「快取率」列。

### 2.2 輸出 TPS（生成階段吞吐）

```
TPS = output_tokens / ((duration_ms − first_token_ms) / 1000)
```

- `first_token_ms` 缺失（非流式請求）時按 0 處理，即以總耗時作為生成視窗。
- 以下情況不計算，顯示 `-`，不參與 P50/P95：
  - 輸出 token < **20**（`OutputTPSMinOutputTokens`）；
  - `duration_ms` 缺失，或生成視窗 < **100ms**（`OutputTPSMinGenerationMs`，自然包含 ≤ 0）；
  - 圖片 / 影片生成請求（`image_count > 0`、`image_output_tokens > 0` 或 `billing_mode = 'video'`）。
- 100ms 下限的原因：部分上游把整段輸出緩衝到最後才下發，首字到結束只有 1~10ms，
  直接相除會得到 7 萬~11 萬 t/s 的虛假尖峰（歷史上報過 109500 t/s、73000 t/s）。
  這類記錄沒有可信的生成速度，因此隱藏而不是給出誤導數值。
- 與舊計算方式的差異：舊版前端用「輸出 ÷ 總耗時」（含排隊與首字等待），新版扣除首字時間，
  數值通常更高，更接近模型生成速度。輸出 token 中包含上游計入 output usage 的推理 token。

### 2.3 獨立列與伺服器端排序

管理端明細表新增兩列（可在「列設定」中隱藏）：

| 列 | 排序鍵 `sort_by` |
| --- | --- |
| TPS | `output_tps` |
| 快取率 | `cache_hit_rate` |

介面：`GET /api/v1/admin/usage?sort_by=output_tps&sort_order=desc&start_date=…&end_date=…`

- 不適用的記錄（NULL）無論升降序都排在最後（`NULLS LAST`），次級排序為 `id`。
- 按表示式排序無法使用索引，因此**必須帶 `start_date`**（或 `request_id`），否則回傳 400。
  管理端頁面始終攜帶日期範圍（預設最近 24 小時）。大範圍排序會掃描範圍內全部行，請儘量縮小時間範圍。
- 原「狀態」列中的首字 / 總耗時 / TPS / 快取率展示保留不變。

## 3. 聚合分析（新介面）

### 3.1 介面

```
GET /api/v1/admin/usage/analytics
```

| 引數 | 說明 |
| --- | --- |
| `dimension` | `account`（預設，上游帳號）/ `group`（分組）/ `model`（請求模型） |
| `start_date`, `end_date` | 必填，`YYYY-MM-DD`，按 `timezone` 解析，`end_date` 當天整天計入；跨度 ≤ 93 天 |
| `timezone` | IANA 時區（前端自動附帶） |
| `group_id`, `account_id` | 可選過濾 |
| `model` | 可選，按請求模型（`requested_model`，預設回退 `model`）過濾 |
| `limit` | 回傳行數，預設 200，最大 1000；超出時按收入降序截斷，`truncated=true`，合計行仍為全量 |

回傳 `data`：

```jsonc
{
  "dimension": "account",
  "start_time": "...", "end_time": "...",
  "rows": [ /* UsageAnalyticsRow */ ],
  "totals": { /* 全量合計，同結構，key/name 為空 */ },
  "truncated": false,
  "error_count_available": true
}
```

`UsageAnalyticsRow` 欄位：

| 欄位 | 計算方式 |
| --- | --- |
| `key` / `id` / `name` / `platform` | 維度值；帳號維度帶帳號名與平台，分組維度 `key="0"` 表示無分組，模型維度無 `id` |
| `requests` | `usage_logs` 行數（成功計費的請求） |
| `error_count` | `ops_error_logs` 中狀態碼 ≥ 400 的請求數，複用維運面板按請求去重的計算方式（排除 count_tokens）；查詢失敗時為 `null` 且 `error_count_available=false` |
| `input_tokens` / `output_tokens` / `cache_creation_tokens` / `cache_read_tokens` / `total_tokens` | 求和 |
| `cache_hit_rate` | `SUM(read) / SUM(input + read + write)`，無提示 token 為 `null` |
| `first_token_p50_ms` / `first_token_p95_ms` | `percentile_cont` 於 `first_token_ms`（忽略 NULL） |
| `output_tps_p50` / `output_tps_p95` | `percentile_cont` 於 2.2 定義的逐行 TPS（不適用的行不參與） |
| `revenue` | `SUM(actual_cost)`：向用戶實際收取的金額 |
| `upstream_cost` | `SUM(COALESCE(account_stats_cost, total_cost) × COALESCE(account_rate_multiplier, 1))` |
| `gross_margin` | `revenue − upstream_cost` |
| `margin_pct` | `gross_margin / revenue`（0~1，可為負）；`revenue ≤ 0` 時為 `null` |

錯誤數說明：

- 只有錯誤、沒有任何成功用量的維度值不會單獨成行（無收入與成本），但計入合計行。
- 帳號維度下未分配帳號的錯誤（例如無可用帳號）只計入合計。
- 模型維度按 `ops_error_logs.model` 匹配，與 `usage_logs` 的請求模型計算方式可能存在少量偏差。

### 3.2 SQL 與效能

- 一次掃描：`WITH scoped AS (… WHERE created_at ∈ [start, end) …)`，再
  `GROUP BY GROUPING SETS ((dim_key), ())` 同時得到分組行與合計行；名稱在聚合之後 `LEFT JOIN accounts/groups`。
- 時間過濾命中現有索引 `idx_usage_logs_created_at`；帶 `account_id` / `group_id` 時可用
  `idx_usage_logs_account_created_at` / `idx_usage_logs_group_created_at_not_null`。
  分位數需要對範圍內行排序，無法預聚合，因此限制跨度 ≤ 93 天；**未新增索引遷移**。
- 錯誤數為第二條查詢（`ops_error_logs`，`idx_ops_error_logs_created_at` 等索引），失敗不影響主結果。

### 3.3 頁面

使用記錄頁明細卡片新增「分析」頁籤（僅管理端，首次切換時才載入）：

- 「分組方式」切換上游帳號 / 分組 / 模型；
- 跟隨頁面時間範圍以及「分組 / 帳號 / 模型」篩選（分析頁隱藏不適用的使用者、API Key、請求型別等篩選項）；
- 表頭點選做客戶端排序（預設按收入降序，空值排最後），底部為合計行；
- 上游成本開關關閉時隱藏「上游成本 / 毛利 / 毛利率」列。

## 4. 毛利報表

毛利 = 收入（`actual_cost`，使用者實際扣費）− 上游成本，按上游帳號 / 分組 / 模型三種維度檢視，
即「分析」頁籤中的「收入 / 上游成本 / 毛利 / 毛利率」列，介面同 3.1。

上游成本計算方式與現有統計卡片「帳號成本」完全一致：

```
upstream_cost = (account_stats_cost ?? total_cost) × (account_rate_multiplier ?? 1.0)
```

- `account_rate_multiplier` 為 NULL（歷史資料）按 1.0；
- `account_stats_cost` 是帳號統計定價的預計算費用，存在時優先於 `total_cost`；為 NULL 時即
  `total_cost × account_rate_multiplier`。

毛利為負的行以紅色顯示。上游成本開關關閉時，成本與毛利一併隱藏。

## 5. 相關程式碼

| 位置 | 內容 |
| --- | --- |
| `backend/internal/pkg/usagestats/usage_metrics.go` | 命中率 / TPS / 上游成本 / 毛利的唯一定義，派生排序鍵 |
| `backend/internal/pkg/usagestats/usage_analytics.go` | 聚合維度、過濾條件、結果結構 |
| `backend/internal/repository/usage_log_metrics_sql.go` | 對應 SQL 表示式 |
| `backend/internal/repository/usage_log_analytics.go` | 聚合查詢與錯誤數合併 |
| `backend/internal/repository/usage_log_repo_query.go` | `usageLogOrderBy` 派生排序 |
| `backend/internal/handler/admin/usage_analytics_handler.go` | `GET /admin/usage/analytics` |
| `frontend/src/composables/useUpstreamCostVisibility.ts` | 上游成本開關 |
| `frontend/src/components/admin/usage/UsageAnalyticsPanel.vue` | 分析頁籤 |
| `frontend/src/utils/usageTps.ts`、`usageCacheRate.ts` | 前端計算方式 |
