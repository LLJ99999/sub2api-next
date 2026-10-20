package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// All-cooled fallback.
//
// When account selection fails only because every otherwise-eligible API-key
// account is cooling down (rate limit / overload / temp-unschedulable / relay
// breaker / model cooldown) — not because accounts are disabled, in error,
// expired, out of quota or paused for balance — the gateway lets one request
// through to the account whose cooldown ends first instead of answering 503.
// A Redis lock (shared across instances) grants at most one such probe per
// group/platform/model and probe interval, so a fully cooled pool is not hit by
// a thundering herd. A successful probe clears the cooldown through the
// success self-heal; a failed one is handled by the normal error path.

const defaultCooldownFallbackProbeInterval = 10 * time.Second

func cooldownFallbackSettings(cfg *config.Config) (bool, time.Duration) {
	if cfg == nil || !cfg.Gateway.CooldownFallback.Enabled {
		return false, 0
	}
	interval := defaultCooldownFallbackProbeInterval
	if cfg.Gateway.CooldownFallback.ProbeIntervalSeconds > 0 {
		interval = time.Duration(cfg.Gateway.CooldownFallback.ProbeIntervalSeconds) * time.Second
	}
	return true, interval
}

// shouldTryCooldownFallback limits the fallback to plain "no available
// accounts" outcomes; channel pricing restrictions and routing errors are
// deliberate rejections.
func shouldTryCooldownFallback(ctx context.Context, err error) bool {
	if err == nil || (ctx != nil && ctx.Err() != nil) {
		return false
	}
	if !errors.Is(err, ErrNoAvailableAccounts) {
		return false
	}
	msg := err.Error()
	return !strings.Contains(msg, "channel pricing restriction") && !strings.Contains(msg, "composite target platform unknown")
}

func (s *RateLimitService) cooldownFallbackLocker() CooldownFallbackProbeLocker {
	if s == nil {
		return nil
	}
	if locker, ok := s.tempUnschedCache.(CooldownFallbackProbeLocker); ok && locker != nil {
		return locker
	}
	s.localFallbackOnce.Do(func() {
		if s.localFallbackLocker == nil {
			s.localFallbackLocker = newLocalCooldownFallbackLocker()
		}
	})
	return s.localFallbackLocker
}

// SetCooldownFallbackProbeLocker overrides the probe locker (tests / wiring).
func (s *RateLimitService) SetCooldownFallbackProbeLocker(locker CooldownFallbackProbeLocker) {
	if s == nil {
		return
	}
	s.localFallbackOnce.Do(func() {})
	s.localFallbackLocker = locker
}

// TryAcquireCooldownFallbackProbe grants one fallback probe per key and ttl.
// Lock errors fail closed (no probe): the fallback is best-effort.
func (s *RateLimitService) TryAcquireCooldownFallbackProbe(ctx context.Context, key string, ttl time.Duration) bool {
	locker := s.cooldownFallbackLocker()
	if locker == nil {
		return false
	}
	ok, err := locker.TryAcquireCooldownFallbackProbe(ctx, key, ttl)
	if err != nil {
		slog.Warn("cooldown_fallback_probe_lock_failed", "key", key, "error", err)
		return false
	}
	return ok
}

// cooldownOnlyBlockEnd reports when the account's recoverable cooldown ends,
// if a cooldown is the only thing keeping it out of scheduling.
func cooldownOnlyBlockEnd(ctx context.Context, account *Account, model string, now time.Time) (time.Time, bool) {
	if account == nil || account.Type != AccountTypeAPIKey || !account.IsActive() || !account.Schedulable {
		return time.Time{}, false
	}
	if account.AutoPauseOnExpired && account.ExpiresAt != nil && !now.Before(*account.ExpiresAt) {
		return time.Time{}, false
	}
	if account.IsQuotaExceeded() {
		return time.Time{}, false
	}
	// Probing cannot fix an exhausted balance or a utilization-threshold pause.
	if isPaymentRequired402Cooldown(account, now) || isActiveSchedulingThresholdPause(account, now) {
		return time.Time{}, false
	}
	var end time.Time
	for _, t := range []*time.Time{account.OverloadUntil, account.RateLimitResetAt, account.TempUnschedulableUntil} {
		if t != nil && now.Before(*t) && t.After(end) {
			end = *t
		}
	}
	if model != "" {
		if remaining := account.GetModelRateLimitRemainingTimeWithContext(ctx, model); remaining > 0 {
			if modelEnd := now.Add(remaining); modelEnd.After(end) {
				end = modelEnd
			}
		}
	}
	if end.IsZero() {
		return time.Time{}, false
	}
	return end, true
}

// withoutCooldown returns a shallow copy of the account with every recoverable
// cooldown removed, used to ask "would this account be eligible if it were not
// cooling down?" with the regular eligibility checks.
func withoutCooldown(account *Account) *Account {
	clone := *account
	clone.RateLimitedAt, clone.RateLimitResetAt, clone.OverloadUntil = nil, nil, nil
	clone.TempUnschedulableUntil, clone.TempUnschedulableReason = nil, ""
	if account.Extra != nil {
		extra := make(map[string]any, len(account.Extra))
		for k, v := range account.Extra {
			if k == modelRateLimitsKey {
				continue
			}
			extra[k] = v
		}
		clone.Extra = extra
	}
	return &clone
}

// pickCooldownFallbackCandidate returns the cooled-only account whose cooldown
// ends first (ties: most recently used, i.e. most recent success).
func pickCooldownFallbackCandidate(ctx context.Context, accounts []Account, excluded map[int64]struct{}, model string, now time.Time, eligible func(*Account) bool) *Account {
	var best *Account
	var bestEnd time.Time
	for i := range accounts {
		acc := &accounts[i]
		if _, skip := excluded[acc.ID]; skip {
			continue
		}
		end, ok := cooldownOnlyBlockEnd(ctx, acc, model, now)
		if !ok {
			continue
		}
		if eligible != nil && !eligible(withoutCooldown(acc)) {
			continue
		}
		if best == nil || end.Before(bestEnd) || (end.Equal(bestEnd) && lastUsedAfter(acc, best)) {
			best, bestEnd = acc, end
		}
	}
	return best
}

func lastUsedAfter(a, b *Account) bool {
	if a.LastUsedAt == nil {
		return false
	}
	return b.LastUsedAt == nil || a.LastUsedAt.After(*b.LastUsedAt)
}

func cooldownFallbackCandidatesQuery(cfg *config.Config, groupID *int64) bool {
	// includeGrouped: only the simple run mode schedules grouped accounts
	// without a group binding on the request.
	return groupID == nil && cfg != nil && cfg.RunMode == config.RunModeSimple
}

// selectCooldownFallback implements the all-cooled fallback for the
// Anthropic/Gemini/Antigravity gateway selection.
func (s *GatewayService) selectCooldownFallback(ctx context.Context, groupID *int64, requestedModel string, excludedIDs map[int64]struct{}) *AccountSelectionResult {
	enabled, interval := cooldownFallbackSettings(s.cfg)
	if !enabled || s.accountRepo == nil || s.rateLimitService == nil {
		return nil
	}
	group, groupID, err := s.checkClaudeCodeRestriction(ctx, groupID)
	if err != nil {
		return nil
	}
	ctx = s.withGroupContext(ctx, group)
	ctx = s.withGatewayProfitControlGate(ctx, groupID)
	platform, _, err := s.resolvePlatform(ctx, groupID, group, requestedModel)
	if err != nil || platform == "" {
		return nil
	}
	accounts, err := s.accountRepo.ListModelAvailabilityCandidates(ctx, groupID, []string{platform}, cooldownFallbackCandidatesQuery(s.cfg, groupID))
	if err != nil || len(accounts) == 0 {
		return nil
	}
	pick := pickCooldownFallbackCandidate(ctx, accounts, excludedIDs, requestedModel, time.Now(), func(acc *Account) bool {
		if acc.Platform != platform {
			return false
		}
		if requestedModel != "" && !s.isModelSupportedByAccountInGroup(ctx, acc, groupID, requestedModel) {
			return false
		}
		return s.isAccountSchedulableForModelSelection(ctx, acc, requestedModel) &&
			s.isAccountSchedulableForQuota(acc) &&
			s.isGatewayAccountProfitEligible(ctx, acc)
	})
	if pick == nil {
		return nil
	}
	key := fmt.Sprintf("gw|%d|%s|%s", derefGroupID(groupID), platform, requestedModel)
	if !s.rateLimitService.TryAcquireCooldownFallbackProbe(ctx, key, interval) {
		return nil
	}
	slog.Warn("cooldown_fallback_probe_selected",
		"account_id", pick.ID,
		"group_id", derefGroupID(groupID),
		"platform", platform,
		"model", requestedModel)
	cfg := s.schedulingConfig()
	if result, err := s.tryAcquireAccountSlot(ctx, pick.ID, pick.Concurrency); err == nil && result != nil && result.Acquired {
		selection, selErr := s.newSelectionResult(ctx, pick, true, result.ReleaseFunc, nil)
		if selErr != nil {
			result.ReleaseFunc()
			return nil
		}
		return selection
	}
	selection, err := s.newSelectionResult(ctx, pick, false, nil, &AccountWaitPlan{
		AccountID:      pick.ID,
		MaxConcurrency: pick.Concurrency,
		Timeout:        cfg.FallbackWaitTimeout,
		MaxWaiting:     cfg.FallbackMaxWaiting,
	})
	if err != nil {
		return nil
	}
	return selection
}

// selectOpenAICooldownFallback implements the all-cooled fallback for the
// OpenAI-compatible scheduler. It is limited to plain HTTP requests without
// compact/capability/previous-response constraints.
func (s *OpenAIGatewayService) selectOpenAICooldownFallback(ctx context.Context, groupID *int64, requestedModel string, excludedIDs map[int64]struct{}, platform string) *AccountSelectionResult {
	enabled, interval := cooldownFallbackSettings(s.cfg)
	if !enabled || s.accountRepo == nil || s.rateLimitService == nil {
		return nil
	}
	platform = NormalizeOpenAICompatiblePlatform(platform)
	accounts, err := s.accountRepo.ListModelAvailabilityCandidates(ctx, groupID, []string{platform}, cooldownFallbackCandidatesQuery(s.cfg, groupID))
	if err != nil || len(accounts) == 0 {
		return nil
	}
	pick := pickCooldownFallbackCandidate(ctx, accounts, excludedIDs, requestedModel, time.Now(), func(acc *Account) bool {
		return isOpenAICompatibleAccountEligibleForRequestBeforeProfit(ctx, acc, groupID, platform, requestedModel, false, "")
	})
	if pick == nil {
		return nil
	}
	key := fmt.Sprintf("oa|%d|%s|%s", derefGroupID(groupID), platform, requestedModel)
	if !s.rateLimitService.TryAcquireCooldownFallbackProbe(ctx, key, interval) {
		return nil
	}
	slog.Warn("cooldown_fallback_probe_selected",
		"account_id", pick.ID,
		"group_id", derefGroupID(groupID),
		"platform", platform,
		"model", requestedModel)
	cfg := s.schedulingConfig()
	if result, err := s.tryAcquireAccountSlot(ctx, pick.ID, pick.Concurrency); err == nil && result != nil && result.Acquired {
		selection, selErr := s.newAcquiredSelectionResult(ctx, pick, result.ReleaseFunc, result.RequestID)
		if selErr != nil {
			return nil
		}
		return selection
	}
	selection, err := s.newSelectionResult(ctx, pick, false, nil, "", &AccountWaitPlan{
		AccountID:      pick.ID,
		MaxConcurrency: pick.Concurrency,
		Timeout:        cfg.FallbackWaitTimeout,
		MaxWaiting:     cfg.FallbackMaxWaiting,
	})
	if err != nil {
		return nil
	}
	return selection
}
