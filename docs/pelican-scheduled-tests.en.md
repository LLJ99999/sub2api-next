[简体中文](pelican-scheduled-tests.md) | [English](pelican-scheduled-tests.en.md) | [繁體中文](pelican-scheduled-tests.zh-TW.md)

# Pelican Scheduled Tests

The full reference (group tests for the user showcase, account-level tests, cost accounting, retention) is in the [Simplified Chinese document](pelican-scheduled-tests.md). This page covers the latest additions.

## Prompt rotation and model picker

- **Group test prompt rotation**: besides the single `prompt`, a group test may list several prompts in `pelican_config.prompts` (up to 50, each ≤ 32000 bytes, blank lines ignored, duplicates allowed for weighting). Each run (scheduled or "Run now") uses the next prompt in order, and all parallel copies of one run share it. The rotation cursor `prompt_rotation` lives on the plan table (migration `321_pelican_prompt_rotation.sql`), so restarts and multiple replicas keep the order. A single prompt behaves exactly as before. Each result records the prompt actually used.
- **Fetch models from upstream**: the model field of a group test has "Fetch from upstream", which merges the models from `/v1/models` of the group's active accounts (`POST /api/v1/admin/accounts/models/sync-upstream-by-group`, one request per base URL). Account-level scheduled tests (including plain connectivity plans) can fetch the account's own upstream models the same way; a fetched model can be selected even when it is not in the static list.
