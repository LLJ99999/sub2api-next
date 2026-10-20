[简体中文](upstream-relay-resilience.md) | [English](upstream-relay-resilience.en.md) | [繁體中文](upstream-relay-resilience.zh-TW.md)

# 上游中轉韌性：402 冷卻、中轉熔斷、成功自愈、換號時間預算與全冷卻備援

本文說明 P0 排程韌性改動的行為、設定與維運方式，適用於對接其他中轉站 / NewAPI / Sub2API 的 API Key 帳號，也覆蓋所有平台通用的 402 冷卻與換號預算。

## 1. 總覽

| 能力 | 作用範圍 | 預設 | 設定鍵 |
| --- | --- | --- | --- |
| 402 可恢復冷卻 | 所有帳號（OpenAI `deactivated_workspace` 除外） | 開啟，30 分鐘 | `rate_limit.payment_required_402_cooldown_minutes` |
| 上游中轉熔斷 | 開啟 `extra.upstream_relay_policy` 的 API Key 帳號 | 帳號級手動開啟 | `rate_limit.upstream_relay.*` |
| 半開試探上限 | 熔斷冷卻剛結束的帳號 / 模型 | 固定最多 2 個並行試探 | 無（內建） |
| 成功自愈 | 所有平台的 API Key 帳號 | 開啟 | 無 |
| 換號時間預算 | 所有閘道器入口的 failover 迴圈 | 關閉（0） | `gateway.failover_time_budget_seconds` |
| 全冷卻備援 | API Key 帳號 | 開啟，10 秒一次探測 | `gateway.cooldown_fallback.*` |

## 2. 402 可恢復冷卻

過去上游回傳 HTTP 402 會把帳號永久標記為 `error`，需要人工恢復。現在：

- 402（OpenAI `deactivated_workspace` 仍為永久錯誤）會把帳號置為**臨時不可排程**，時長為 `payment_required_402_cooldown_minutes`（預設 30 分鐘），原因欄位為結構化 JSON（狀態碼 402 + 截斷後的上游訊息）。
- 帳號池模式帳號遇到 402 同樣冷卻，不再走 `pool_mode_error_skipped`；使用者自定義的 `temp_unschedulable_rules` 優先順序更高。
- 上游餘額探測一旦看到正餘額，會提前解除 402 冷卻。
- 帳號維運通知把裸 402 歸類為 `balance_low`。
- 402 冷卻**不會**被全冷卻備援選中（探測無法解決餘額耗盡）。

## 3. 上游中轉熔斷（`upstream_relay_policy`）

### 3.1 開啟方式

在後台「帳號管理 → 編輯帳號」中，API Key 帳號新增「上游中轉熔斷策略」開關，對應 `accounts.extra.upstream_relay_policy=true`。只有 API Key 帳號生效；關閉時寫入 `false`。

### 3.2 判定規則

- 計入失敗：429、所有 5xx、超時 / 傳輸層瞬時錯誤（超時按帳號級計）。
- 不計入：4xx 客戶端 / 引數錯誤；402、401、403 保持各自原有處理；命中 `temp_unschedulable_rules` 或自定義錯誤碼時仍以規則為準。
- 失敗與成功寫入 Redis 滑動視窗（跨例項共享，無 Redis 擴充套件時退化為程序記憶體儲）。
- 視窗內失敗數 ≥ `min_failures` **且** 失敗率 ≥ `failure_rate_percent` 時觸發熔斷。
- 冷卻時長按退避級別遞增：5 秒 → 15 秒 → 60 秒 → 5 分鐘 → 之後翻倍，封頂 `max_cooldown_seconds`；不採用上游回傳的重置時間提示。
- 2 秒內的並行觸發只升一級，避免一次突發把級別連升多級。
- 級別在 `level_reset_seconds` 內沒有新的熔斷則過期，下次從第一級開始。
- 粒度：`scope: model`（預設）只冷卻出錯的模型（寫入 `extra.model_rate_limits`），`scope: account` 冷卻整個帳號（寫入臨時不可排程）。

### 3.3 半開與試探上限

冷卻到期後帳號進入**半開**狀態：帳號快照上仍保留已過期的熔斷原因，直到一次成功把它清除或一次失敗重新熔斷。

- 半開期間，同一帳號（或同一模型作用域）**最多放行 2 個並行試探請求**。計數器存放在 Redis（鍵字首 `upstream_relay_half_open:`，與熔斷視窗同一雜湊標籤），無 Redis 時使用程序內計數。
- 排程選中一個已滿額的半開帳號時，會釋放已佔的並行槽位、把該帳號排除後重新選號（每次請求最多重選 3 次）。
- 試探結果：
  - 成功 → 關閉熔斷、退避級別清零、清除冷卻殘留，計數器重置；
  - 失敗（429/5xx/超時）→ 立即按下一級重新熔斷，計數器重置；
  - 既不成功也不計入熔斷的結果（例如 400）→ 計數器在 `max(window_seconds, 30s)` 後自動過期。
- OpenAI `previous_response_id` 續鏈請求必須命中原帳號，不參與半開重選。
- 儲存出錯時放行（fail open），不會因為 Redis 抖動把帳號鎖死。

### 3.4 Redis 鍵

| 鍵 | 說明 |
| --- | --- |
| `upstream_relay:{<帳號ID>\|<scope>}:fail` / `:all` / `:seq` | 滑動視窗（失敗 / 全部 / 序號） |
| `upstream_relay:{<帳號ID>\|<scope>}:state` | 退避級別與上次熔斷時間 |
| `upstream_relay_half_open:{<帳號ID>\|<scope>}` | 半開試探計數 |
| `cooldown_fallback_probe:<key>` | 全冷卻備援探測鎖 |

其中 `<scope>` 為 `account` 或 `model:<模型>`。

## 4. 成功自愈

API Key 帳號（任意平台）收到成功響應並記賬時：

- 清除殘留的限流 / 過載 / 臨時不可排程狀態，以及本次服務模型在 `model_rate_limits` 中的條目；
- 對開啟熔斷的帳號關閉已觸發的熔斷並重置退避級別；
- 僅在帳號快照上確有殘留時寫庫，健康路徑零開銷；
- 後台「自動停調閾值」觸發的暫停不會被清除。

## 5. 換號時間預算

`gateway.failover_time_budget_seconds`（預設 0 = 不限制）限制單個請求在 failover 上花費的總時間。超出預算後不再切換帳號、不再同號重試，按正常的“重試耗盡”路徑回傳最後一次上游錯誤（保留原始狀態碼）。覆蓋 Anthropic / Gemini / Chat Completions / Responses / SystemOne 及 OpenAI Responses / Chat / WebSocket 迴圈。

建議：客戶端超時較短（如 60 秒）時，把預算設為略小於客戶端超時，避免客戶端已斷開而閘道器仍在換號。

## 6. 全冷卻備援

當選號失敗**僅僅**是因為所有候選 API Key 帳號都在冷卻（限流、過載、臨時不可排程、熔斷、模型冷卻），而不是停用、錯誤、過期、配額耗盡、402 餘額問題或自動停調閾值暫停時：

- 閘道器挑選**最早結束冷卻**的帳號放行一次探測請求（同時結束時取最近使用的帳號），而不是直接回傳 503；
- 同一 分組 / 平台 / 模型 在 `probe_interval_seconds`（預設 10 秒）內只放行一次探測（Redis 跨例項鎖，`SETNX`）；
- 探測成功由成功自愈清除冷卻；失敗按正常錯誤路徑處理；
- 渠道定價限制、路由錯誤等主動拒絕不會觸發備援；
- OpenAI 相容閘道器僅對普通 HTTP 請求備援（無 compact、能力要求、`previous_response_id` 約束，傳輸方式為 Any 或 HTTP/SSE）。

## 7. 後台展示與一鍵恢復

帳號列表的狀態列新增「冷卻 N」徽標，懸停可檢視每條生效中的冷卻：

- 型別（臨時不可排程 / 限流 / 過載 / 模型冷卻）與作用域（整個帳號 / 具體模型）；
- 觸發狀態碼與截斷後的上游訊息（最多 160 字）；
- 剩餘時間；
- 熔斷級別（來自原因 JSON 的 `breaker_level`）。

提示框內「一鍵恢復」呼叫 `POST /api/v1/admin/accounts/:id/recover-state`，清除可恢復狀態後重新整理該行。

後端介面：帳號詳情與列表響應新增 `cooldowns` 陣列（僅在存在生效冷卻時回傳），欄位為 `kind`、`scope`、`model`、`status_code`、`message`、`keyword`、`breaker_level`、`until`、`remaining_seconds`。

## 8. 設定示例

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

- 日誌關鍵字：`upstream_relay_breaker_tripped`、`upstream_relay_breaker_closed`、`upstream_relay_half_open_trial_capped`（debug）、`cooldown_fallback_probe_selected`、`upstream_success_cleared_residual_cooldown`。
- 帳號持續半開：確認試探請求是否回傳了既不成功也不計入熔斷的狀態（如 400），計數器會在視窗時長後過期。
- 全冷卻仍回傳 503：檢查是否存在 402 冷卻、配額耗盡或自動停調閾值暫停，這些情況不參與備援；或同一分組 / 模型剛在探測間隔內放行過一次。
