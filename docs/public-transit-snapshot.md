# 公开中转快照（AI Transit Snapshot）

公开中转快照以匿名、聚合的方式对外发布本站提供的公开分组、模型与价格、累计缓存命中率和可用性，便于导航站、比价站或其他中转站自动读取。默认关闭。

## 1. 隐私边界

快照只由两部分数据构建：

1. **模型广场**的匿名可见数据：与模型广场同一规则，**专属分组（exclusive）不会出现**；
2. **渠道监控 V2** 的聚合指标（可用率、延迟、首字时间、缓存命中率）。

快照中**不包含**：上游账号、密钥、令牌、代理配置、内部 ID（分组 ID 只在服务内部用于关联可用性，不会输出）、任何用户数据。

## 2. 开启方式

后台「系统设置 → 功能」页的「公开中转快照」卡片（`settings` 表键 `public_transit_settings`）：

| 字段 | 默认 | 说明 |
| --- | --- | --- |
| `enabled` | false | 开启发现文档与快照接口 |
| `page_enabled` | false | 开启公开展示页 `/public/transit`；必须先开启 `enabled` |
| `site_name` | 空 | 可选的公开站点名（最多 80 字，空则不输出） |
| `availability_range` | `24h` | 可用性统计窗口：`24h` / `7d` / `30d` |

设置读取失败或内容非法时按关闭处理（fail closed）。修改设置会立即清空快照缓存；运行时设置有 10 秒缓存，避免匿名流量频繁读设置表。

## 3. 公开接口

两个接口都不在 `/api/v1` 下（路径属于跨站约定），使用公共按 IP 的面板限流；关闭时返回 `404 {"error":"not_found"}`，构建失败返回 `503 {"error":"snapshot_unavailable"}`。响应为纯 JSON（无统一响应包装），并带 `Access-Control-Allow-Origin: *`。

### 3.1 发现文档

`GET /.well-known/ai-transit.json`（`Cache-Control: public, max-age=300`）

```json
{
  "schema": "ai-transit/v1",
  "snapshot_url": "/api/public/transit/v1/snapshot",
  "page_url": "/public/transit",
  "site_name": "示例中转",
  "cache_ttl_seconds": 60
}
```

`page_url` 仅在开启公开页时输出。嵌入式前端会放行该路径，不会被 SPA 路由吞掉。

### 3.2 快照

`GET /api/public/transit/v1/snapshot`（`Cache-Control: public, max-age=60`）

服务端缓存 60 秒，并发请求通过 singleflight 合并为一次构建，单次构建超时 20 秒。

```json
{
  "schema": "ai-transit/v1",
  "generated_at": "2026-10-20T08:00:00Z",
  "site_name": "示例中转",
  "price_unit": "USD per 1M tokens (per_request_price: USD per request)",
  "recharge_multiplier": 1,
  "groups": [
    {
      "name": "默认",
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
    { "name": "gpt-4o", "group": "默认", "platform": "openai", "billing_mode": "token",
      "input_price": 2.5, "output_price": 10, "cache_read_price": 1.25 }
  ],
  "cache": { "range": "30d", "hit_rate": 0.42 },
  "availability": { "range": "24h", "status": "healthy", "uptime": 0.997, "latency_p50_ms": 1300, "ttft_p50_ms": 650, "sample_count": 99999 }
}
```

字段说明：

- `price_unit`：token 价格为每百万 token 美元；按次计费的 `per_request_price` 为每次请求美元。价格与模型广场的展示定价口径一致（按实收口径合成），分组倍率与峰值倍率见 `groups[]`，数值保留 6 位小数。
- `recharge_multiplier`：余额充值倍率（系统设置中的充值倍率）。
- `groups[].availability` / `availability`：来自渠道监控 V2，`status` 为监控的整体健康状态（无数据时为 `unknown`），`uptime` 为成功率（无样本时为 `null`）。
- `cache.hit_rate`：最近 30 天累计缓存命中率；无分母时为 `null`。渠道监控关闭时 `cache` / `availability` 为 `null`。
- 字段名属于公开契约，后续只做向后兼容的新增。

## 4. 公开展示页

`/public/transit` 是无需登录的前端页面。页面先读取发现文档，只有 `page_url` 存在（即开启了 `page_enabled`）时才渲染快照，否则显示未开放提示。

## 5. 管理接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET / PUT | `/api/v1/admin/public-transit/settings` | 读取 / 更新设置（管理员） |

## 6. 运维建议

- 开启前在模型广场确认要公开的分组均为非专属分组，且价格展示正确。
- 若前置 CDN，可直接遵循响应的 `Cache-Control`；快照最长 60 秒 + CDN 缓存时间的延迟属正常。
- 需要停止对外发布时关闭 `enabled` 即可，两个接口立即（最多 10 秒设置缓存后）返回 404。
