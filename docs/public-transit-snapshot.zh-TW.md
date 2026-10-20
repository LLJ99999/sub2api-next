[简体中文](public-transit-snapshot.md) | [English](public-transit-snapshot.en.md) | [繁體中文](public-transit-snapshot.zh-TW.md)

# 公開中轉快照（AI Transit Snapshot）

公開中轉快照以匿名、聚合的方式對外發布本站提供的公開分組、模型與價格、累計快取命中率和可用性，便於導航站、比價站或其他中轉站自動讀取。預設關閉。

## 1. 隱私邊界

快照只由兩部分資料構建：

1. **模型廣場**的匿名可見資料：與模型廣場同一規則，**專屬分組（exclusive）不會出現**；
2. **渠道監控 V2** 的聚合指標（可用率、延遲、首字時間、快取命中率）。

快照中**不包含**：上游帳號、金鑰、令牌、代理設定、內部 ID（分組 ID 只在服務內部用於關聯可用性，不會輸出）、任何使用者資料。

## 2. 開啟方式

後台「系統設定 → 功能」頁的「公開中轉快照」卡片（`settings` 表鍵 `public_transit_settings`）：

| 欄位 | 預設 | 說明 |
| --- | --- | --- |
| `enabled` | false | 開啟發現文件與快照介面 |
| `page_enabled` | false | 開啟公開展示頁 `/public/transit`；必須先開啟 `enabled` |
| `site_name` | 空 | 可選的公開站點名（最多 80 字，空則不輸出） |
| `availability_range` | `24h` | 可用性統計視窗：`24h` / `7d` / `30d` |

設定讀取失敗或內容非法時按關閉處理（fail closed）。修改設定會立即清空快照快取；執行時設定有 10 秒快取，避免匿名流量頻繁讀設定表。

## 3. 公開介面

兩個介面都不在 `/api/v1` 下（路徑屬於跨站約定），使用公共按 IP 的面板限流；關閉時回傳 `404 {"error":"not_found"}`，構建失敗回傳 `503 {"error":"snapshot_unavailable"}`。響應為純 JSON（無統一響應包裝），並帶 `Access-Control-Allow-Origin: *`。

### 3.1 發現文件

`GET /.well-known/ai-transit.json`（`Cache-Control: public, max-age=300`）

```json
{
  "schema": "ai-transit/v1",
  "snapshot_url": "/api/public/transit/v1/snapshot",
  "page_url": "/public/transit",
  "site_name": "示例中轉",
  "cache_ttl_seconds": 60
}
```

`page_url` 僅在開啟公開頁時輸出。嵌入式前端會放行該路徑，不會被 SPA 路由吞掉。

### 3.2 快照

`GET /api/public/transit/v1/snapshot`（`Cache-Control: public, max-age=60`）

伺服器端快取 60 秒，並行請求透過 singleflight 合併為一次構建，單次構建超時 20 秒。

```json
{
  "schema": "ai-transit/v1",
  "generated_at": "2026-10-20T08:00:00Z",
  "site_name": "示例中轉",
  "price_unit": "USD per 1M tokens (per_request_price: USD per request)",
  "recharge_multiplier": 1,
  "groups": [
    {
      "name": "預設",
      "platform": "openai",
      "subscription_type": "standard",
      "rate_multiplier": 1,
      "peak_rate_enabled": true,
      "peak_start": "20:00",
      "peak_end": "23:00",
      "peak_rate_multiplier": 1.5,
      "model_count": 12,
      "availability": { "range": "24h", "status": "healthy", "uptime": 0.998, "latency_p50_ms": 1200, "ttft_p50_ms": 600, "sample_count": 3456 }
    }
  ],
  "models": [
    { "name": "gpt-4o", "group": "預設", "platform": "openai", "billing_mode": "token",
      "input_price": 2.5, "output_price": 10, "cache_read_price": 1.25 }
  ],
  "cache": { "range": "30d", "hit_rate": 0.42 },
  "availability": { "range": "24h", "status": "healthy", "uptime": 0.997, "latency_p50_ms": 1300, "ttft_p50_ms": 650, "sample_count": 99999 }
}
```

欄位說明：

- `price_unit`：token 價格為每百萬 token 美元；按次計費的 `per_request_price` 為每次請求美元。價格與模型廣場的展示定價計算方式一致（按實收計算方式合成），分組倍率與峰值倍率見 `groups[]`，數值保留 6 位小數。
- `recharge_multiplier`：餘額充值倍率（系統設定中的充值倍率）。
- `groups[].availability` / `availability`：來自渠道監控 V2，`status` 為監控的整體健康狀態（無資料時為 `unknown`），`uptime` 為成功率（無樣本時為 `null`）。
- `cache.hit_rate`：最近 30 天累計快取命中率；無分母時為 `null`。渠道監控關閉時 `cache` / `availability` 為 `null`。
- 欄位名屬於公開契約，後續只做向後相容的新增。

## 4. 公開展示頁

`/public/transit` 是無需登入的前端頁面。頁面先讀取發現文件，只有 `page_url` 存在（即開啟了 `page_enabled`）時才渲染快照，否則顯示未開放提示。

## 5. 管理介面

| 方法 | 路徑 | 說明 |
| --- | --- | --- |
| GET / PUT | `/api/v1/admin/public-transit/settings` | 讀取 / 更新設定（管理員） |

## 6. 維運建議

- 開啟前在模型廣場確認要公開的分組均為非專屬分組，且價格展示正確。
- 若前置 CDN，可直接遵循響應的 `Cache-Control`；快照最長 60 秒 + CDN 快取時間的延遲屬正常。
- 需要停止對外發布時關閉 `enabled` 即可，兩個介面立即（最多 10 秒設定快取後）回傳 404。
