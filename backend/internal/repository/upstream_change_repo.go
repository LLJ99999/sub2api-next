package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type upstreamChangeRepository struct{ db *sql.DB }

func NewUpstreamChangeRepository(db *sql.DB) service.UpstreamChangeRepository {
	return &upstreamChangeRepository{db: db}
}

func nullableFloat(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

func floatFromNull(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	out := v.Float64
	return &out
}

func (r *upstreamChangeRepository) InsertSnapshot(ctx context.Context, rec *service.UpstreamRateSnapshotRecord) error {
	return r.db.QueryRowContext(ctx, `
INSERT INTO upstream_rate_snapshots
 (account_id, site, provider, upstream_group, group_multiplier, resolved_multiplier, effective_multiplier, balance, balance_unit, observed_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`,
		rec.AccountID, rec.Site, rec.Provider, rec.UpstreamGroup,
		nullableFloat(rec.GroupMultiplier), nullableFloat(rec.ResolvedMultiplier), nullableFloat(rec.EffectiveMultiplier),
		nullableFloat(rec.Balance), rec.BalanceUnit, rec.ObservedAt,
	).Scan(&rec.ID)
}

func (r *upstreamChangeRepository) InsertEvent(ctx context.Context, ev *service.UpstreamChangeEvent) error {
	return r.db.QueryRowContext(ctx, `
INSERT INTO upstream_change_events
 (account_id, account_name, site, kind, direction, old_value, new_value, threshold, unit, notify_state, observed_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`,
		ev.AccountID, ev.AccountName, ev.Site, ev.Kind, ev.Direction,
		nullableFloat(ev.OldValue), nullableFloat(ev.NewValue), nullableFloat(ev.Threshold), ev.Unit, ev.NotifyState, ev.ObservedAt,
	).Scan(&ev.ID)
}

const upstreamChangeEventColumns = `id, account_id, account_name, site, kind, direction, old_value, new_value, threshold, unit, notify_state, notified_at, observed_at`

func scanUpstreamChangeEvent(rows interface{ Scan(...any) error }) (service.UpstreamChangeEvent, error) {
	var ev service.UpstreamChangeEvent
	var oldValue, newValue, threshold sql.NullFloat64
	var notified sql.NullTime
	err := rows.Scan(&ev.ID, &ev.AccountID, &ev.AccountName, &ev.Site, &ev.Kind, &ev.Direction,
		&oldValue, &newValue, &threshold, &ev.Unit, &ev.NotifyState, &notified, &ev.ObservedAt)
	if err != nil {
		return ev, err
	}
	ev.OldValue, ev.NewValue, ev.Threshold = floatFromNull(oldValue), floatFromNull(newValue), floatFromNull(threshold)
	if notified.Valid {
		t := notified.Time
		ev.NotifiedAt = &t
	}
	return ev, nil
}

func (r *upstreamChangeRepository) ListEvents(ctx context.Context, f service.UpstreamChangeEventFilter) ([]service.UpstreamChangeEvent, int64, error) {
	where := []string{"1=1"}
	args := []any{}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if f.AccountID > 0 {
		add("account_id = $%d", f.AccountID)
	}
	if f.Site != "" {
		add("site = $%d", f.Site)
	}
	if f.Kind != "" {
		add("kind = $%d", f.Kind)
	}
	if f.Direction != "" {
		add("direction = $%d", f.Direction)
	}
	if f.From != nil {
		add("observed_at >= $%d", *f.From)
	}
	if f.To != nil {
		add("observed_at < $%d", *f.To)
	}
	cond := strings.Join(where, " AND ")
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM upstream_change_events WHERE `+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, f.PageSize, (f.Page-1)*f.PageSize)
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(`SELECT %s FROM upstream_change_events WHERE %s ORDER BY observed_at DESC, id DESC LIMIT $%d OFFSET $%d`,
		upstreamChangeEventColumns, cond, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	out := []service.UpstreamChangeEvent{}
	for rows.Next() {
		ev, err := scanUpstreamChangeEvent(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, ev)
	}
	return out, total, rows.Err()
}

func (r *upstreamChangeRepository) ListSnapshots(ctx context.Context, accountID int64, since time.Time, limit int) ([]service.UpstreamRateSnapshotRecord, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT id, account_id, site, provider, upstream_group, group_multiplier, resolved_multiplier, effective_multiplier, balance, balance_unit, observed_at
FROM (
  SELECT * FROM upstream_rate_snapshots WHERE account_id = $1 AND observed_at >= $2 ORDER BY observed_at DESC LIMIT $3
) s ORDER BY observed_at ASC`, accountID, since, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []service.UpstreamRateSnapshotRecord{}
	for rows.Next() {
		var rec service.UpstreamRateSnapshotRecord
		var group, resolved, effective, balance sql.NullFloat64
		if err := rows.Scan(&rec.ID, &rec.AccountID, &rec.Site, &rec.Provider, &rec.UpstreamGroup,
			&group, &resolved, &effective, &balance, &rec.BalanceUnit, &rec.ObservedAt); err != nil {
			return nil, err
		}
		rec.GroupMultiplier, rec.ResolvedMultiplier = floatFromNull(group), floatFromNull(resolved)
		rec.EffectiveMultiplier, rec.Balance = floatFromNull(effective), floatFromNull(balance)
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (r *upstreamChangeRepository) ListSites(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT DISTINCT site FROM upstream_change_events WHERE site <> '' ORDER BY site LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []string{}
	for rows.Next() {
		var site string
		if err := rows.Scan(&site); err != nil {
			return nil, err
		}
		out = append(out, site)
	}
	return out, rows.Err()
}

func (r *upstreamChangeRepository) ListPendingEvents(ctx context.Context, limit int) ([]service.UpstreamChangeEvent, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+upstreamChangeEventColumns+` FROM upstream_change_events WHERE notify_state = 'pending' ORDER BY observed_at ASC, id ASC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []service.UpstreamChangeEvent{}
	for rows.Next() {
		ev, err := scanUpstreamChangeEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

func (r *upstreamChangeRepository) LastNotifiedAt(ctx context.Context, accountID int64, kind string) (*time.Time, error) {
	var at sql.NullTime
	err := r.db.QueryRowContext(ctx, `SELECT MAX(notified_at) FROM upstream_change_events WHERE account_id = $1 AND kind = $2 AND notify_state = 'sent'`, accountID, kind).Scan(&at)
	if err != nil || !at.Valid {
		return nil, err
	}
	t := at.Time
	return &t, nil
}

func (r *upstreamChangeRepository) MarkEvents(ctx context.Context, ids []int64, state string, at *time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	var notified any
	if at != nil {
		notified = *at
	}
	// Only pending rows move, so a concurrent flush cannot overwrite a result.
	_, err := r.db.ExecContext(ctx, `UPDATE upstream_change_events SET notify_state = $2, notified_at = $3 WHERE id = ANY($1) AND notify_state = 'pending'`, pq.Array(ids), state, notified)
	return err
}

func (r *upstreamChangeRepository) Prune(ctx context.Context, before time.Time) (int64, error) {
	var total int64
	for _, q := range []string{
		`DELETE FROM upstream_rate_snapshots WHERE id IN (SELECT id FROM upstream_rate_snapshots WHERE observed_at < $1 LIMIT 5000)`,
		`DELETE FROM upstream_change_events WHERE id IN (SELECT id FROM upstream_change_events WHERE observed_at < $1 AND notify_state <> 'pending' LIMIT 5000)`,
	} {
		res, err := r.db.ExecContext(ctx, q, before)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}
