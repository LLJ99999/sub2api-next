//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type cooldownFallbackRepoStub struct {
	mockAccountRepoForGemini
	candidates []Account
}

func (r *cooldownFallbackRepoStub) ListModelAvailabilityCandidates(ctx context.Context, _ *int64, platforms []string, _ bool) ([]Account, error) {
	out := make([]Account, 0, len(r.candidates))
	for _, acc := range r.candidates {
		for _, p := range platforms {
			if acc.Platform == p {
				out = append(out, acc)
			}
		}
	}
	return out, nil
}

func cooledAPIKeyAccount(id int64, until time.Time) Account {
	return Account{ID: id, Name: fmt.Sprintf("acc-%d", id), Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true, Concurrency: 1, TempUnschedulableUntil: &until,
		TempUnschedulableReason: `{"status_code":503,"matched_keyword":"upstream_relay_breaker"}`}
}

func TestShouldTryCooldownFallback(t *testing.T) {
	ctx := context.Background()
	require.True(t, shouldTryCooldownFallback(ctx, ErrNoAvailableAccounts))
	require.True(t, shouldTryCooldownFallback(ctx, fmt.Errorf("%w supporting model: m (2 rate limited)", ErrNoAvailableAccounts)))
	require.False(t, shouldTryCooldownFallback(ctx, fmt.Errorf("%w supporting model: m (channel pricing restriction)", ErrNoAvailableAccounts)))
	require.False(t, shouldTryCooldownFallback(ctx, fmt.Errorf("boom")))
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	require.False(t, shouldTryCooldownFallback(canceled, ErrNoAvailableAccounts))
}

func TestCooldownOnlyBlockEnd(t *testing.T) {
	now := time.Now()
	future := now.Add(time.Minute)
	past := now.Add(-time.Minute)
	ctx := context.Background()

	acc := cooledAPIKeyAccount(1, future)
	end, ok := cooldownOnlyBlockEnd(ctx, &acc, "", now)
	require.True(t, ok)
	require.Equal(t, future, end)

	// Not cooled at all: excluded for some other reason, never bypassed.
	healthy := Account{ID: 2, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}
	_, ok = cooldownOnlyBlockEnd(ctx, &healthy, "", now)
	require.False(t, ok)

	disabled := cooledAPIKeyAccount(3, future)
	disabled.Status = StatusError
	_, ok = cooldownOnlyBlockEnd(ctx, &disabled, "", now)
	require.False(t, ok)

	manual := cooledAPIKeyAccount(4, future)
	manual.Schedulable = false
	_, ok = cooldownOnlyBlockEnd(ctx, &manual, "", now)
	require.False(t, ok)

	expired := cooledAPIKeyAccount(5, future)
	expired.AutoPauseOnExpired = true
	expired.ExpiresAt = &past
	_, ok = cooldownOnlyBlockEnd(ctx, &expired, "", now)
	require.False(t, ok)

	oauth := cooledAPIKeyAccount(6, future)
	oauth.Type = AccountTypeOAuth
	_, ok = cooldownOnlyBlockEnd(ctx, &oauth, "", now)
	require.False(t, ok)

	balance := cooledAPIKeyAccount(7, future)
	reason, _ := json.Marshal(TempUnschedState{StatusCode: 402, MatchedKeyword: PaymentRequired402ReasonKeyword})
	balance.TempUnschedulableReason = string(reason)
	_, ok = cooldownOnlyBlockEnd(ctx, &balance, "", now)
	require.False(t, ok, "a 402 balance cooldown is not probed")

	modelOnly := Account{ID: 8, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Extra: map[string]any{modelRateLimitsKey: map[string]any{"gpt-4o": map[string]any{"rate_limit_reset_at": future.UTC().Format(time.RFC3339)}}}}
	end, ok = cooldownOnlyBlockEnd(ctx, &modelOnly, "gpt-4o", now)
	require.True(t, ok)
	require.WithinDuration(t, future, end, 2*time.Second)
	_, ok = cooldownOnlyBlockEnd(ctx, &modelOnly, "other-model", now)
	require.False(t, ok)
}

func TestPickCooldownFallbackCandidate(t *testing.T) {
	now := time.Now()
	ctx := context.Background()
	recent := now.Add(-time.Second)
	older := now.Add(-time.Hour)

	a := cooledAPIKeyAccount(1, now.Add(5*time.Minute))
	b := cooledAPIKeyAccount(2, now.Add(30*time.Second))
	c := cooledAPIKeyAccount(3, now.Add(30*time.Second))
	b.LastUsedAt = &older
	c.LastUsedAt = &recent
	accounts := []Account{a, b, c}

	pick := pickCooldownFallbackCandidate(ctx, accounts, nil, "", now, nil)
	require.NotNil(t, pick)
	require.Equal(t, int64(3), pick.ID, "earliest reset, tie broken by most recent use")

	pick = pickCooldownFallbackCandidate(ctx, accounts, map[int64]struct{}{3: {}}, "", now, nil)
	require.Equal(t, int64(2), pick.ID, "accounts already tried in this request are skipped")

	// The eligibility check sees the account without its cooldown.
	pick = pickCooldownFallbackCandidate(ctx, accounts, nil, "", now, func(acc *Account) bool {
		require.True(t, acc.IsSchedulable())
		return acc.ID == 1
	})
	require.Equal(t, int64(1), pick.ID)
	require.NotNil(t, accounts[0].TempUnschedulableUntil, "the original account snapshot is not mutated")

	require.Nil(t, pickCooldownFallbackCandidate(ctx, accounts, nil, "", now, func(*Account) bool { return false }))
}

func TestLocalCooldownFallbackLocker(t *testing.T) {
	locker := newLocalCooldownFallbackLocker()
	clock := time.Unix(1_700_000_000, 0)
	locker.now = func() time.Time { return clock }
	ok, _ := locker.TryAcquireCooldownFallbackProbe(context.Background(), "k", 10*time.Second)
	require.True(t, ok)
	ok, _ = locker.TryAcquireCooldownFallbackProbe(context.Background(), "k", 10*time.Second)
	require.False(t, ok)
	clock = clock.Add(11 * time.Second)
	ok, _ = locker.TryAcquireCooldownFallbackProbe(context.Background(), "k", 10*time.Second)
	require.True(t, ok)
}

func newOpenAICooldownFallbackService(candidates []Account, enabled bool) *OpenAIGatewayService {
	cfg := &config.Config{}
	cfg.Gateway.CooldownFallback.Enabled = enabled
	cfg.Gateway.CooldownFallback.ProbeIntervalSeconds = 10
	repo := &cooldownFallbackRepoStub{candidates: candidates}
	return &OpenAIGatewayService{
		accountRepo:      repo,
		cfg:              cfg,
		rateLimitService: NewRateLimitService(repo, nil, cfg, nil, nil),
	}
}

func TestSelectOpenAICooldownFallback_ProbesEarliestAndThrottles(t *testing.T) {
	now := time.Now()
	svc := newOpenAICooldownFallbackService([]Account{
		cooledAPIKeyAccount(1, now.Add(2*time.Minute)),
		cooledAPIKeyAccount(2, now.Add(20*time.Second)),
	}, true)

	selection := svc.selectOpenAICooldownFallback(context.Background(), nil, "gpt-4o", nil, PlatformOpenAI)
	require.NotNil(t, selection)
	require.Equal(t, int64(2), selection.Account.ID)
	require.True(t, selection.Acquired)

	// Thundering-herd guard: only one probe per key and interval.
	require.Nil(t, svc.selectOpenAICooldownFallback(context.Background(), nil, "gpt-4o", nil, PlatformOpenAI))
	// A different model is an independent probe key.
	require.NotNil(t, svc.selectOpenAICooldownFallback(context.Background(), nil, "gpt-4.1", nil, PlatformOpenAI))
}

func TestSelectOpenAICooldownFallback_DisabledOrNothingCooled(t *testing.T) {
	now := time.Now()
	require.Nil(t, newOpenAICooldownFallbackService([]Account{cooledAPIKeyAccount(1, now.Add(time.Minute))}, false).
		selectOpenAICooldownFallback(context.Background(), nil, "gpt-4o", nil, PlatformOpenAI))

	errored := cooledAPIKeyAccount(2, now.Add(time.Minute))
	errored.Status = StatusError
	require.Nil(t, newOpenAICooldownFallbackService([]Account{errored}, true).
		selectOpenAICooldownFallback(context.Background(), nil, "gpt-4o", nil, PlatformOpenAI))
}
