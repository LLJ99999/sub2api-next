[简体中文](public-transit-snapshot.md) | [English](public-transit-snapshot.en.md) | [繁體中文](public-transit-snapshot.zh-TW.md)

# Public Transit Snapshot (AI Transit Snapshot)

The public transit snapshot publishes, in anonymized and aggregated form, the public groups, models and prices, cumulative cache hit rate, and availability offered by this site, so that directory sites, price-comparison sites, or other relay stations can read them automatically. It is disabled by default.

## 1. Privacy boundary

The snapshot is built from only two sources of data:

1. The anonymously visible data of the **Model Marketplace**: the same rules as the Model Marketplace apply, so **exclusive groups (exclusive) never appear**;
2. Aggregated metrics from **Channel Monitoring V2** (availability, latency, time to first token, cache hit rate).

The snapshot **does not include**: upstream accounts, keys, tokens, proxy configuration, internal IDs (group IDs are used only inside the service to associate availability and are never output), or any user data.

## 2. Enabling it

Use the "Public Transit Snapshot" card on the admin "System Settings → Features" page (`settings` table key `public_transit_settings`):

| Field | Default | Description |
| --- | --- | --- |
| `enabled` | false | Enables the discovery document and the snapshot endpoint |
| `page_enabled` | false | Enables the public display page `/public/transit`; requires `enabled` to be on first |
| `site_name` | empty | Optional public site name (up to 80 characters; omitted from output when empty) |
| `availability_range` | `24h` | Availability statistics window: `24h` / `7d` / `30d` |

If the settings fail to load or contain invalid content, the feature is treated as disabled (fail closed). Changing the settings clears the snapshot cache immediately; runtime settings are cached for 10 seconds so that anonymous traffic does not hit the settings table too often.

## 3. Public endpoints

Neither endpoint lives under `/api/v1` (the paths are a cross-site convention). Both use the shared per-IP rate limit for public panels. When the feature is disabled they return `404 {"error":"not_found"}`; when building fails they return `503 {"error":"snapshot_unavailable"}`. Responses are plain JSON (without the unified response wrapper) and carry `Access-Control-Allow-Origin: *`.

### 3.1 Discovery document

`GET /.well-known/ai-transit.json` (`Cache-Control: public, max-age=300`)

```json
{
  "schema": "ai-transit/v1",
  "snapshot_url": "/api/public/transit/v1/snapshot",
  "page_url": "/public/transit",
  "site_name": "Example Relay",
  "cache_ttl_seconds": 60
}
```

`page_url` is output only when the public page is enabled. The embedded frontend lets this path through, so it is not swallowed by the SPA router.

### 3.2 Snapshot

`GET /api/public/transit/v1/snapshot` (`Cache-Control: public, max-age=60`)

The server caches the snapshot for 60 seconds; concurrent requests are merged into a single build via singleflight, and each build times out after 20 seconds.

```json
{
  "schema": "ai-transit/v1",
  "generated_at": "Tue 2026-10-20 4:00 PM CST (UTC+08:00)",
  "site_name": "Example Relay",
  "price_unit": "USD per 1M tokens (per_request_price: USD per request)",
  "recharge_multiplier": 1,
  "groups": [
    {
      "name": "default",
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
    { "name": "gpt-4o", "group": "default", "platform": "openai", "billing_mode": "token",
      "input_price": 2.5, "output_price": 10, "cache_read_price": 1.25 }
  ],
  "cache": { "range": "30d", "hit_rate": 0.42 },
  "availability": { "range": "24h", "status": "healthy", "uptime": 0.997, "latency_p50_ms": 1300, "ttft_p50_ms": 650, "sample_count": 99999 }
}
```

Field notes:

- `price_unit`: token prices are in USD per million tokens; the per-request `per_request_price` is in USD per request. Prices follow the same basis as the Model Marketplace's displayed pricing (composed on an amount-actually-charged basis); group multipliers and peak multipliers are in `groups[]`. Values are rounded to 6 decimal places.
- `recharge_multiplier`: the balance top-up multiplier (the top-up multiplier in System Settings).
- `groups[].availability` / `availability`: come from Channel Monitoring V2. `status` is the monitor's overall health status (`unknown` when there is no data); `uptime` is the success rate (`null` when there are no samples).
- `cache.hit_rate`: cumulative cache hit rate over the last 30 days; `null` when there is no denominator. When channel monitoring is disabled, `cache` / `availability` are `null`.
- Field names are part of the public contract; future changes will only add fields in a backward-compatible way.

## 4. Public display page

`/public/transit` is a frontend page that requires no login. The page first reads the discovery document and renders the snapshot only if `page_url` is present (that is, `page_enabled` is on); otherwise it shows a "not available" notice.

## 5. Admin endpoints

| Method | Path | Description |
| --- | --- | --- |
| GET / PUT | `/api/v1/admin/public-transit/settings` | Read / update settings (admin) |

## 6. Operational recommendations

- Before enabling, confirm in the Model Marketplace that every group you intend to publish is non-exclusive and that prices display correctly.
- If you put a CDN in front, it can simply honor the response's `Cache-Control`; a delay of up to 60 seconds for the snapshot plus the CDN cache time is normal.
- To stop publishing, just turn off `enabled`; both endpoints return 404 immediately (after at most the 10-second settings cache).
