//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func halfOpenRelayAccount(id int64) *Account {
	return &Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Extra: map[string]any{UpstreamRelayPolicyExtraKey: true}}
}

func TestUpstreamRelayHalfOpenScope(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	past := now.Add(-time.Second)
	future := now.Add(time.Minute)

	acc := halfOpenRelayAccount(1)
	_, ok := upstreamRelayHalfOpenScope(ctx, acc, "gpt-4o", now)
	require.False(t, ok, "no residue: not half-open")

	acc.TempUnschedulableUntil = &past
	acc.TempUnschedulableReason = `{"matched_keyword":"upstream_relay_breaker","breaker_level":1}`
	scope, ok := upstreamRelayHalfOpenScope(ctx, acc, "gpt-4o", now)
	require.True(t, ok)
	require.Equal(t, "account", scope)

	acc.TempUnschedulableUntil = &future
	_, ok = upstreamRelayHalfOpenScope(ctx, acc, "gpt-4o", now)
	require.False(t, ok, "still cooling down: not half-open")

	other := halfOpenRelayAccount(2)
	other.TempUnschedulableUntil = &past
	other.TempUnschedulableReason = `{"matched_keyword":"overloaded"}`
	_, ok = upstreamRelayHalfOpenScope(ctx, other, "gpt-4o", now)
	require.False(t, ok, "non-breaker residue is not gated")

	model := halfOpenRelayAccount(3)
	setAccountModelRateLimitSnapshot(model, "gpt-4o", past, `{"matched_keyword":"upstream_relay_breaker"}`, now.Add(-time.Minute))
	scope, ok = upstreamRelayHalfOpenScope(ctx, model, "gpt-4o", now)
	require.True(t, ok)
	require.Equal(t, "model:gpt-4o", scope)
	_, ok = upstreamRelayHalfOpenScope(ctx, model, "gpt-4.1", now)
	require.False(t, ok)

	optOut := halfOpenRelayAccount(4)
	optOut.Extra = nil
	optOut.TempUnschedulableUntil = &past
	optOut.TempUnschedulableReason = `{"matched_keyword":"upstream_relay_breaker"}`
	_, ok = upstreamRelayHalfOpenScope(ctx, optOut, "", now)
	require.False(t, ok, "policy off: not gated")
}

func TestAdmitUpstreamRelayHalfOpenTrialCapsAtTwo(t *testing.T) {
	ctx := context.Background()
	svc := &RateLimitService{}
	past := time.Now().Add(-time.Second)
	acc := halfOpenRelayAccount(10)
	acc.TempUnschedulableUntil = &past
	acc.TempUnschedulableReason = `{"matched_keyword":"upstream_relay_breaker"}`

	require.True(t, svc.AdmitUpstreamRelayHalfOpenTrial(ctx, acc, ""))
	require.True(t, svc.AdmitUpstreamRelayHalfOpenTrial(ctx, acc, ""))
	require.False(t, svc.AdmitUpstreamRelayHalfOpenTrial(ctx, acc, ""))

	svc.resetUpstreamRelayHalfOpen(ctx, acc.ID, "account")
	require.True(t, svc.AdmitUpstreamRelayHalfOpenTrial(ctx, acc, ""))

	healthy := halfOpenRelayAccount(11)
	for i := 0; i < 5; i++ {
		require.True(t, svc.AdmitUpstreamRelayHalfOpenTrial(ctx, healthy, ""))
	}
}

func TestAdmitHalfOpenSelectionReselects(t *testing.T) {
	ctx := context.Background()
	svc := &RateLimitService{}
	past := time.Now().Add(-time.Second)
	capped := halfOpenRelayAccount(20)
	capped.TempUnschedulableUntil = &past
	capped.TempUnschedulableReason = `{"matched_keyword":"upstream_relay_breaker"}`
	healthy := halfOpenRelayAccount(21)

	// Use up both trials of the capped account.
	require.True(t, svc.AdmitUpstreamRelayHalfOpenTrial(ctx, capped, ""))
	require.True(t, svc.AdmitUpstreamRelayHalfOpenTrial(ctx, capped, ""))

	released := 0
	calls := 0
	result, err := admitHalfOpenSelection(ctx, svc, "", map[int64]struct{}{99: {}}, func(excluded map[int64]struct{}) (*AccountSelectionResult, error) {
		calls++
		require.Contains(t, excluded, int64(99))
		if _, skip := excluded[capped.ID]; !skip {
			return &AccountSelectionResult{Account: capped, Acquired: true, ReleaseFunc: func() { released++ }}, nil
		}
		return &AccountSelectionResult{Account: healthy, Acquired: true, ReleaseFunc: func() {}}, nil
	})
	require.NoError(t, err)
	require.Equal(t, healthy.ID, result.Account.ID)
	require.Equal(t, 2, calls)
	require.Equal(t, 1, released, "slot of the capped account is released")

	// Selection errors pass through unchanged.
	_, err = admitHalfOpenSelection(ctx, svc, "", nil, func(map[int64]struct{}) (*AccountSelectionResult, error) {
		return nil, ErrNoAvailableAccounts
	})
	require.True(t, errors.Is(err, ErrNoAvailableAccounts))
}
