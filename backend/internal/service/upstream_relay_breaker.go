package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// UpstreamRelayPolicyExtraKey is the per-account opt-in flag (accounts.extra)
// for the upstream relay circuit-breaker policy. Only API-key accounts honour
// it: those are the accounts that point at another relay / NewAPI / Sub2API.
const UpstreamRelayPolicyExtraKey = "upstream_relay_policy"

// UpstreamRelayBreakerReasonKeyword marks cooldowns written by the breaker.
const UpstreamRelayBreakerReasonKeyword = "upstream_relay_breaker"

const (
	defaultUpstreamRelayWindowSeconds      = 60
	defaultUpstreamRelayMinFailures        = 5
	defaultUpstreamRelayFailureRatePercent = 50
	defaultUpstreamRelayMaxCooldownSeconds = 600
	defaultUpstreamRelayLevelResetSeconds  = 1800
	// upstreamRelayTripDedupe absorbs near-simultaneous trips from concurrent
	// requests/instances so one burst escalates the backoff only once.
	upstreamRelayTripDedupe = 2 * time.Second
)

// upstreamRelayBackoffSteps is the cooldown ladder; levels beyond it double
// the previous step until the configured maximum.
var upstreamRelayBackoffSteps = []time.Duration{5 * time.Second, 15 * time.Second, time.Minute, 5 * time.Minute}

// IsUpstreamRelayPolicyEnabled reports whether the account opted into the
// upstream relay circuit-breaker policy.
func (a *Account) IsUpstreamRelayPolicyEnabled() bool {
	if a == nil || a.Type != AccountTypeAPIKey || a.Extra == nil {
		return false
	}
	switch v := a.Extra[UpstreamRelayPolicyExtraKey].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	}
	return false
}

// UpstreamRelayBreakerSettings is the resolved breaker configuration.
type UpstreamRelayBreakerSettings struct {
	Window             time.Duration
	MinFailures        int
	FailureRatePercent int
	MaxCooldown        time.Duration
	LevelReset         time.Duration
	ModelScope         bool
}

func (s *RateLimitService) upstreamRelayBreakerSettings() UpstreamRelayBreakerSettings {
	out := UpstreamRelayBreakerSettings{
		Window:             defaultUpstreamRelayWindowSeconds * time.Second,
		MinFailures:        defaultUpstreamRelayMinFailures,
		FailureRatePercent: defaultUpstreamRelayFailureRatePercent,
		MaxCooldown:        defaultUpstreamRelayMaxCooldownSeconds * time.Second,
		LevelReset:         defaultUpstreamRelayLevelResetSeconds * time.Second,
		ModelScope:         true,
	}
	if s == nil || s.cfg == nil {
		return out
	}
	c := s.cfg.RateLimit.UpstreamRelay
	if c.WindowSeconds > 0 {
		out.Window = time.Duration(c.WindowSeconds) * time.Second
	}
	if c.MinFailures > 0 {
		out.MinFailures = c.MinFailures
	}
	if c.FailureRatePercent > 0 && c.FailureRatePercent <= 100 {
		out.FailureRatePercent = c.FailureRatePercent
	}
	if c.MaxCooldownSeconds > 0 {
		out.MaxCooldown = time.Duration(c.MaxCooldownSeconds) * time.Second
	}
	if c.LevelResetSeconds > 0 {
		out.LevelReset = time.Duration(c.LevelResetSeconds) * time.Second
	}
	if strings.EqualFold(strings.TrimSpace(c.Scope), TempUnschedScopeAccount) {
		out.ModelScope = false
	}
	return out
}

// upstreamRelayCooldownForLevel returns the exponential backoff for the n-th
// consecutive trip (1-based), capped by max.
func upstreamRelayCooldownForLevel(level int, max time.Duration) time.Duration {
	if level < 1 {
		level = 1
	}
	var d time.Duration
	if level <= len(upstreamRelayBackoffSteps) {
		d = upstreamRelayBackoffSteps[level-1]
	} else {
		d = upstreamRelayBackoffSteps[len(upstreamRelayBackoffSteps)-1]
		for i := len(upstreamRelayBackoffSteps); i < level && (max <= 0 || d < max); i++ {
			d *= 2
		}
	}
	if max > 0 && d > max {
		d = max
	}
	return d
}

// isUpstreamRelayBreakerStatus reports which upstream statuses count toward the
// breaker: 429 and every 5xx. 4xx client/param errors never do; 402 and
// 401/403 keep their dedicated handling.
func isUpstreamRelayBreakerStatus(statusCode int) bool {
	return statusCode == http.StatusTooManyRequests || (statusCode >= 500 && statusCode <= 599)
}

// UpstreamRelayOutcome is the breaker window state after recording one result.
type UpstreamRelayOutcome struct {
	Failures int64
	Total    int64
	// Engaged is true while the breaker has tripped and not yet been closed by
	// a success (open or half-open).
	Engaged bool
}

// UpstreamRelayBreakerStore keeps the sliding window and backoff level. The
// Redis implementation shares them across gateway instances; the in-process
// store is the fallback when no shared cache is wired.
type UpstreamRelayBreakerStore interface {
	// RecordUpstreamRelayOutcome records a result in the window. A success with
	// no failures in the window and no engaged breaker writes nothing.
	RecordUpstreamRelayOutcome(ctx context.Context, accountID int64, scope string, success bool, window time.Duration) (UpstreamRelayOutcome, error)
	// TripUpstreamRelayBreaker escalates the backoff level (deduped within
	// upstreamRelayTripDedupe) and clears the window. Returns the level.
	TripUpstreamRelayBreaker(ctx context.Context, accountID int64, scope string, levelTTL time.Duration) (int, error)
	// CloseUpstreamRelayBreaker resets the window and backoff level.
	CloseUpstreamRelayBreaker(ctx context.Context, accountID int64, scope string) error
}

// CooldownFallbackProbeLocker grants at most one all-cooled fallback probe per
// key and interval across instances.
type CooldownFallbackProbeLocker interface {
	TryAcquireCooldownFallbackProbe(ctx context.Context, key string, ttl time.Duration) (bool, error)
}

func (s *RateLimitService) relayBreakerStore() UpstreamRelayBreakerStore {
	if s == nil {
		return nil
	}
	if store, ok := s.tempUnschedCache.(UpstreamRelayBreakerStore); ok && store != nil {
		return store
	}
	s.localRelayOnce.Do(func() { s.localRelayStore = newLocalUpstreamRelayBreakerStore() })
	return s.localRelayStore
}

// SetUpstreamRelayBreakerStore overrides the breaker store (tests / wiring).
func (s *RateLimitService) SetUpstreamRelayBreakerStore(store UpstreamRelayBreakerStore) {
	if s == nil {
		return
	}
	s.localRelayOnce.Do(func() {})
	s.localRelayStore = store
}

func upstreamRelayScopeKey(modelScope bool, model string) string {
	model = strings.TrimSpace(model)
	if !modelScope || model == "" {
		return "account"
	}
	return "model:" + model
}

// observeUpstreamRelayFailure feeds one breaker-eligible failure (429/5xx/
// timeout) of an opted-in account into the sliding window and, once tripped,
// applies an exponential-backoff cooldown. It returns true when the account
// (or model) was cooled down, so the current request fails over.
func (s *RateLimitService) observeUpstreamRelayFailure(ctx context.Context, account *Account, statusCode int, upstreamMsg string, model string) bool {
	if s == nil || account == nil || s.accountRepo == nil {
		return false
	}
	store := s.relayBreakerStore()
	if store == nil {
		return false
	}
	settings := s.upstreamRelayBreakerSettings()
	modelScoped := settings.ModelScope && strings.TrimSpace(model) != ""
	scope := upstreamRelayScopeKey(modelScoped, model)

	outcome, err := store.RecordUpstreamRelayOutcome(ctx, account.ID, scope, false, settings.Window)
	if err != nil {
		slog.Warn("upstream_relay_breaker_record_failed", "account_id", account.ID, "scope", scope, "error", err)
		return false
	}
	// Half-open/open: the trial request failed, so re-open immediately with the
	// next backoff level. Closed: trip only past both thresholds.
	if !outcome.Engaged {
		if outcome.Failures < int64(settings.MinFailures) {
			return false
		}
		if outcome.Total <= 0 || outcome.Failures*100 < int64(settings.FailureRatePercent)*outcome.Total {
			return false
		}
	}
	level, err := store.TripUpstreamRelayBreaker(ctx, account.ID, scope, settings.LevelReset)
	if err != nil {
		slog.Warn("upstream_relay_breaker_trip_failed", "account_id", account.ID, "scope", scope, "error", err)
		return false
	}
	cooldown := upstreamRelayCooldownForLevel(level, settings.MaxCooldown)
	now := time.Now()
	until := now.Add(cooldown)
	state := &TempUnschedState{
		UntilUnix:            until.Unix(),
		TriggeredAtUnix:      now.Unix(),
		StatusCode:           statusCode,
		MatchedKeyword:       UpstreamRelayBreakerReasonKeyword,
		RuleIndex:            -1,
		ErrorMessage:         truncateTempUnschedMessage([]byte(upstreamMsg), 512),
		TriggerCount:         outcome.Failures,
		TriggerThreshold:     settings.MinFailures,
		TriggerWindowSeconds: int(settings.Window / time.Second),
		BreakerLevel:         level,
		Scope:                TempUnschedScopeAccount,
	}
	if modelScoped {
		state.Scope = TempUnschedScopeModel
		state.Model = strings.TrimSpace(model)
	}
	reason := upstreamMsg
	if raw, err := json.Marshal(state); err == nil {
		reason = string(raw)
	}

	if modelScoped {
		setAccountModelRateLimitSnapshot(account, state.Model, until, reason, now)
		if err := s.accountRepo.SetModelRateLimit(ctx, account.ID, state.Model, until, reason); err != nil {
			slog.Warn("upstream_relay_breaker_model_cooldown_failed", "account_id", account.ID, "model", state.Model, "error", err)
		}
	} else {
		account.TempUnschedulableUntil = &until
		account.TempUnschedulableReason = reason
		s.notifyAccountSchedulingBlocked(account, until, UpstreamRelayBreakerReasonKeyword)
		if err := s.accountRepo.SetTempUnschedulable(ctx, account.ID, until, reason); err != nil {
			slog.Warn("upstream_relay_breaker_account_cooldown_failed", "account_id", account.ID, "error", err)
		} else if s.tempUnschedCache != nil {
			if err := s.tempUnschedCache.SetTempUnsched(ctx, account.ID, state); err != nil {
				slog.Warn("upstream_relay_breaker_cache_set_failed", "account_id", account.ID, "error", err)
			}
		}
	}
	slog.Warn("upstream_relay_breaker_tripped",
		"account_id", account.ID,
		"scope", scope,
		"status_code", statusCode,
		"failures", outcome.Failures,
		"total", outcome.Total,
		"half_open_retrip", outcome.Engaged,
		"level", level,
		"cooldown", cooldown.String(),
		"until", until.UTC())
	return true
}

// ObserveUpstreamRelayTransportFailure counts a timeout / transient transport
// failure of an opted-in account toward the breaker (account scope: the
// failure is not model specific).
func (s *RateLimitService) ObserveUpstreamRelayTransportFailure(ctx context.Context, account *Account, message string) bool {
	if account == nil || !account.IsUpstreamRelayPolicyEnabled() {
		return false
	}
	return s.observeUpstreamRelayFailure(ctx, account, http.StatusGatewayTimeout, message, "")
}

// observeUpstreamRelaySuccess closes an engaged breaker (half-open trial
// succeeded) and otherwise counts the success in an active failure window.
func (s *RateLimitService) observeUpstreamRelaySuccess(ctx context.Context, account *Account, models []string) {
	store := s.relayBreakerStore()
	if store == nil || account == nil {
		return
	}
	settings := s.upstreamRelayBreakerSettings()
	scopes := []string{"account"}
	if settings.ModelScope {
		for _, m := range models {
			if m = strings.TrimSpace(m); m != "" {
				scopes = append(scopes, upstreamRelayScopeKey(true, m))
			}
		}
	}
	for _, scope := range dedupeStrings(scopes) {
		outcome, err := store.RecordUpstreamRelayOutcome(ctx, account.ID, scope, true, settings.Window)
		if err != nil {
			slog.Warn("upstream_relay_breaker_record_success_failed", "account_id", account.ID, "scope", scope, "error", err)
			continue
		}
		if outcome.Engaged {
			if err := store.CloseUpstreamRelayBreaker(ctx, account.ID, scope); err != nil {
				slog.Warn("upstream_relay_breaker_close_failed", "account_id", account.ID, "scope", scope, "error", err)
				continue
			}
			slog.Info("upstream_relay_breaker_closed", "account_id", account.ID, "scope", scope)
		}
	}
}

// localUpstreamRelayBreakerStore is the single-process fallback store.
type localUpstreamRelayBreakerStore struct {
	mu     sync.Mutex
	now    func() time.Time
	states map[string]*localUpstreamRelayState
}

type localUpstreamRelayState struct {
	failures  []time.Time
	successes []time.Time
	level     int
	lastTrip  time.Time
	levelExp  time.Time
}

func newLocalUpstreamRelayBreakerStore() *localUpstreamRelayBreakerStore {
	return &localUpstreamRelayBreakerStore{now: time.Now, states: make(map[string]*localUpstreamRelayState)}
}

func (l *localUpstreamRelayBreakerStore) key(accountID int64, scope string) string {
	return fmt.Sprintf("%d|%s", accountID, scope)
}

func trimTimes(ts []time.Time, cutoff time.Time) []time.Time {
	i := 0
	for i < len(ts) && !ts[i].After(cutoff) {
		i++
	}
	return ts[i:]
}

func (l *localUpstreamRelayBreakerStore) RecordUpstreamRelayOutcome(_ context.Context, accountID int64, scope string, success bool, window time.Duration) (UpstreamRelayOutcome, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	k := l.key(accountID, scope)
	st := l.states[k]
	if st != nil && st.level > 0 && !st.levelExp.IsZero() && now.After(st.levelExp) {
		st.level = 0
	}
	if st != nil {
		cutoff := now.Add(-window)
		st.failures = trimTimes(st.failures, cutoff)
		st.successes = trimTimes(st.successes, cutoff)
	}
	if success {
		if st == nil || (len(st.failures) == 0 && st.level == 0) {
			return UpstreamRelayOutcome{}, nil
		}
		st.successes = append(st.successes, now)
	} else {
		if st == nil {
			st = &localUpstreamRelayState{}
			l.states[k] = st
		}
		st.failures = append(st.failures, now)
	}
	f := int64(len(st.failures))
	return UpstreamRelayOutcome{Failures: f, Total: f + int64(len(st.successes)), Engaged: st.level > 0}, nil
}

func (l *localUpstreamRelayBreakerStore) TripUpstreamRelayBreaker(_ context.Context, accountID int64, scope string, levelTTL time.Duration) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	k := l.key(accountID, scope)
	st := l.states[k]
	if st == nil {
		st = &localUpstreamRelayState{}
		l.states[k] = st
	}
	if st.level > 0 && !st.levelExp.IsZero() && now.After(st.levelExp) {
		st.level = 0
	}
	if st.level == 0 || now.Sub(st.lastTrip) >= upstreamRelayTripDedupe {
		st.level++
		st.lastTrip = now
	}
	st.levelExp = now.Add(levelTTL)
	st.failures = nil
	st.successes = nil
	return st.level, nil
}

func (l *localUpstreamRelayBreakerStore) CloseUpstreamRelayBreaker(_ context.Context, accountID int64, scope string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.states, l.key(accountID, scope))
	return nil
}

// localCooldownFallbackLocker is the in-process fallback for the probe lock.
type localCooldownFallbackLocker struct {
	mu    sync.Mutex
	now   func() time.Time
	until map[string]time.Time
}

func newLocalCooldownFallbackLocker() *localCooldownFallbackLocker {
	return &localCooldownFallbackLocker{now: time.Now, until: make(map[string]time.Time)}
}

func (l *localCooldownFallbackLocker) TryAcquireCooldownFallbackProbe(_ context.Context, key string, ttl time.Duration) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if until, ok := l.until[key]; ok && now.Before(until) {
		return false, nil
	}
	if len(l.until) > 4096 {
		for k, v := range l.until {
			if !now.Before(v) {
				delete(l.until, k)
			}
		}
	}
	l.until[key] = now.Add(ttl)
	return true, nil
}

func relayBreakerUpstreamMessage(responseBody []byte) string {
	msg := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(responseBody)))
	if msg == "" {
		return ""
	}
	return truncateForLog([]byte(msg), 512)
}
