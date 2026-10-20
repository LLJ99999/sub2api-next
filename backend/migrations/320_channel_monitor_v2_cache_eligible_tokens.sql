-- Channel monitor V2: cache-rate denominator excludes zero-cache requests.
--
-- Probe / distillation traffic that never reads cache (cache_read_tokens = 0)
-- used to inflate the cache-rate denominator and drag the monitor cache rate
-- down. These columns hold the input / cache-creation tokens of successful
-- requests that had cache_read_tokens > 0, and are used only as the cache-rate
-- denominator. input_tokens / cache_creation_tokens stay complete, so Token
-- and TPM still count every request.
--
-- NULL means "row written before this migration": readers fall back to
-- input_tokens / cache_creation_tokens (old behaviour) for that row. Run
-- docs/sql/channel_monitor_v2_recompute_cache.sql to backfill a window.
ALTER TABLE channel_monitor_v2_metrics_1m
    ADD COLUMN IF NOT EXISTS cache_eligible_input_tokens BIGINT,
    ADD COLUMN IF NOT EXISTS cache_eligible_creation_tokens BIGINT;
ALTER TABLE channel_monitor_v2_user_metrics_1m
    ADD COLUMN IF NOT EXISTS cache_eligible_input_tokens BIGINT,
    ADD COLUMN IF NOT EXISTS cache_eligible_creation_tokens BIGINT;
ALTER TABLE channel_monitor_v2_metrics_rollup
    ADD COLUMN IF NOT EXISTS cache_eligible_input_tokens BIGINT,
    ADD COLUMN IF NOT EXISTS cache_eligible_creation_tokens BIGINT;
ALTER TABLE channel_monitor_v2_user_metrics_rollup
    ADD COLUMN IF NOT EXISTS cache_eligible_input_tokens BIGINT,
    ADD COLUMN IF NOT EXISTS cache_eligible_creation_tokens BIGINT;
