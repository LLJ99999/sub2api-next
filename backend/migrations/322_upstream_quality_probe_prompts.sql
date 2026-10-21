-- Optional custom prompts for upstream quality probes. When set, each run uses the
-- next prompt round-robin (prompt_rotation is the persisted cursor) and the
-- "reply pong" content check is skipped.
ALTER TABLE upstream_quality_probe_configs ADD COLUMN IF NOT EXISTS prompts JSONB NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE upstream_quality_probe_configs ADD COLUMN IF NOT EXISTS prompt_rotation BIGINT NOT NULL DEFAULT 0;
