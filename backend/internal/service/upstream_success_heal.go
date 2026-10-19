package service

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

// AccountModelRateLimitScopeClearer removes one model-scoped cooldown entry
// (accounts.extra.model_rate_limits[scope]) without touching the others.
// It is an optional AccountRepository extension.
type AccountModelRateLimitScopeClearer interface {
	ClearModelRateLimitScope(ctx context.Context, id int64, scope string) error
}

const upstreamSuccessHealTimeout = 3 * time.Second

// ObserveUpstreamSuccess is called after a successful upstream response. For
// API-key accounts (any platform) it clears residual rate-limit /
// temp-unschedulable / overload state for the account and the served model
// scope, mirroring the Anthropic-only clear in UpdateSessionWindow, and closes
// an engaged upstream-relay breaker. It only writes when state is present on
// the account snapshot, so the common healthy path costs nothing.
func (s *RateLimitService) ObserveUpstreamSuccess(ctx context.Context, account *Account, models ...string) {
	if s == nil || account == nil || account.ID <= 0 || account.Type != AccountTypeAPIKey {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if account.IsUpstreamRelayPolicyEnabled() {
		s.observeUpstreamRelaySuccess(ctx, account, models)
	}
	s.clearResidualCooldownOnSuccess(ctx, account, models)
}

func (s *RateLimitService) clearResidualCooldownOnSuccess(ctx context.Context, account *Account, models []string) {
	if s.accountRepo == nil {
		return
	}
	now := time.Now()
	clearAccountRateLimit := account.RateLimitedAt != nil || account.RateLimitResetAt != nil || account.OverloadUntil != nil
	clearTemp := account.TempUnschedulableUntil != nil && !isActiveSchedulingThresholdPause(account, now)
	modelKeys := residualModelRateLimitKeys(ctx, account, models)
	if !clearAccountRateLimit && !clearTemp && len(modelKeys) == 0 {
		return
	}

	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), upstreamSuccessHealTimeout)
	defer cancel()
	cleared := false
	if clearAccountRateLimit {
		if err := s.accountRepo.ClearRateLimit(writeCtx, account.ID); err != nil {
			slog.Warn("upstream_success_clear_rate_limit_failed", "account_id", account.ID, "error", err)
		} else {
			account.RateLimitedAt, account.RateLimitResetAt, account.OverloadUntil = nil, nil, nil
			cleared = true
		}
	}
	if clearTemp {
		if err := s.accountRepo.ClearTempUnschedulable(writeCtx, account.ID); err != nil {
			slog.Warn("upstream_success_clear_temp_unsched_failed", "account_id", account.ID, "error", err)
		} else {
			account.TempUnschedulableUntil, account.TempUnschedulableReason = nil, ""
			cleared = true
			if s.tempUnschedCache != nil {
				if err := s.tempUnschedCache.DeleteTempUnsched(writeCtx, account.ID); err != nil {
					slog.Warn("upstream_success_temp_unsched_cache_delete_failed", "account_id", account.ID, "error", err)
				}
			}
		}
	}
	if len(modelKeys) > 0 {
		if clearer, ok := s.accountRepo.(AccountModelRateLimitScopeClearer); ok {
			for _, key := range modelKeys {
				if err := clearer.ClearModelRateLimitScope(writeCtx, account.ID, key); err != nil {
					slog.Warn("upstream_success_clear_model_rate_limit_failed", "account_id", account.ID, "model", key, "error", err)
					continue
				}
				removeAccountModelRateLimitSnapshot(account, key)
				cleared = true
			}
		}
	}
	if cleared {
		s.notifyAccountSchedulingBlockCleared(account.ID)
		slog.Info("upstream_success_cleared_residual_cooldown",
			"account_id", account.ID,
			"rate_limit", clearAccountRateLimit,
			"temp_unsched", clearTemp,
			"models", modelKeys)
	}
}

// isActiveSchedulingThresholdPause keeps admin utilization-threshold pauses:
// they come from usage snapshots, not from upstream errors, and a single
// success does not prove the window has headroom again.
func isActiveSchedulingThresholdPause(account *Account, now time.Time) bool {
	if account == nil || account.TempUnschedulableUntil == nil || !now.Before(*account.TempUnschedulableUntil) {
		return false
	}
	payload, ok := parseTempUnschedReasonPayload(account.TempUnschedulableReason)
	return ok && payload.Source == AccountSchedulingThresholdReasonSource
}

// residualModelRateLimitKeys returns the model_rate_limits keys of the served
// model(s) that currently hold an entry on the account snapshot.
func residualModelRateLimitKeys(ctx context.Context, account *Account, models []string) []string {
	if account == nil || account.Extra == nil {
		return nil
	}
	limits, ok := account.Extra[modelRateLimitsKey].(map[string]any)
	if !ok || len(limits) == 0 {
		return nil
	}
	candidates := make([]string, 0, 4)
	for _, m := range models {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		candidates = append(candidates, m)
		candidates = append(candidates, account.modelRateLimitKeysForRequest(ctx, m)...)
	}
	out := make([]string, 0, len(candidates))
	for _, key := range dedupeStrings(candidates) {
		if _, exists := limits[key]; exists {
			out = append(out, key)
		}
	}
	return out
}

func removeAccountModelRateLimitSnapshot(account *Account, scope string) {
	if account == nil || account.Extra == nil {
		return
	}
	if limits, ok := account.Extra[modelRateLimitsKey].(map[string]any); ok {
		delete(limits, scope)
	}
}
