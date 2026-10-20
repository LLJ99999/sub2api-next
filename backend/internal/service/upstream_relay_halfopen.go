package service

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Half-open trial cap for the upstream relay circuit breaker.
//
// When a breaker cooldown (account or model scope) expires, the account's
// snapshot still carries the expired breaker reason until a success clears it
// (success self-heal) or a failure re-trips the breaker with a new cooldown.
// During that half-open window at most upstreamRelayHalfOpenMaxTrials
// requests may be scheduled onto the account/scope at the same time, so a
// recovering relay is not hit by the full pent-up traffic. The counter is
// shared across instances through the Redis store and resets as soon as a
// trial resolves (success closes the breaker, failure re-opens it); a TTL is
// the safety net for trials that end without a breaker-relevant outcome.

const (
	upstreamRelayHalfOpenMaxTrials = 2
	upstreamRelayHalfOpenMinTTL    = 30 * time.Second
	// upstreamRelayHalfOpenMaxReselect bounds how many half-open accounts the
	// selection wrapper skips before giving up on the cap for this request.
	upstreamRelayHalfOpenMaxReselect = 3
)

// UpstreamRelayHalfOpenStore keeps the half-open trial counter.
type UpstreamRelayHalfOpenStore interface {
	// TryAcquireUpstreamRelayHalfOpenTrial increments the trial counter if it is
	// below limit. The counter expires after ttl.
	TryAcquireUpstreamRelayHalfOpenTrial(ctx context.Context, accountID int64, scope string, limit int, ttl time.Duration) (bool, error)
	// ResetUpstreamRelayHalfOpenTrials drops the counter once a trial resolved.
	ResetUpstreamRelayHalfOpenTrials(ctx context.Context, accountID int64, scope string) error
}

func (s *RateLimitService) relayHalfOpenStore() UpstreamRelayHalfOpenStore {
	if s == nil {
		return nil
	}
	if store, ok := s.tempUnschedCache.(UpstreamRelayHalfOpenStore); ok && store != nil {
		return store
	}
	if store, ok := s.relayBreakerStore().(UpstreamRelayHalfOpenStore); ok && store != nil {
		return store
	}
	return nil
}

func (s *RateLimitService) upstreamRelayHalfOpenTTL() time.Duration {
	ttl := s.upstreamRelayBreakerSettings().Window
	if ttl < upstreamRelayHalfOpenMinTTL {
		ttl = upstreamRelayHalfOpenMinTTL
	}
	return ttl
}

// upstreamRelayHalfOpenScope reports whether the account is in the half-open
// window of a breaker cooldown for the requested model and returns the breaker
// scope key ("account" or "model:<key>").
func upstreamRelayHalfOpenScope(ctx context.Context, account *Account, model string, now time.Time) (string, bool) {
	if account == nil || !account.IsUpstreamRelayPolicyEnabled() {
		return "", false
	}
	if until := account.TempUnschedulableUntil; until != nil && !now.Before(*until) &&
		strings.Contains(account.TempUnschedulableReason, UpstreamRelayBreakerReasonKeyword) {
		return "account", true
	}
	model = strings.TrimSpace(model)
	if model == "" || account.Extra == nil {
		return "", false
	}
	limits, ok := account.Extra[modelRateLimitsKey].(map[string]any)
	if !ok || len(limits) == 0 {
		return "", false
	}
	keys := append([]string{model}, account.modelRateLimitKeysForRequest(ctx, model)...)
	for _, key := range dedupeStrings(keys) {
		entry, ok := limits[key].(map[string]any)
		if !ok {
			continue
		}
		reason, _ := entry["reason"].(string)
		if !strings.Contains(reason, UpstreamRelayBreakerReasonKeyword) {
			continue
		}
		resetRaw, _ := entry["rate_limit_reset_at"].(string)
		resetAt, err := time.Parse(time.RFC3339, resetRaw)
		if err != nil || now.Before(resetAt) {
			continue
		}
		return upstreamRelayScopeKey(true, key), true
	}
	return "", false
}

// AdmitUpstreamRelayHalfOpenTrial reports whether a request may be scheduled
// onto the account. Accounts outside a breaker half-open window are always
// admitted; store errors fail open.
func (s *RateLimitService) AdmitUpstreamRelayHalfOpenTrial(ctx context.Context, account *Account, model string) bool {
	if s == nil {
		return true
	}
	scope, ok := upstreamRelayHalfOpenScope(ctx, account, model, time.Now())
	if !ok {
		return true
	}
	store := s.relayHalfOpenStore()
	if store == nil {
		return true
	}
	admitted, err := store.TryAcquireUpstreamRelayHalfOpenTrial(ctx, account.ID, scope, upstreamRelayHalfOpenMaxTrials, s.upstreamRelayHalfOpenTTL())
	if err != nil {
		slog.Warn("upstream_relay_half_open_acquire_failed", "account_id", account.ID, "scope", scope, "error", err)
		return true
	}
	if !admitted {
		slog.Debug("upstream_relay_half_open_trial_capped", "account_id", account.ID, "scope", scope)
	}
	return admitted
}

func (s *RateLimitService) resetUpstreamRelayHalfOpen(ctx context.Context, accountID int64, scope string) {
	store := s.relayHalfOpenStore()
	if store == nil {
		return
	}
	if err := store.ResetUpstreamRelayHalfOpenTrials(ctx, accountID, scope); err != nil {
		slog.Warn("upstream_relay_half_open_reset_failed", "account_id", accountID, "scope", scope, "error", err)
	}
}

// admitHalfOpenSelection wraps an account selection: when the selected account
// is in a capped half-open window, the selection is released and retried with
// the account excluded.
func admitHalfOpenSelection(
	ctx context.Context,
	rls *RateLimitService,
	model string,
	excludedIDs map[int64]struct{},
	selectFn func(excluded map[int64]struct{}) (*AccountSelectionResult, error),
) (*AccountSelectionResult, error) {
	result, err := selectFn(excludedIDs)
	if rls == nil {
		return result, err
	}
	var excluded map[int64]struct{}
	for attempt := 0; attempt < upstreamRelayHalfOpenMaxReselect; attempt++ {
		if err != nil || result == nil || result.Account == nil {
			return result, err
		}
		if rls.AdmitUpstreamRelayHalfOpenTrial(ctx, result.Account, model) {
			return result, nil
		}
		if result.Acquired && result.ReleaseFunc != nil {
			result.ReleaseFunc()
		}
		if excluded == nil {
			excluded = make(map[int64]struct{}, len(excludedIDs)+upstreamRelayHalfOpenMaxReselect)
			for id := range excludedIDs {
				excluded[id] = struct{}{}
			}
		}
		excluded[result.Account.ID] = struct{}{}
		result, err = selectFn(excluded)
	}
	return result, err
}

// --- in-process store (attached to the local breaker store) ---

type localHalfOpenEntry struct {
	count   int
	expires time.Time
}

type localUpstreamRelayHalfOpen struct {
	mu      sync.Mutex
	entries map[string]*localHalfOpenEntry
}

func (l *localUpstreamRelayBreakerStore) TryAcquireUpstreamRelayHalfOpenTrial(_ context.Context, accountID int64, scope string, limit int, ttl time.Duration) (bool, error) {
	l.halfOpen.mu.Lock()
	defer l.halfOpen.mu.Unlock()
	now := l.now()
	if l.halfOpen.entries == nil {
		l.halfOpen.entries = make(map[string]*localHalfOpenEntry)
	}
	k := l.key(accountID, scope)
	e := l.halfOpen.entries[k]
	if e == nil || !now.Before(e.expires) {
		e = &localHalfOpenEntry{expires: now.Add(ttl)}
		l.halfOpen.entries[k] = e
	}
	if e.count >= limit {
		return false, nil
	}
	e.count++
	return true, nil
}

func (l *localUpstreamRelayBreakerStore) ResetUpstreamRelayHalfOpenTrials(_ context.Context, accountID int64, scope string) error {
	l.halfOpen.mu.Lock()
	defer l.halfOpen.mu.Unlock()
	delete(l.halfOpen.entries, l.key(accountID, scope))
	return nil
}
