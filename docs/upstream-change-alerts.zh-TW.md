[简体中文](upstream-change-alerts.md) | [English](upstream-change-alerts.en.md) | [繁體中文](upstream-change-alerts.zh-TW.md)

# 上游倍率 / 餘額變動提醒

本功能把「上游計費探測」已經讀到的上游分組倍率和錢包餘額持久化成歷史，識別倍率變化與餘額跌破閾值的時刻，並可透過帳號維運（Account Ops）已設定的通知渠道發出提醒。

> 它**不會**額外請求上游：只觀察上游計費探測每次寫入的快照。所有開關預設關閉。

## 1. 前提

- 帳號為 API Key 帳號，並已在「編輯帳號」中開啟「上游計費自動探測」（倍率 / 餘額資料來自探測快照）。
- 若需要推送通知，需在「帳號維運」中設定至少一個通知渠道；否則事件會被記錄為 `failed`。

## 2. 入口

後台側邊欄「上游變動」（`/admin/upstream-changes`）：

- **設定**：歷史記錄、倍率提醒、餘額提醒、靜音帳號；
- **事件**：倍率變化與餘額閾值穿越的列表，可按站點、型別、方向、帳號篩選；
- **快照**：單帳號最近 N 天的倍率 / 餘額曲線資料。

## 3. 設定項（`settings` 表鍵 `upstream_change_alert_settings`）

| 欄位 | 預設 | 說明 |
| --- | --- | --- |
| `record_history` | false | 每次成功探測寫一條快照，並記錄所有變化事件 |
| `retention_days` | 30 | 快照 / 事件保留天數（1–365），每小時最多清理一次 |
| `rate_alert_enabled` | false | 倍率變化提醒 |
| `rate_alert_direction` | `both` | `both` / `up` / `down`，按合併視窗內的**淨變化**判斷 |
| `rate_min_change_percent` | 0 | 相對變化小於該百分比時不提醒（事件仍記錄），0–1000 |
| `merge_window_minutes` | 10 | 同一帳號連續變化合併為一條通知的視窗（0–1440） |
| `cooldown_minutes` | 60 | 同一帳號兩次倍率通知的最小間隔（0–10080） |
| `balance_alert_enabled` | false | 餘額跌破閾值提醒 |
| `balance_threshold` | 5 | 全域性餘額閾值（≥0），僅對沒有單獨餘額規則的帳號生效 |
| `balance_unit` | `USD` | 只有快照單位與此一致時才比較閾值 |
| `balance_cooldown_minutes` | 360 | 同一帳號兩次餘額提醒的最小間隔（5–10080） |
| `muted_account_ids` | [] | 靜音帳號（最多 1000 個），事件記錄為 `muted` |

只要「歷史記錄 / 倍率提醒 / 餘額提醒」任一開啟即生效；三者都關閉時完全不寫庫。

## 4. 判定規則

### 4.1 倍率變化

- 比較的是**實際計費倍率**：使用者覆蓋倍率優先，否則上游分組倍率；不含分時段峰值倍率。
- 新舊倍率都存在、本次探測的倍率是新鮮值、且差值超過 1e-9 時產生事件，方向為 `up` / `down`。
- 通知在後台週期內統一發送（見 4.3），同一帳號在合併視窗內的多次變化合併成一條，訊息展示“首箇舊值 → 最新值”及變化次數；合併的舊事件標記為 `merged`。
- 淨變化為 0、方向不匹配、相對變化低於閾值時分別記為 `suppressed` / `filtered`。

### 4.2 餘額跌破閾值

- 餘額由高於閾值變為 ≤ 閾值時記錄 `low` 事件；由 ≤ 閾值回到閾值之上記錄 `recovered` 事件（`recovered` 只記錄不推送）。
- 帳號維運中已為該帳號設定單獨餘額規則時，本功能不重複提醒（`suppressed`），以帳號維運的規則為準。
- 冷卻期內的再次跌破記為 `suppressed`。

### 4.3 通知傳送

- 上游計費探測服務每個週期先執行一次待發送事件的重新整理，使用 leader 鎖（`upstream:change:flush:leader`）保證多例項只有一個例項傳送。
- 通知透過帳號維運的廣播介面傳送到其已設定的渠道；沒有可用渠道或傳送失敗記為 `failed`。

### 4.4 事件狀態 `notify_state`

| 狀態 | 含義 |
| --- | --- |
| `none` | 只記錄，不需要通知（提醒開關關閉或 `recovered`） |
| `pending` | 等待下一次重新整理 |
| `sent` / `failed` | 已傳送 / 傳送失敗 |
| `muted` | 帳號被靜音 |
| `suppressed` | 被冷卻、單獨規則或淨變化為 0 抑制 |
| `filtered` | 方向或最小變化百分比不滿足 |
| `merged` | 合併到同窗口內最新一條事件的通知中 |

## 5. 資料表（遷移 `310_upstream_change_alerts.sql`）

- `upstream_rate_snapshots`：帳號、站點、提供方、上游分組、分組倍率 / 解析倍率 / 實際倍率、餘額與單位、觀察時間。
- `upstream_change_events`：帳號、帳號名、站點、型別（`rate_multiplier` / `balance_low`）、方向、舊值 / 新值 / 閾值、單位、通知狀態與時間。

站點鍵由帳號 `base_url` 歸一化得到（`scheme://host[:port][/path]`，去掉憑證、查詢串與結尾的 `/v1`）。表中**不儲存**任何憑證、金鑰或令牌。帳號刪除時級聯刪除。

## 6. 管理介面

均位於 `/api/v1/admin`，需要管理員許可權：

| 方法 | 路徑 | 說明 |
| --- | --- | --- |
| GET / PUT | `/upstream-changes/settings` | 讀取 / 更新設定 |
| GET | `/upstream-changes/events` | 事件分頁，引數 `site`、`kind`、`direction`、`account_id`、`page`、`page_size`（預設 50） |
| GET | `/upstream-changes/snapshots` | 單帳號快照，引數 `account_id`（必填）、`days`（預設 7） |
| GET | `/upstream-changes/sites` | 已出現過的站點列表 |

## 7. 注意事項

- 本功能依賴探測快照的新鮮度：探測失敗或未開啟時不會產生事件。
- 首次開啟時沒有“上一次快照”，不會產生倍率事件；第二次成功探測後開始比較。
- 若同時使用帳號維運的餘額規則，建議只保留一種提醒方式，避免理解混亂（本功能已自動去重）。
