[简体中文](upstream-quality-probes.md) | [English](upstream-quality-probes.en.md) | [繁體中文](upstream-quality-probes.zh-TW.md)

# 上游品質探測

上游品質探測針對單個 API Key 帳號，直接向其上游傳送一個很小的流式請求，記錄現有工具看不到的品質指標：首字時間（TTFT）、輸出 TPS、上游回傳的輸入 / 輸出 / 快取 token、上游實際模型名，以及異常提示。

## 1. 與現有工具的關係

| 工具 | 粒度 | 記錄內容 |
| --- | --- | --- |
| 渠道監控 V2 | 平台 / 分組 / 模型（真實流量聚合，非單帳號） | 可用率、延遲、TTFT、快取命中率 |
| 定時測試 / Pelican 測試 | 單帳號（走帳號測試鏈路） | 總耗時與回答文本 |
| **上游品質探測** | 單帳號（直連上游） | TTFT、輸出 TPS、上游 token 用量、上游模型名、異常提示 |

品質探測是補充，不替代上述工具。

## 2. 成本與預設值

- 每次探測都會**消耗上游額度**（輸出上限 64 token），因此：
  - 全域性開關預設關閉（`settings` 表鍵 `upstream_quality_probe_settings`）；
  - 每個帳號需要單獨新增探測設定（顯式 opt-in）。
- 手動「立即執行」不受全域性開關限制。

## 3. 適用帳號

- 狀態為啟用的 **API Key** 帳號；
- Anthropic 平台使用 Messages 協議（`/v1/messages`），其他 OpenAI 相容平台使用 Chat Completions（`/v1/chat/completions`）；
- Gemini、Antigravity 不支援。
- 請求經由帳號測試同一套 HTTP 上游通道傳送（遵循帳號代理與 TLS 設定），關閉重定向，未設定 `base_url` 時 OpenAI / Anthropic 使用官方地址；`base_url` 需透過與帳號測試相同的地址校驗。
- 模型名會先按帳號的模型對映轉換成上游模型。

## 4. 入口與設定

後台側邊欄「上游品質探測」（`/admin/upstream-quality-probes`）。

### 4.1 全域性設定

| 欄位 | 預設 | 說明 |
| --- | --- | --- |
| `enabled` | false | 定時探測總開關 |
| `max_results_per_account` | 100 | 每個帳號保留的結果條數（10–1000） |
| `input_token_tolerance_pct` | 200 | 輸入 token 與本地估算的容差百分比（50–10000） |

### 4.2 帳號設定

| 欄位 | 說明 |
| --- | --- |
| `enabled` | 是否參與定時探測 |
| `model` | 探測模型（單個模型名，最多 128 字元，不能含空白） |
| `interval_minutes` | 探測間隔，5–1440 分鐘，預設 60 |

儲存設定後，下一次排程週期立即執行，不等待舊計劃。

## 5. 排程

- 掛在定時測試執行器的同一個 tick 上，與渠道監控 Candy、Pelican 分組測試並行執行。
- 每個 tick 最多領取 10 個到期帳號，4 個並行 worker；領取使用 `FOR UPDATE SKIP LOCKED` 並租約 5 分鐘，多例項不會重複探測同一帳號。
- 單次探測超時 90 秒；響應體讀取上限 1 MiB。

## 6. 探測請求與異常判定

請求要求模型“只回答單詞 pong，不要呼叫任何工具”，同時附帶一個帶隨機名稱（`qp_canary_xxxxxxxx`）的誘餌工具。異常提示（`anomalies`）：

| 值 | 含義 |
| --- | --- |
| `unexpected_tool_call` | 模型呼叫了誘餌工具（上游或中間層擅自呼叫工具） |
| `echoed_tool_config` | 回答中出現誘餌工具名或其描述（請求設定被回顯 / 洩露） |
| `missing_usage` | 上游未回傳輸入或輸出 token 用量 |
| `input_tokens_mismatch` | 上游上報的輸入 token 超出本地估算的容差範圍（過大，或小於估算值的一半再除以容差係數） |
| `upstream_model_mismatch` | 上游回傳的模型名與請求模型不一致（疑似偷換模型） |
| `empty_output` | 沒有任何文本輸出且未呼叫工具 |
| `unexpected_answer` | 有輸出但不含 `pong` |

說明：

- 本地輸入 token 估算刻意粗略（約 4 字元 / token 加固定的訊息與工具框架開銷），因此預設容差較寬（200%）。
- Chat Completions 的快取 token 已包含在 `prompt_tokens` 中；Messages 協議按 輸入 + 快取讀 + 快取寫 合計比較。
- 異常是提示而非判決，請結合多次結果判斷。

## 7. 指標計算方式

- **TTFT**：從發出請求到第一個輸出增量的毫秒數。
- **輸出 TPS**：輸出 token 數 ÷（結束時間 − 首字時間），僅在有首字且上游報告了輸出 token 時計算，保留 2 位小數。
- **總耗時**：請求開始到流結束。
- 失敗時 `error_message` 為 `timeout`、`request_failed`、`http_<狀態碼>`、`stream_error: ...`、`missing_api_key`、`invalid_base_url` 等簡短程式碼。
- 響應摘錄最多 300 字，並會把帳號自身的 API Key 從文本中抹除。

## 8. 資料表（遷移 `311_upstream_quality_probes.sql`）

- `upstream_quality_probe_configs`：每帳號一行（主鍵 `account_id`），含啟用、模型、間隔、下次 / 上次執行時間與執行租約。
- `upstream_quality_probe_results`：每次探測一行，含觸發方式（`scheduled` / `manual`）、協議、模型 / 上游模型、狀態、HTTP 狀態、TTFT / 總耗時 / TPS、token 用量、估算輸入 token、異常陣列、響應摘錄。

帳號刪除時級聯刪除；結果按 `max_results_per_account` 修剪。

## 9. 管理介面

均位於 `/api/v1/admin/upstream-quality-probes`，需要管理員許可權：

| 方法 | 路徑 | 說明 |
| --- | --- | --- |
| GET / PUT | `/settings` | 讀取 / 更新全域性設定 |
| GET | `/accounts` | 已設定帳號列表（附最近一次結果） |
| GET | `/accounts/:id` | 單帳號設定與結果歷史，引數 `limit`（預設 100） |
| PUT | `/accounts/:id` | 儲存帳號設定 |
| DELETE | `/accounts/:id` | 刪除帳號設定 |
| POST | `/accounts/:id/run` | 立即執行一次（可選請求體 `{"model": "..."}`） |

## 10. 建議

- 只為需要關注品質的中轉上游開啟，間隔 30–60 分鐘足夠觀察趨勢。
- 若某上游持續出現 `upstream_model_mismatch` 或 `input_tokens_mismatch`，可結合「上游中轉熔斷」「上游變動提醒」一起排查是否被替換模型或倍率調整。

## 11. 自訂題目輪換、模型選擇器與批次開啟

- **題目輪換**：帳號設定可填寫 `prompts`（最多 20 條，每條 ≤ 8000 字元，空行忽略）。設定後每次探測（排程與「立即執行」）依序輪換使用下一條，游標 `prompt_rotation` 持久化於資料庫（遷移 `322_upstream_quality_probe_prompts.sql`），重啟或多實例不會打亂順序。使用自訂題目時不再檢查回答是否含 `pong`（不產生 `unexpected_answer`），輸出上限放寬至 512 token；誘餌工具仍保留，`unexpected_tool_call` / `echoed_tool_config` 等檢查照常。`prompts` 為空時維持原本的 ping/pong 探測。儲存設定時省略 `prompts` 欄位會保留既有題目（批次開關不會清空）。
- **從上游拉取模型**：模型輸入框旁的「從上游拉取」按鈕會即時請求該帳號上游的 `/v1/models`，清單可搜尋，點選即填入。同一個選擇器也用於鵜鶘測智、通道監控、分組模型白名單、帳號測試與排程測試。依分組設定的頁面呼叫 `POST /api/v1/admin/accounts/models/sync-upstream-by-group`（`{"group_id": N}`），彙總該分組中啟用帳號的模型（同一 Base URL 只請求一次，最多 8 個上游），失敗帳號只回傳警告，不暴露金鑰。
- **批次開啟**：帳號管理勾選多個帳號後，批次操作列「上游品質探測」可一次開啟 / 關閉探測，並可統一指定模型與間隔（5–1440 分鐘，模型留空則沿用各帳號既有設定）。開啟時同一上游 Base URL 只開啟第一個帳號，其餘標記為「已略過（上游重複）」並註明首個帳號；關閉對全部選取帳號生效。每列顯示結果（已開啟 / 已關閉 / 已略過 / 失敗）。介面：`POST /api/v1/admin/upstream-quality-probes/accounts/batch`，請求體 `{"account_ids": [...], "enabled": true, "model": "...", "interval_minutes": 60}`，單次 1–500 個帳號。
