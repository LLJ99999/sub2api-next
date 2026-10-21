package service

import (
	"context"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// UpstreamProbeBatchResult is one row of a bulk enable/disable of an upstream
// probe (上游变动监控 / 上游质量探测) from the accounts page.
type UpstreamProbeBatchResult struct {
	AccountID int64  `json:"account_id"`
	Status    string `json:"status"` // enabled | disabled | skipped_duplicate | failed
	Message   string `json:"message,omitempty"`
	// DuplicateOf is the account that already covers this base URL in the batch.
	DuplicateOf int64 `json:"duplicate_of,omitempty"`
}

const (
	UpstreamProbeBatchStatusEnabled   = "enabled"
	UpstreamProbeBatchStatusDisabled  = "disabled"
	UpstreamProbeBatchStatusDuplicate = "skipped_duplicate"
	UpstreamProbeBatchStatusFailed    = "failed"
	upstreamProbeBatchMaxAccounts     = 500
)

var ErrUpstreamProbeBatchInvalid = infraerrors.BadRequest("UPSTREAM_PROBE_BATCH_INVALID", "account_ids must list 1-500 accounts")

func upstreamProbeBaseURLKey(account *Account) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(account.GetCredential("base_url")), "/"))
}

// runUpstreamProbeBatch loads each account once and applies set. When enabling,
// accounts sharing a base URL with an earlier one in the batch are skipped so the
// same upstream is not probed twice; disabling always applies to every account.
func runUpstreamProbeBatch(ctx context.Context, repo AccountRepository, ids []int64, enabled bool, set func(*Account) error) ([]UpstreamProbeBatchResult, error) {
	if repo == nil {
		return nil, ErrUpstreamBillingProbeUnavailable
	}
	if len(ids) == 0 || len(ids) > upstreamProbeBatchMaxAccounts {
		return nil, ErrUpstreamProbeBatchInvalid
	}
	seenID := map[int64]struct{}{}
	byURL := map[string]int64{}
	out := make([]UpstreamProbeBatchResult, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, dup := seenID[id]; dup {
			continue
		}
		seenID[id] = struct{}{}
		row := UpstreamProbeBatchResult{AccountID: id}
		account, err := repo.GetByID(ctx, id)
		if err != nil || account == nil {
			row.Status, row.Message = UpstreamProbeBatchStatusFailed, "account not found"
			out = append(out, row)
			continue
		}
		if enabled {
			if key := upstreamProbeBaseURLKey(account); key != "" {
				if first, dup := byURL[key]; dup {
					row.Status, row.DuplicateOf = UpstreamProbeBatchStatusDuplicate, first
					out = append(out, row)
					continue
				}
				byURL[key] = id
			}
		}
		if err := set(account); err != nil {
			row.Status, row.Message = UpstreamProbeBatchStatusFailed, safeUpstreamProbeBatchMessage(err)
			if enabled {
				// A failed row does not cover its base URL; let a later account try.
				delete(byURL, upstreamProbeBaseURLKey(account))
			}
			out = append(out, row)
			continue
		}
		row.Status = UpstreamProbeBatchStatusDisabled
		if enabled {
			row.Status = UpstreamProbeBatchStatusEnabled
		}
		out = append(out, row)
	}
	return out, nil
}

func safeUpstreamProbeBatchMessage(err error) string {
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
}

// BatchSetAccountEnabled toggles 上游变动监控 (upstream billing auto-probe) for many accounts.
func (s *UpstreamBillingProbeService) BatchSetAccountEnabled(ctx context.Context, ids []int64, enabled bool) ([]UpstreamProbeBatchResult, error) {
	if s == nil || s.accountRepo == nil {
		return nil, ErrUpstreamBillingProbeUnavailable
	}
	return runUpstreamProbeBatch(ctx, s.accountRepo, ids, enabled, func(account *Account) error {
		return s.SetAccountEnabled(ctx, account.ID, enabled)
	})
}

// BatchSetEnabled turns 上游质量探测 on or off for many accounts. Enabling keeps
// each account's saved model/interval/prompts and uses model/interval only for
// accounts without a saved config. Disabling an account without config is a no-op.
func (s *UpstreamQualityProbeService) BatchSetEnabled(ctx context.Context, ids []int64, enabled bool, model string, intervalMinutes int) ([]UpstreamProbeBatchResult, error) {
	if s == nil || s.accounts == nil || s.repo == nil {
		return nil, ErrUpstreamQualityProbeAccount
	}
	model = strings.TrimSpace(model)
	return runUpstreamProbeBatch(ctx, s.accounts, ids, enabled, func(account *Account) error {
		current, err := s.repo.GetConfig(ctx, account.ID)
		if err != nil {
			return err
		}
		if current == nil && !enabled {
			return nil
		}
		cfg := UpstreamQualityProbeConfig{AccountID: account.ID, Enabled: enabled, Model: model, IntervalMinutes: intervalMinutes}
		if current != nil {
			cfg.Model, cfg.IntervalMinutes, cfg.Prompts = current.Model, current.IntervalMinutes, current.Prompts
			if cfg.Model == "" {
				cfg.Model = model
			}
		}
		_, err = s.SaveConfig(ctx, cfg)
		return err
	})
}
