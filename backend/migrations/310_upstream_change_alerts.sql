-- Upstream multiplier / wallet balance history and change events.
-- Rows are written only after an admin enables history or alerts
-- (setting upstream_change_alert_settings, all switches off by default).
-- Values come from the existing upstream billing probe snapshot; no
-- credentials, keys or tokens are stored here.
CREATE TABLE IF NOT EXISTS upstream_rate_snapshots (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    site TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT '',
    upstream_group TEXT NOT NULL DEFAULT '',
    group_multiplier DOUBLE PRECISION,
    resolved_multiplier DOUBLE PRECISION,
    effective_multiplier DOUBLE PRECISION,
    balance DOUBLE PRECISION,
    balance_unit TEXT NOT NULL DEFAULT '',
    observed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS upstream_rate_snapshots_account_observed_idx
    ON upstream_rate_snapshots (account_id, observed_at DESC);
CREATE INDEX IF NOT EXISTS upstream_rate_snapshots_observed_idx
    ON upstream_rate_snapshots (observed_at);

CREATE TABLE IF NOT EXISTS upstream_change_events (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    account_name TEXT NOT NULL DEFAULT '',
    site TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL CHECK (kind IN ('rate_multiplier', 'balance_low')),
    direction TEXT NOT NULL CHECK (direction IN ('up', 'down', 'low', 'recovered')),
    old_value DOUBLE PRECISION,
    new_value DOUBLE PRECISION,
    threshold DOUBLE PRECISION,
    unit TEXT NOT NULL DEFAULT '',
    notify_state TEXT NOT NULL DEFAULT 'none'
        CHECK (notify_state IN ('none', 'pending', 'sent', 'failed', 'muted', 'suppressed', 'filtered', 'merged')),
    notified_at TIMESTAMPTZ,
    observed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS upstream_change_events_observed_idx
    ON upstream_change_events (observed_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS upstream_change_events_account_idx
    ON upstream_change_events (account_id, kind, observed_at DESC);
CREATE INDEX IF NOT EXISTS upstream_change_events_pending_idx
    ON upstream_change_events (observed_at) WHERE notify_state = 'pending';
