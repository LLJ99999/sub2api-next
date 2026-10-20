//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type relayRepoStub struct {
	rateLimitAccountRepoStub
	modelLimitCalls    int
	lastModelScope     string
	lastModelReason    string
	lastModelUntil     time.Time
	lastTempUntil      time.Time
	clearRateCalls     int
	clearTempCalls     int
	clearedModelScopes []string
}

func (r *relayRepoStub) SetModelRateLimit(ctx context.Context, id int64, scope string, resetAt time.Time, reason ...string) error {
	r.modelLimitCalls++
	r.lastModelScope = scope
	r.lastModelUntil = resetAt
	if len(reason) > 0 {
		r.lastModelReason = reason[0]
	}
	return nil
}

func (r *relayRepoStub) SetTempUnschedulable(ctx context.Context, id int64, until time.Time, reason string) error {
	r.lastTempUntil = until
	return r.rateLimitAccountRepoStub.SetTempUnschedulable(ctx, id, until, reason)
}

func (r *relayRepoStub) ClearRateLimit(ctx context.Context, id int64) error {
	r.clearRateCalls++
	return nil
}

func (r *relayRepoStub) ClearTempUnschedulable(ctx context.Context, id int64) error {
	r.clearTempCalls++
	return nil
}

func (r *relayRepoStub) ClearModelRateLimitScope(ctx context.Context, id int64, scope string) error {
	r.clearedModelScopes = append(r.clearedModelScopes, scope)
	return nil
}

type relayTestClock struct{ now time.Time }

func (c *relayTestClock) Now() time.Time          { return c.now }
func (c *relayTestClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func newRelayTestService(t *testing.T) (*RateLimitService, *relayRepoStub, *relayTestClock) {
	t.Helper()
	repo := &relayRepoStub{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	clock := &relayTestClock{now: time.Unix(1_700_000_000, 0)}
	store := newLocalUpstreamRelayBreakerStore()
	store.now = clock.Now
	svc.SetUpstreamRelayBreakerStore(store)
	return svc, repo, clock
}

func relayAccount(id int64) *Account {
	return &Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Extra: map[string]any{UpstreamRelayPolicyExtraKey: true}}
}

func TestIsUpstreamRelayPolicyEnabled(t *testing.T) {
	require.True(t, relayAccount(1).IsUpstreamRelayPolicyEnabled())
	require.True(t, (&Account{Type: AccountTypeAPIKey, Extra: map[string]any{UpstreamRelayPolicyExtraKey: "true"}}).IsUpstreamRelayPolicyEnabled())
	require.False(t, (&Account{Type: AccountTypeOAuth, Extra: map[string]any{UpstreamRelayPolicyExtraKey: true}}).IsUpstreamRelayPolicyEnabled())
	require.False(t, (&Account{Type: AccountTypeAPIKey}).IsUpstreamRelayPolicyEnabled())
	require.False(t, (*Account)(nil).IsUpstreamRelayPolicyEnabled())
}

func TestUpstreamRelayCooldownLadder(t *testing.T) {
	max := 10 * time.Minute
	want := []time.Duration{5 * time.Second, 15 * time.Second, time.Minute, 5 * time.Minute, 10 * time.Minute, 10 * time.Minute}
	for i, w := range want {
		require.Equal(t, w, upstreamRelayCooldownForLevel(i+1, max), "level %d", i+1)
	}
	require.Equal(t, 30*time.Second, upstreamRelayCooldownForLevel(3, 30*time.Second))
}

func TestUpstreamRelayBreaker_TripsOnlyAfterThreshold(t *testing.T) {
	svc, repo, _ := newRelayTestService(t)
	account := relayAccount(31)
	body := []byte(`{"error":{"message":"upstream overloaded"}}`)
	headers := http.Header{"Retry-After": []string{"3600"}}

	for i := 0; i < 4; i++ {
		require.False(t, svc.HandleUpstreamError(context.Background(), account, http.StatusServiceUnavailable, headers, body, "gpt-4o"))
	}
	require.Zero(t, repo.modelLimitCalls, "a few transient errors must not cool down the upstream")

	before := time.Now()
	require.True(t, svc.HandleUpstreamError(context.Background(), account, http.StatusTooManyRequests, headers, body, "gpt-4o"))
	require.Equal(t, 1, repo.modelLimitCalls)
	require.Equal(t, "gpt-4o", repo.lastModelScope)
	// Level 1 backoff, ignoring the upstream's one-hour Retry-After.
	require.WithinDuration(t, before.Add(5*time.Second), repo.lastModelUntil, 2*time.Second)
	require.Zero(t, repo.rateLimitedCalls)
	require.Zero(t, repo.tempCalls)
	state := decodeTempUnschedReason(t, repo.lastModelReason)
	require.Equal(t, UpstreamRelayBreakerReasonKeyword, state.MatchedKeyword)
	require.Equal(t, 1, state.BreakerLevel)
	require.Equal(t, TempUnschedScopeModel, state.Scope)
	require.Equal(t, "gpt-4o", state.Model)
	require.EqualValues(t, 5, state.TriggerCount)
	require.Equal(t, http.StatusTooManyRequests, state.StatusCode)
}

func TestUpstreamRelayBreaker_LowFailureRateDoesNotTrip(t *testing.T) {
	svc, repo, _ := newRelayTestService(t)
	account := relayAccount(32)
	body := []byte(`{"error":{"message":"bad gateway"}}`)

	require.False(t, svc.HandleUpstreamError(context.Background(), account, http.StatusBadGateway, http.Header{}, body, "m1"))
	for i := 0; i < 6; i++ {
		svc.ObserveUpstreamSuccess(context.Background(), account, "m1")
	}
	for i := 0; i < 4; i++ {
		require.False(t, svc.HandleUpstreamError(context.Background(), account, http.StatusBadGateway, http.Header{}, body, "m1"))
	}
	// 5 failures out of 11 results (45%) stays below the 50% threshold.
	require.Zero(t, repo.modelLimitCalls)
}

func TestUpstreamRelayBreaker_ClientErrorsDoNotCount(t *testing.T) {
	svc, repo, _ := newRelayTestService(t)
	account := relayAccount(33)
	for i := 0; i < 10; i++ {
		svc.HandleUpstreamError(context.Background(), account, http.StatusBadRequest, http.Header{}, []byte(`{"error":{"message":"invalid parameter"}}`), "m1")
	}
	require.Zero(t, repo.modelLimitCalls)
	require.Zero(t, repo.tempCalls)
	require.Zero(t, repo.setErrorCalls)
	require.False(t, svc.HandleUpstreamError(context.Background(), account, http.StatusServiceUnavailable, http.Header{}, []byte(`{}`), "m1"))
}

func TestUpstreamRelayBreaker_AccountScopeWithoutModel(t *testing.T) {
	svc, repo, _ := newRelayTestService(t)
	account := relayAccount(34)
	for i := 0; i < 4; i++ {
		require.False(t, svc.ObserveUpstreamRelayTransportFailure(context.Background(), account, "i/o timeout"))
	}
	require.True(t, svc.ObserveUpstreamRelayTransportFailure(context.Background(), account, "i/o timeout"))
	require.Equal(t, 1, repo.tempCalls)
	require.Zero(t, repo.modelLimitCalls)
	state := decodeTempUnschedReason(t, repo.lastTempReason)
	require.Equal(t, TempUnschedScopeAccount, state.Scope)
	require.Equal(t, http.StatusGatewayTimeout, state.StatusCode)
	require.NotNil(t, account.TempUnschedulableUntil)
}

func TestUpstreamRelayBreaker_ScopeAccountConfig(t *testing.T) {
	svc, repo, _ := newRelayTestService(t)
	svc.cfg.RateLimit.UpstreamRelay.Scope = "account"
	svc.cfg.RateLimit.UpstreamRelay.MinFailures = 2
	account := relayAccount(35)
	require.False(t, svc.HandleUpstreamError(context.Background(), account, http.StatusInternalServerError, http.Header{}, []byte(`{}`), "m1"))
	require.True(t, svc.HandleUpstreamError(context.Background(), account, http.StatusInternalServerError, http.Header{}, []byte(`{}`), "m1"))
	require.Equal(t, 1, repo.tempCalls)
	require.Zero(t, repo.modelLimitCalls)
}

func TestUpstreamRelayBreaker_HalfOpenFailureReopensWithBackoff(t *testing.T) {
	svc, repo, clock := newRelayTestService(t)
	account := relayAccount(36)
	body := []byte(`{"error":{"message":"overloaded"}}`)
	for i := 0; i < 5; i++ {
		svc.HandleUpstreamError(context.Background(), account, http.StatusServiceUnavailable, http.Header{}, body, "m1")
	}
	require.Equal(t, 1, repo.modelLimitCalls)

	// Cooldown elapsed: the first trial request fails → re-open immediately at level 2.
	clock.Advance(6 * time.Second)
	before := time.Now()
	require.True(t, svc.HandleUpstreamError(context.Background(), account, http.StatusServiceUnavailable, http.Header{}, body, "m1"))
	require.Equal(t, 2, repo.modelLimitCalls)
	require.Equal(t, 2, decodeTempUnschedReason(t, repo.lastModelReason).BreakerLevel)
	require.WithinDuration(t, before.Add(15*time.Second), repo.lastModelUntil, 2*time.Second)
}

func TestUpstreamRelayBreaker_HalfOpenSuccessClosesAndResetsBackoff(t *testing.T) {
	svc, repo, clock := newRelayTestService(t)
	account := relayAccount(37)
	body := []byte(`{}`)
	for i := 0; i < 5; i++ {
		svc.HandleUpstreamError(context.Background(), account, http.StatusServiceUnavailable, http.Header{}, body, "m1")
	}
	require.Equal(t, 1, repo.modelLimitCalls)
	clock.Advance(6 * time.Second)

	svc.ObserveUpstreamSuccess(context.Background(), account, "m1")

	// Closed again: a single failure no longer trips, and the next trip starts at level 1.
	require.False(t, svc.HandleUpstreamError(context.Background(), account, http.StatusServiceUnavailable, http.Header{}, body, "m1"))
	for i := 0; i < 4; i++ {
		svc.HandleUpstreamError(context.Background(), account, http.StatusServiceUnavailable, http.Header{}, body, "m1")
	}
	require.Equal(t, 2, repo.modelLimitCalls)
	require.Equal(t, 1, decodeTempUnschedReason(t, repo.lastModelReason).BreakerLevel)
}

func TestUpstreamRelayBreaker_UserRuleKeepsPriority(t *testing.T) {
	svc, repo, _ := newRelayTestService(t)
	account := relayAccount(38)
	account.Credentials = map[string]any{
		"temp_unschedulable_enabled": true,
		"temp_unschedulable_rules": []any{map[string]any{
			"error_code": 429, "keywords": []any{"quota"}, "duration_minutes": 7,
		}},
	}
	require.True(t, svc.HandleUpstreamError(context.Background(), account, http.StatusTooManyRequests, http.Header{}, []byte(`{"error":{"message":"quota exceeded"}}`), "m1"))
	require.Equal(t, 1, repo.tempCalls)
	require.Equal(t, "quota", decodeTempUnschedReason(t, repo.lastTempReason).MatchedKeyword)
	require.Zero(t, repo.modelLimitCalls)
}

func TestUpstreamRelayBreaker_StreamTimeoutCounts(t *testing.T) {
	svc, repo, _ := newRelayTestService(t)
	account := relayAccount(39)
	for i := 0; i < 4; i++ {
		require.False(t, svc.HandleStreamTimeout(context.Background(), account, "m1"))
	}
	require.True(t, svc.HandleStreamTimeout(context.Background(), account, "m1"))
	require.Equal(t, 1, repo.modelLimitCalls)
}

func TestUpstreamRelayBreaker_PoolModeAccountUsesBreaker(t *testing.T) {
	svc, repo, _ := newRelayTestService(t)
	svc.cfg.RateLimit.UpstreamRelay.MinFailures = 1
	account := relayAccount(40)
	account.Credentials = map[string]any{"pool_mode": true}
	require.True(t, svc.HandleUpstreamError(context.Background(), account, http.StatusBadGateway, http.Header{}, []byte(`{}`), "m1"))
	require.Equal(t, 1, repo.modelLimitCalls)
}
