[简体中文](recharge-tabs.md) | [English](recharge-tabs.en.md) | [繁體中文](recharge-tabs.zh-TW.md)

# 充值页多 Tab（含币安支付接入）

用户充值页（`/purchase`）可以显示多个由管理员配置的 Tab，例如「充值」「币安支付」「USDC」。每个 Tab 有自定义名称、排序、启用开关，并绑定一个或多个支付方式；用户切换 Tab 时只看到该 Tab 绑定的支付方式。

## 1. 默认行为

不配置（或删除全部 Tab）时，充值页保持原样：只有一个「充值」Tab，显示全部可用支付方式。默认 Tab 的名称不会被改动。

## 2. 配置

管理员 → 系统设置 → 支付设置 → 「充值页 Tab」卡片：

- **添加 Tab**：第一次添加时会预填「充值」并勾选当前服务商能提供的全部方式，相当于把默认页面显式化；之后添加的 Tab 需填写名称并勾选方式。
- **名称**：1–32 个字符，用户在充值页看到的就是这个名称。
- **排序**：用 ↑ / ↓ 调整，列表顺序即显示顺序。
- **启用**：关闭的 Tab 不对用户显示；至少要启用一个 Tab。
- **绑定的支付方式**：勾选服务商实例提供的用户可见方式（`alipay`、`wxpay`、`stripe`、`airwallex` 以及 EasyPay `customMethods` 中的 `type`，例如 `binance`）。列表外的方式可手动输入 key 添加。启用的 Tab 至少绑定一个方式。
- 最多 10 个 Tab，修改后点「保存设置」。

数据保存在 `settings` 表键 `RECHARGE_TABS`（JSON 数组，服务端严格校验），通过 `GET /api/v1/payment/checkout-info` 的 `recharge_tabs` 字段下发（仅启用项、已排序）。管理接口字段为 `payment_recharge_tabs`：

```json
[
  {"id": "recharge", "name": "充值", "sort_order": 1, "enabled": true, "methods": ["alipay", "wxpay"]},
  {"id": "binance", "name": "币安支付", "sort_order": 2, "enabled": true, "methods": ["binance"]}
]
```

- `id`：1–32 位小写字母、数字、`_`、`-`，唯一；可用于直达链接 `/purchase?tab=<id>`。
- Tab 只决定「显示哪些方式」，不改变下单、限额、手续费、充值优惠的计算；订阅 Tab 仍显示全部方式。
- 绑定的方式当前没有可用服务商时，该 Tab 显示「暂无可用支付方式」。
- 支付方式的 `type` 或 EasyPay `upstreamType` 含 `binance` 时显示币安图标。

## 3. 通过 EasyPay 接入币安支付（BinancePayTool）

币安支付不需要新的网关代码：BinancePayTool 自带易支付（彩虹易支付协议）兼容层，sub2api 使用现有 **EasyPay** 服务商即可。

**网关（BinancePayTool `config.env`）**：设置 `BASE_URL`（sub2api 与用户都能访问的网关地址）、`EPAY_PID`、`EPAY_KEY`（`./bpaygate -gen-key` 生成）、`EPAY_RATE`（sub2api 按美元计价填 `1`；按人民币计价填 USDT 汇率，如 `7.2`）、`EPAY_CURRENCY=USDT`。建议网关只监听 127.0.0.1，由 Caddy/Nginx 反代并设置 `TRUST_PROXY=true`。

**sub2api 后台**：支付设置 → 服务商管理 → 添加 EasyPay 实例：

| 字段 | 填写 |
| --- | --- |
| PID | 与 `EPAY_PID` 相同 |
| PKey | 与 `EPAY_KEY` 相同 |
| API 地址（apiBase） | 网关 `BASE_URL`，如 `https://pay.example.com` |
| 支付模式 | 推荐 `popup`（跳转网关收银页）；`qrcode` 模式下二维码内容是收银页链接 |
| 自定义支付方式（customMethods） | `[{"type":"binance","upstreamType":"binance","displayName":"币安支付 USDT"}]` |
| 支持的方式 | 勾选 `binance` |

`type` 只能用小写字母、数字、`_`、`-`，且不能以 `alipay` / `wxpay` 开头。保存实例后，在「充值页 Tab」里新建「币安支付」Tab 并绑定 `binance`（或直接加入现有 Tab），再点「保存设置」。

**订单超时**：网关订单默认 `ORDER_TTL=900` 秒有效，过期后 `EXPIRED_GRACE=1800` 秒内到账仍会确认。请把 sub2api 的「订单超时时间」设为不短于 `ORDER_TTL + EXPIRED_GRACE`（默认即 ≥ 45 分钟），否则 sub2api 可能先把订单判为过期，只能依赖查单补单。

其他说明：网关通知与查单中的 `money` 始终是 sub2api 提交的原始金额；同一 `out_trade_no` 重复下单返回同一收银页；网关不支持退款，需要在币安 App 里手动转回。
