-- =============================================================================
-- 渠道监控 V2：按新口径重算「缓存率分母」（cache_eligible_* 列）
-- =============================================================================
--
-- 背景（迁移 320_channel_monitor_v2_cache_eligible_tokens.sql）
--   缓存率 = cache_read ÷ (eligible_input + eligible_creation + cache_read)
--   eligible_* 只统计 cache_read_tokens > 0 的成功请求（actual_cost > 0）的
--   input_tokens / cache_creation_tokens。探测 / 蒸馏等零缓存流量不再拉低缓存率。
--   input_tokens / cache_creation_tokens 本身保持完整，Token / TPM 不受影响。
--
--   迁移前写入的行 eligible_* 为 NULL，读取时回退到旧口径（全部 input），
--   所以不跑本脚本也不会出错，只是历史区间仍是旧口径。
--
-- 本脚本做什么
--   只 UPDATE eligible_* 两列（不删除、不改其它指标列），对 [start, end) 区间：
--     1) channel_monitor_v2_metrics_1m / channel_monitor_v2_user_metrics_1m
--        （1m 表只保留 7 天 / 用户 3 天，更早的行已被清理，UPDATE 不到也无妨）
--     2) channel_monitor_v2_metrics_rollup / channel_monitor_v2_user_metrics_rollup
--        的 5m / 1h / 12h / 1d 四档，直接从 usage_logs 聚合（与 1m 是否已清理无关）。
--        与区间相交的整桶都会按完整桶重算，因此桶边界外的 usage_logs 也会被扫描。
--
-- 用法（不要在业务高峰执行；大区间请分段，比如每次 1 天）：
--   psql "$DATABASE_URL" -v start='2026-10-01 00:00:00+08' -v end='2026-10-02 00:00:00+08' \
--        -f docs/sql/channel_monitor_v2_recompute_cache.sql
--
-- 注意
--   * 表达式必须与 backend/internal/repository/channel_monitor_v2_aggregation.go 一致：
--       platform = lower(usageLogEffectivePlatformExpr)
--       model    = COALESCE(NULLIF(TRIM(requested_model),''), NULLIF(TRIM(model),''), 'unknown')
--       成功过滤 = ul.actual_cost > 0
--     若后端改了这些表达式，请同步修改本脚本。
--   * 桶对齐使用 UTC 原点 TIMESTAMPTZ '1970-01-01 00:00:00+00'，与后端一致。
--   * 后台聚合任务每隔几分钟会重写最近窗口；与其并发执行不会产生错误数据
--     （两边算法相同），但建议避开 RecomputeRange 正在运行的时刻。
--   * 本脚本未在生产库执行过，首次使用请先在副本 / 小区间验证。
-- =============================================================================

\set ON_ERROR_STOP on

BEGIN;

-- 1) 1m 指标表：先把区间内的行置 0（纯错误行没有 usage，也应为 0），再按 usage_logs 回填。
UPDATE channel_monitor_v2_metrics_1m
SET cache_eligible_input_tokens = 0, cache_eligible_creation_tokens = 0
WHERE bucket_start >= :'start'::timestamptz AND bucket_start < :'end'::timestamptz;

UPDATE channel_monitor_v2_metrics_1m m
SET cache_eligible_input_tokens = src.eligible_input,
    cache_eligible_creation_tokens = src.eligible_creation
FROM (
  SELECT date_trunc('minute', ul.created_at) AS bucket_start,
         lower(CASE WHEN g.platform = 'composite' THEN a.platform ELSE COALESCE(NULLIF(g.platform,''), a.platform) END) AS platform,
         COALESCE(ul.group_id, 0) AS group_id,
         COALESCE(NULLIF(TRIM(ul.requested_model), ''), NULLIF(TRIM(ul.model), ''), 'unknown') AS model,
         COALESCE(SUM(ul.input_tokens) FILTER (WHERE COALESCE(ul.cache_read_tokens, 0) > 0 AND ul.actual_cost > 0), 0) AS eligible_input,
         COALESCE(SUM(ul.cache_creation_tokens) FILTER (WHERE COALESCE(ul.cache_read_tokens, 0) > 0 AND ul.actual_cost > 0), 0) AS eligible_creation
  FROM usage_logs ul
  LEFT JOIN groups g ON g.id = ul.group_id
  LEFT JOIN accounts a ON a.id = ul.account_id
  WHERE ul.created_at >= :'start'::timestamptz AND ul.created_at < :'end'::timestamptz
  GROUP BY 1, 2, 3, 4
) src
WHERE m.bucket_start = src.bucket_start AND m.platform = src.platform
  AND m.group_id = src.group_id AND m.model = src.model;

UPDATE channel_monitor_v2_user_metrics_1m
SET cache_eligible_input_tokens = 0, cache_eligible_creation_tokens = 0
WHERE bucket_start >= :'start'::timestamptz AND bucket_start < :'end'::timestamptz;

UPDATE channel_monitor_v2_user_metrics_1m m
SET cache_eligible_input_tokens = src.eligible_input,
    cache_eligible_creation_tokens = src.eligible_creation
FROM (
  SELECT date_trunc('minute', ul.created_at) AS bucket_start,
         lower(CASE WHEN g.platform = 'composite' THEN a.platform ELSE COALESCE(NULLIF(g.platform,''), a.platform) END) AS platform,
         COALESCE(ul.group_id, 0) AS group_id,
         COALESCE(NULLIF(TRIM(ul.requested_model), ''), NULLIF(TRIM(ul.model), ''), 'unknown') AS model,
         ul.user_id,
         COALESCE(SUM(ul.input_tokens) FILTER (WHERE COALESCE(ul.cache_read_tokens, 0) > 0 AND ul.actual_cost > 0), 0) AS eligible_input,
         COALESCE(SUM(ul.cache_creation_tokens) FILTER (WHERE COALESCE(ul.cache_read_tokens, 0) > 0 AND ul.actual_cost > 0), 0) AS eligible_creation
  FROM usage_logs ul
  LEFT JOIN groups g ON g.id = ul.group_id
  LEFT JOIN accounts a ON a.id = ul.account_id
  WHERE ul.created_at >= :'start'::timestamptz AND ul.created_at < :'end'::timestamptz
    AND ul.user_id IS NOT NULL
  GROUP BY 1, 2, 3, 4, 5
) src
WHERE m.bucket_start = src.bucket_start AND m.platform = src.platform AND m.group_id = src.group_id
  AND m.model = src.model AND m.user_id = src.user_id;

-- 2) 固定粒度汇总表（5m / 1h / 12h / 1d）：与 [start,end) 相交的整桶，直接从 usage_logs 聚合。
WITH sizes(sec) AS (VALUES (300), (3600), (43200), (86400)),
bounds AS (
  SELECT sec,
         make_interval(secs => sec) AS step,
         date_bin(make_interval(secs => sec), :'start'::timestamptz, TIMESTAMPTZ '1970-01-01 00:00:00+00') AS start_at,
         date_bin(make_interval(secs => sec), :'end'::timestamptz - INTERVAL '1 microsecond', TIMESTAMPTZ '1970-01-01 00:00:00+00')
           + make_interval(secs => sec) AS end_at
  FROM sizes
),
zeroed AS (
  UPDATE channel_monitor_v2_metrics_rollup r
  SET cache_eligible_input_tokens = 0, cache_eligible_creation_tokens = 0
  FROM bounds b
  WHERE r.bucket_seconds = b.sec AND r.bucket_start >= b.start_at AND r.bucket_start < b.end_at
  RETURNING 1
)
SELECT count(*) AS metrics_rollup_rows_reset FROM zeroed;

WITH sizes(sec) AS (VALUES (300), (3600), (43200), (86400)),
bounds AS (
  SELECT sec,
         make_interval(secs => sec) AS step,
         date_bin(make_interval(secs => sec), :'start'::timestamptz, TIMESTAMPTZ '1970-01-01 00:00:00+00') AS start_at,
         date_bin(make_interval(secs => sec), :'end'::timestamptz - INTERVAL '1 microsecond', TIMESTAMPTZ '1970-01-01 00:00:00+00')
           + make_interval(secs => sec) AS end_at
  FROM sizes
),
src AS (
  SELECT b.sec,
         date_bin(b.step, ul.created_at, TIMESTAMPTZ '1970-01-01 00:00:00+00') AS bucket_start,
         lower(CASE WHEN g.platform = 'composite' THEN a.platform ELSE COALESCE(NULLIF(g.platform,''), a.platform) END) AS platform,
         COALESCE(ul.group_id, 0) AS group_id,
         COALESCE(NULLIF(TRIM(ul.requested_model), ''), NULLIF(TRIM(ul.model), ''), 'unknown') AS model,
         COALESCE(SUM(ul.input_tokens) FILTER (WHERE COALESCE(ul.cache_read_tokens, 0) > 0 AND ul.actual_cost > 0), 0) AS eligible_input,
         COALESCE(SUM(ul.cache_creation_tokens) FILTER (WHERE COALESCE(ul.cache_read_tokens, 0) > 0 AND ul.actual_cost > 0), 0) AS eligible_creation
  FROM bounds b
  JOIN usage_logs ul ON ul.created_at >= b.start_at AND ul.created_at < b.end_at
  LEFT JOIN groups g ON g.id = ul.group_id
  LEFT JOIN accounts a ON a.id = ul.account_id
  GROUP BY 1, 2, 3, 4, 5
)
UPDATE channel_monitor_v2_metrics_rollup r
SET cache_eligible_input_tokens = src.eligible_input,
    cache_eligible_creation_tokens = src.eligible_creation
FROM src
WHERE r.bucket_seconds = src.sec AND r.bucket_start = src.bucket_start AND r.platform = src.platform
  AND r.group_id = src.group_id AND r.model = src.model;

WITH sizes(sec) AS (VALUES (300), (3600), (43200), (86400)),
bounds AS (
  SELECT sec,
         date_bin(make_interval(secs => sec), :'start'::timestamptz, TIMESTAMPTZ '1970-01-01 00:00:00+00') AS start_at,
         date_bin(make_interval(secs => sec), :'end'::timestamptz - INTERVAL '1 microsecond', TIMESTAMPTZ '1970-01-01 00:00:00+00')
           + make_interval(secs => sec) AS end_at
  FROM sizes
),
zeroed AS (
  UPDATE channel_monitor_v2_user_metrics_rollup r
  SET cache_eligible_input_tokens = 0, cache_eligible_creation_tokens = 0
  FROM bounds b
  WHERE r.bucket_seconds = b.sec AND r.bucket_start >= b.start_at AND r.bucket_start < b.end_at
  RETURNING 1
)
SELECT count(*) AS user_rollup_rows_reset FROM zeroed;

WITH sizes(sec) AS (VALUES (300), (3600), (43200), (86400)),
bounds AS (
  SELECT sec,
         make_interval(secs => sec) AS step,
         date_bin(make_interval(secs => sec), :'start'::timestamptz, TIMESTAMPTZ '1970-01-01 00:00:00+00') AS start_at,
         date_bin(make_interval(secs => sec), :'end'::timestamptz - INTERVAL '1 microsecond', TIMESTAMPTZ '1970-01-01 00:00:00+00')
           + make_interval(secs => sec) AS end_at
  FROM sizes
),
src AS (
  SELECT b.sec,
         date_bin(b.step, ul.created_at, TIMESTAMPTZ '1970-01-01 00:00:00+00') AS bucket_start,
         lower(CASE WHEN g.platform = 'composite' THEN a.platform ELSE COALESCE(NULLIF(g.platform,''), a.platform) END) AS platform,
         COALESCE(ul.group_id, 0) AS group_id,
         COALESCE(NULLIF(TRIM(ul.requested_model), ''), NULLIF(TRIM(ul.model), ''), 'unknown') AS model,
         ul.user_id,
         COALESCE(SUM(ul.input_tokens) FILTER (WHERE COALESCE(ul.cache_read_tokens, 0) > 0 AND ul.actual_cost > 0), 0) AS eligible_input,
         COALESCE(SUM(ul.cache_creation_tokens) FILTER (WHERE COALESCE(ul.cache_read_tokens, 0) > 0 AND ul.actual_cost > 0), 0) AS eligible_creation
  FROM bounds b
  JOIN usage_logs ul ON ul.created_at >= b.start_at AND ul.created_at < b.end_at AND ul.user_id IS NOT NULL
  LEFT JOIN groups g ON g.id = ul.group_id
  LEFT JOIN accounts a ON a.id = ul.account_id
  GROUP BY 1, 2, 3, 4, 5, 6
)
UPDATE channel_monitor_v2_user_metrics_rollup r
SET cache_eligible_input_tokens = src.eligible_input,
    cache_eligible_creation_tokens = src.eligible_creation
FROM src
WHERE r.bucket_seconds = src.sec AND r.bucket_start = src.bucket_start AND r.platform = src.platform
  AND r.group_id = src.group_id AND r.model = src.model AND r.user_id = src.user_id;

-- 检查：区间内缓存率（新口径 vs 旧口径），确认后再 COMMIT。
SELECT platform,
       SUM(cache_read_tokens) AS cache_read,
       ROUND(SUM(cache_read_tokens)::numeric / NULLIF(SUM(COALESCE(cache_eligible_input_tokens, input_tokens) + COALESCE(cache_eligible_creation_tokens, cache_creation_tokens) + cache_read_tokens), 0), 4) AS cache_rate_new,
       ROUND(SUM(cache_read_tokens)::numeric / NULLIF(SUM(input_tokens + cache_creation_tokens + cache_read_tokens), 0), 4) AS cache_rate_old
FROM channel_monitor_v2_metrics_rollup
WHERE bucket_seconds = 3600 AND bucket_start >= :'start'::timestamptz AND bucket_start < :'end'::timestamptz
GROUP BY platform
ORDER BY platform;

COMMIT;
