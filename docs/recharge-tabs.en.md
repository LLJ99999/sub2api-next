[简体中文](recharge-tabs.md) | [English](recharge-tabs.en.md) | [繁體中文](recharge-tabs.zh-TW.md)

# Recharge Page Tabs (incl. Binance Pay)

The user recharge page (`/purchase`) can show several admin-configured tabs, e.g. "Top Up", "Binance Pay", "USDC". Each tab has a custom name, a sort order, an enabled switch and one or more bound payment methods; switching tabs shows only that tab's methods.

## 1. Default behavior

With no tabs configured (or all removed) the page is unchanged: a single "Top Up" (充值) tab listing every available method. The default tab label is never changed.

## 2. Configuration

Admin → System Settings → Payment → "Recharge Page Tabs" card:

- **Add Tab**: the first tab is pre-filled as 「充值」 with every method the providers offer (the default page made explicit); later tabs need a name and methods.
- **Name**: 1–32 characters, shown to users as-is.
- **Order**: ↑ / ↓; list order is display order.
- **Enabled**: disabled tabs are hidden; at least one tab must be enabled.
- **Bound payment methods**: tick the user-facing methods offered by provider instances (`alipay`, `wxpay`, `stripe`, `airwallex` and EasyPay `customMethods` `type`s such as `binance`). Other keys can be typed in. An enabled tab must bind at least one method.
- Up to 10 tabs; click "Save Settings" afterwards.

Stored in the `settings` key `RECHARGE_TABS` (JSON array, strictly validated server-side) and delivered as `recharge_tabs` in `GET /api/v1/payment/checkout-info` (enabled tabs only, sorted). The admin settings field is `payment_recharge_tabs`:

```json
[
  {"id": "recharge", "name": "充值", "sort_order": 1, "enabled": true, "methods": ["alipay", "wxpay"]},
  {"id": "binance", "name": "Binance Pay", "sort_order": 2, "enabled": true, "methods": ["binance"]}
]
```

- `id`: 1–32 lowercase letters, digits, `_`, `-`, unique; usable as a deep link `/purchase?tab=<id>`.
- Tabs only decide which methods are shown; order creation, limits, fees and recharge bonuses are unchanged. The subscription tab still lists every method.
- If none of a tab's methods is currently available the tab shows "no payment method available".
- Methods whose `type` or EasyPay `upstreamType` contains `binance` get the Binance icon.

## 3. Binance Pay through EasyPay (BinancePayTool)

No new gateway code is needed: BinancePayTool ships an EasyPay-compatible layer, so sub2api's existing **EasyPay** provider is used.

**Gateway (BinancePayTool `config.env`)**: set `BASE_URL` (reachable by sub2api and users), `EPAY_PID`, `EPAY_KEY` (from `./bpaygate -gen-key`), `EPAY_RATE` (`1` when sub2api prices in USD; the USDT rate such as `7.2` when it prices in CNY) and `EPAY_CURRENCY=USDT`. Bind the gateway to 127.0.0.1 behind Caddy/Nginx with `TRUST_PROXY=true`.

**sub2api admin**: Payment → Providers → add an EasyPay instance:

| Field | Value |
| --- | --- |
| PID | same as `EPAY_PID` |
| PKey | same as `EPAY_KEY` |
| API base (apiBase) | gateway `BASE_URL`, e.g. `https://pay.example.com` |
| Payment mode | `popup` recommended (redirect to the gateway checkout); in `qrcode` mode the QR encodes the checkout URL |
| customMethods | `[{"type":"binance","upstreamType":"binance","displayName":"Binance Pay USDT"}]` |
| Supported types | tick `binance` |

`type` may only contain lowercase letters, digits, `_`, `-` and must not start with `alipay` / `wxpay`. After saving the instance, add a "Binance Pay" tab bound to `binance` (or add it to an existing tab) and click "Save Settings".

**Order timeout**: gateway orders live `ORDER_TTL=900` s and payments within `EXPIRED_GRACE=1800` s after expiry are still confirmed. Set sub2api's order timeout to at least `ORDER_TTL + EXPIRED_GRACE` (≥ 45 minutes with defaults); otherwise sub2api may expire the order first and rely on order queries to recover it.

Notes: `money` in gateway notifications and queries is always the amount sub2api submitted; re-submitting the same `out_trade_no` returns the same checkout; refunds are not supported by the gateway and must be sent back manually in the Binance app.
