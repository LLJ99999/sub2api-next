[简体中文](pelican-scheduled-tests.md) | [English](pelican-scheduled-tests.en.md) | [繁體中文](pelican-scheduled-tests.zh-TW.md)

# 鵜鶘排程測試

完整說明（給使用者展示頁的分組測試、帳號級測試、成本口徑、保留規則）見[簡體中文文件](pelican-scheduled-tests.md)。本頁說明最新新增的功能。

## 題目輪換與模型選擇器

- **分組測試題目輪換**：分組測試除單一 `prompt` 外，可在 `pelican_config.prompts` 填寫多道題（最多 50 道，每道 ≤ 32000 位元組，空行忽略，允許重複以加權）。每次執行（排程或立即執行）依序使用下一道題，同一輪的並行份數使用同一道題；輪換游標 `prompt_rotation` 存於測試計畫表（遷移 `321_pelican_prompt_rotation.sql`），重啟或多實例不會打亂順序。只填一道題時與原本行為一致。每筆結果記錄當次實際使用的題目。
- **從上游拉取模型**：分組測試的模型輸入框旁可「從上游拉取」，彙總該分組啟用帳號上游 `/v1/models` 的模型（`POST /api/v1/admin/accounts/models/sync-upstream-by-group`，同一 Base URL 只請求一次）；帳號級排程測試（含一般連線測試計畫）同樣可從該帳號上游拉取模型，拉到的模型即使不在靜態清單中也可選取。
