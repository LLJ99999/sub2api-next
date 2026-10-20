-- Per-account upstream quality probes (TTFT, output TPS, token usage, anomaly
-- hints). A config row is an explicit admin opt-in; scheduled runs also need
-- the global switch in setting upstream_quality_probe_settings (off by default).
CREATE TABLE IF NOT EXISTS upstream_quality_probe_configs (
    account_id BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    model TEXT NOT NULL,
    interval_minutes INT NOT NULL DEFAULT 60 CHECK (interval_minutes BETWEEN 5 AND 1440),
    next_run_at TIMESTAMPTZ,
    running_until TIMESTAMPTZ,
    last_run_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS upstream_quality_probe_configs_due_idx
    ON upstream_quality_probe_configs (next_run_at) WHERE enabled;

CREATE TABLE IF NOT EXISTS upstream_quality_probe_results (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    trigger TEXT NOT NULL DEFAULT 'scheduled' CHECK (trigger IN ('scheduled', 'manual')),
    protocol TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    upstream_model TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('success', 'failed')),
    http_status INT NOT NULL DEFAULT 0,
    error_message TEXT NOT NULL DEFAULT '',
    ttft_ms BIGINT,
    total_ms BIGINT NOT NULL DEFAULT 0,
    output_tps DOUBLE PRECISION,
    input_tokens BIGINT,
    output_tokens BIGINT,
    cache_read_tokens BIGINT,
    cache_write_tokens BIGINT,
    estimated_input_tokens BIGINT NOT NULL DEFAULT 0,
    anomalies TEXT[] NOT NULL DEFAULT '{}',
    response_excerpt TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS upstream_quality_probe_results_account_idx
    ON upstream_quality_probe_results (account_id, created_at DESC, id DESC);
