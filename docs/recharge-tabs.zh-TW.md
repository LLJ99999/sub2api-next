[简体中文](recharge-tabs.md) | [English](recharge-tabs.en.md) | [繁體中文](recharge-tabs.zh-TW.md)

# 儲值頁多 Tab（含幣安支付接入）

使用者儲值頁（`/purchase`）可以顯示多個由管理員設定的 Tab，例如「充值」「幣安支付」「USDC」。每個 Tab 有自訂名稱、排序、啟用開關，並綁定一個或多個支付方式；使用者切換 Tab 時只看到該 Tab 綁定的支付方式。

## 1. 預設行為

未設定（或刪除全部 Tab）時，儲值頁維持原樣：只有一個「充值」Tab，顯示全部可用支付方式。預設 Tab 的名稱不會被更動。

## 2. 設定

管理員 → 系統設定 → 支付設定 → 「充值頁 Tab」卡片：

- **新增 Tab**：第一次新增時會預填「充值」並勾選目前服務商能提供的全部方式，等同把預設頁面明確化；之後新增的 Tab 需填寫名稱並勾選方式。
- **名稱**：1–32 個字元，使用者在儲值頁看到的就是此名稱。
- **排序**：以 ↑ / ↓ 調整，清單順序即顯示順序。
- **啟用**：關閉的 Tab 不對使用者顯示；至少要啟用一個 Tab。
- **綁定的支付方式**：勾選服務商實例提供的使用者可見方式（`alipay`、`wxpay`、`stripe`、`airwallex` 以及 EasyPay `customMethods` 中的 `type`，例如 `binance`）。清單外的方式可手動輸入 key 新增。啟用的 Tab 至少綁定一個方式。
- 最多 10 個 Tab，修改後點「儲存設定」。

資料存於 `settings` 表鍵 `RECHARGE_TABS`（JSON 陣列，伺服端嚴格校驗），透過 `GET /api/v1/payment/checkout-info` 的 `recharge_tabs` 欄位下發（僅啟用項、已排序）。管理介面欄位為 `payment_recharge_tabs`：

```json
[
  {"id": "recharge", "name": "充值", "sort_order": 1, "enabled": true, "methods": ["alipay", "wxpay"]},
  {"id": "binance", "name": "幣安支付", "sort_order": 2, "enabled": true, "methods": ["binance"]}
]
```

- `id`：1–32 位小寫字母、數字、`_`、`-`，唯一；可用於直達連結 `/purchase?tab=<id>`。
- Tab 只決定「顯示哪些方式」，不改變下單、限額、手續費、儲值優惠的計算；訂閱 Tab 仍顯示全部方式。
- 綁定的方式目前沒有可用服務商時，該 Tab 顯示「暫無可用支付方式」。
- 支付方式的 `type` 或 EasyPay `upstreamType` 含 `binance` 時顯示幣安圖示。

## 3. 透過 EasyPay 接入幣安支付（BinancePayTool）

幣安支付不需要新的閘道程式碼：BinancePayTool 內建易支付（彩虹易支付協議）相容層，sub2api 使用既有的 **EasyPay** 服務商即可。

**閘道（BinancePayTool `config.env`）**：設定 `BASE_URL`（sub2api 與使用者都能存取的閘道位址）、`EPAY_PID`、`EPAY_KEY`（`./bpaygate -gen-key` 產生）、`EPAY_RATE`（sub2api 以美元計價填 `1`；以人民幣計價填 USDT 匯率，如 `7.2`）、`EPAY_CURRENCY=USDT`。建議閘道只監聽 127.0.0.1，由 Caddy/Nginx 反代並設定 `TRUST_PROXY=true`。

**sub2api 後台**：支付設定 → 服務商管理 → 新增 EasyPay 實例：

| 欄位 | 填寫 |
| --- | --- |
| PID | 與 `EPAY_PID` 相同 |
| PKey | 與 `EPAY_KEY` 相同 |
| API 位址（apiBase） | 閘道 `BASE_URL`，如 `https://pay.example.com` |
| 支付模式 | 建議 `popup`（跳轉閘道收銀頁）；`qrcode` 模式下 QR Code 內容為收銀頁連結 |
| 自訂支付方式（customMethods） | `[{"type":"binance","upstreamType":"binance","displayName":"幣安支付 USDT"}]` |
| 支援的方式 | 勾選 `binance` |

`type` 只能使用小寫字母、數字、`_`、`-`，且不能以 `alipay` / `wxpay` 開頭。儲存實例後，在「充值頁 Tab」新增「幣安支付」Tab 並綁定 `binance`（或直接加入既有 Tab），再點「儲存設定」。

**訂單逾時**：閘道訂單預設 `ORDER_TTL=900` 秒有效，過期後 `EXPIRED_GRACE=1800` 秒內到帳仍會確認。請將 sub2api 的「訂單逾時時間」設為不短於 `ORDER_TTL + EXPIRED_GRACE`（預設即 ≥ 45 分鐘），否則 sub2api 可能先將訂單判為過期，只能依賴查單補單。

其他說明：閘道通知與查單中的 `money` 一律是 sub2api 提交的原始金額；同一 `out_trade_no` 重複下單會回傳同一收銀頁；閘道不支援退款，需在幣安 App 手動轉回。
