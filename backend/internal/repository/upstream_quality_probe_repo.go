package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type upstreamQualityProbeRepository struct{ db *sql.DB }

func NewUpstreamQualityProbeRepository(db *sql.DB) service.UpstreamQualityProbeRepository {
	return &upstreamQualityProbeRepository{db: db}
}

const upstreamQualityConfigColumns = `c.account_id, COALESCE(a.name, ''), COALESCE(a.platform, ''), c.enabled, c.model, c.interval_minutes, c.next_run_at, c.last_run_at, c.updated_at, COALESCE(c.prompts, '[]'::jsonb)`

func scanUpstreamQualityConfig(row interface{ Scan(...any) error }) (service.UpstreamQualityProbeConfig, error) {
	var cfg service.UpstreamQualityProbeConfig
	var next, last sql.NullTime
	var prompts []byte
	err := row.Scan(&cfg.AccountID, &cfg.AccountName, &cfg.Platform, &cfg.Enabled, &cfg.Model, &cfg.IntervalMinutes, &next, &last, &cfg.UpdatedAt, &prompts)
	cfg.Prompts = decodeQualityPrompts(prompts)
	if next.Valid {
		t := next.Time
		cfg.NextRunAt = &t
	}
	if last.Valid {
		t := last.Time
		cfg.LastRunAt = &t
	}
	return cfg, err
}

func (r *upstreamQualityProbeRepository) GetConfig(ctx context.Context, accountID int64) (*service.UpstreamQualityProbeConfig, error) {
	cfg, err := scanUpstreamQualityConfig(r.db.QueryRowContext(ctx, `SELECT `+upstreamQualityConfigColumns+`
FROM upstream_quality_probe_configs c LEFT JOIN accounts a ON a.id = c.account_id WHERE c.account_id = $1`, accountID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

func decodeQualityPrompts(raw []byte) []string {
	out := []string{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	if out == nil {
		out = []string{}
	}
	return out
}

func encodeQualityPrompts(prompts []string) string {
	if len(prompts) == 0 {
		return "[]"
	}
	b, err := json.Marshal(prompts)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func (r *upstreamQualityProbeRepository) UpsertConfig(ctx context.Context, cfg *service.UpstreamQualityProbeConfig) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO upstream_quality_probe_configs (account_id, enabled, model, interval_minutes, prompts, next_run_at, updated_at)
VALUES ($1, $2, $3, $4, $5::jsonb, NULL, NOW())
ON CONFLICT (account_id) DO UPDATE SET enabled = EXCLUDED.enabled, model = EXCLUDED.model,
  interval_minutes = EXCLUDED.interval_minutes, prompts = EXCLUDED.prompts, next_run_at = NULL, updated_at = NOW()`,
		cfg.AccountID, cfg.Enabled, cfg.Model, cfg.IntervalMinutes, encodeQualityPrompts(cfg.Prompts))
	return err
}

// NextPromptIndex hands out the account's next custom-prompt rotation slot (manual runs).
func (r *upstreamQualityProbeRepository) NextPromptIndex(ctx context.Context, accountID int64) (int64, error) {
	var slot int64
	err := r.db.QueryRowContext(ctx, `UPDATE upstream_quality_probe_configs SET prompt_rotation = prompt_rotation + 1
WHERE account_id = $1 RETURNING prompt_rotation - 1`, accountID).Scan(&slot)
	return slot, err
}

func (r *upstreamQualityProbeRepository) DeleteConfig(ctx context.Context, accountID int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM upstream_quality_probe_configs WHERE account_id = $1`, accountID)
	return err
}

func (r *upstreamQualityProbeRepository) ListConfigs(ctx context.Context) ([]service.UpstreamQualityProbeConfig, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+upstreamQualityConfigColumns+`
FROM upstream_quality_probe_configs c JOIN accounts a ON a.id = c.account_id AND a.deleted_at IS NULL ORDER BY c.account_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []service.UpstreamQualityProbeConfig{}
	for rows.Next() {
		cfg, err := scanUpstreamQualityConfig(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, cfg)
	}
	return out, rows.Err()
}

func (r *upstreamQualityProbeRepository) ClaimDue(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]service.UpstreamQualityProbeConfig, error) {
	rows, err := r.db.QueryContext(ctx, `
WITH due AS (
  SELECT c.account_id FROM upstream_quality_probe_configs c
  JOIN accounts a ON a.id = c.account_id AND a.deleted_at IS NULL
  WHERE c.enabled AND (c.next_run_at IS NULL OR c.next_run_at <= $1)
    AND (c.running_until IS NULL OR c.running_until < $1)
  ORDER BY c.next_run_at NULLS FIRST, c.account_id
  LIMIT $3
  FOR UPDATE OF c SKIP LOCKED
)
UPDATE upstream_quality_probe_configs c
SET running_until = $1 + $2 * INTERVAL '1 second',
    next_run_at = $1 + c.interval_minutes * INTERVAL '1 minute',
    prompt_rotation = c.prompt_rotation + CASE WHEN jsonb_array_length(COALESCE(c.prompts, '[]'::jsonb)) > 0 THEN 1 ELSE 0 END
FROM due WHERE c.account_id = due.account_id
RETURNING c.account_id, c.model, c.interval_minutes, COALESCE(c.prompts, '[]'::jsonb),
  c.prompt_rotation - CASE WHEN jsonb_array_length(COALESCE(c.prompts, '[]'::jsonb)) > 0 THEN 1 ELSE 0 END`, now, int64(lease/time.Second), limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []service.UpstreamQualityProbeConfig{}
	for rows.Next() {
		var cfg service.UpstreamQualityProbeConfig
		var prompts []byte
		if err := rows.Scan(&cfg.AccountID, &cfg.Model, &cfg.IntervalMinutes, &prompts, &cfg.PromptSlot); err != nil {
			return nil, err
		}
		cfg.Prompts = decodeQualityPrompts(prompts)
		cfg.Enabled = true
		out = append(out, cfg)
	}
	return out, rows.Err()
}

func (r *upstreamQualityProbeRepository) FinishRun(ctx context.Context, accountID int64, finishedAt time.Time) error {
	_, err := r.db.ExecContext(ctx, `UPDATE upstream_quality_probe_configs SET running_until = NULL, last_run_at = $2 WHERE account_id = $1`, accountID, finishedAt)
	return err
}

func nullableInt64(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

func (r *upstreamQualityProbeRepository) InsertResult(ctx context.Context, res *service.UpstreamQualityProbeResult) error {
	anomalies := res.Anomalies
	if anomalies == nil {
		anomalies = []string{}
	}
	return r.db.QueryRowContext(ctx, `
INSERT INTO upstream_quality_probe_results
 (account_id, trigger, protocol, model, upstream_model, status, http_status, error_message, ttft_ms, total_ms, output_tps,
  input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, estimated_input_tokens, anomalies, response_excerpt, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19) RETURNING id`,
		res.AccountID, res.Trigger, res.Protocol, res.Model, res.UpstreamModel, res.Status, res.HTTPStatus, res.ErrorMessage,
		nullableInt64(res.TTFTMs), res.TotalMs, nullableFloat(res.OutputTPS), nullableInt64(res.InputTokens), nullableInt64(res.OutputTokens),
		nullableInt64(res.CacheReadTokens), nullableInt64(res.CacheWriteTokens), res.EstimatedInputTokens, pq.Array(anomalies),
		res.ResponseExcerpt, res.CreatedAt,
	).Scan(&res.ID)
}

const upstreamQualityResultColumns = `id, account_id, trigger, protocol, model, upstream_model, status, http_status, error_message, ttft_ms, total_ms, output_tps,
 input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, estimated_input_tokens, anomalies, response_excerpt, created_at`

func scanUpstreamQualityResult(row interface{ Scan(...any) error }) (service.UpstreamQualityProbeResult, error) {
	var res service.UpstreamQualityProbeResult
	var ttft, in, out, cr, cw sql.NullInt64
	var tps sql.NullFloat64
	var anomalies pq.StringArray
	err := row.Scan(&res.ID, &res.AccountID, &res.Trigger, &res.Protocol, &res.Model, &res.UpstreamModel, &res.Status, &res.HTTPStatus,
		&res.ErrorMessage, &ttft, &res.TotalMs, &tps, &in, &out, &cr, &cw, &res.EstimatedInputTokens, &anomalies, &res.ResponseExcerpt, &res.CreatedAt)
	toPtr := func(v sql.NullInt64) *int64 {
		if !v.Valid {
			return nil
		}
		n := v.Int64
		return &n
	}
	res.TTFTMs, res.InputTokens, res.OutputTokens, res.CacheReadTokens, res.CacheWriteTokens = toPtr(ttft), toPtr(in), toPtr(out), toPtr(cr), toPtr(cw)
	res.OutputTPS = floatFromNull(tps)
	res.Anomalies = []string(anomalies)
	if res.Anomalies == nil {
		res.Anomalies = []string{}
	}
	return res, err
}

func (r *upstreamQualityProbeRepository) ListResults(ctx context.Context, accountID int64, limit int) ([]service.UpstreamQualityProbeResult, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+upstreamQualityResultColumns+` FROM upstream_quality_probe_results WHERE account_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2`, accountID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []service.UpstreamQualityProbeResult{}
	for rows.Next() {
		res, err := scanUpstreamQualityResult(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, res)
	}
	return out, rows.Err()
}

func (r *upstreamQualityProbeRepository) LatestResults(ctx context.Context) (map[int64]service.UpstreamQualityProbeResult, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+upstreamQualityResultColumns+` FROM (
  SELECT DISTINCT ON (r.account_id) r.* FROM upstream_quality_probe_results r
  JOIN upstream_quality_probe_configs c ON c.account_id = r.account_id
  ORDER BY r.account_id, r.created_at DESC, r.id DESC
) latest`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[int64]service.UpstreamQualityProbeResult{}
	for rows.Next() {
		res, err := scanUpstreamQualityResult(rows)
		if err != nil {
			return nil, err
		}
		out[res.AccountID] = res
	}
	return out, rows.Err()
}

func (r *upstreamQualityProbeRepository) PruneResults(ctx context.Context, accountID int64, keep int) error {
	_, err := r.db.ExecContext(ctx, `
DELETE FROM upstream_quality_probe_results WHERE account_id = $1 AND id NOT IN (
  SELECT id FROM upstream_quality_probe_results WHERE account_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2
)`, accountID, keep)
	return err
}
