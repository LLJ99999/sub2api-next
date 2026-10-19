//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestObserveUpstreamSuccess_ClearsResidualStateForAPIKeyAccounts(t *testing.T) {
	svc, repo, _ := newRelayTestService(t)
	past := time.Now().Add(-time.Minute)
	future := time.Now().Add(time.Minute)
	account := &Account{ID: 41, Platform: PlatformGemini, Type: AccountTypeAPIKey, RateLimitResetAt: &past, TempUnschedulableUntil: &future,
		TempUnschedulableReason: `{"status_code":503}`,
		Extra: map[string]any{modelRateLimitsKey: map[string]any{
			"gemini-2.5-pro": map[string]any{"rate_limit_reset_at": future.UTC().Format(time.RFC3339)},
			"other-model":    map[string]any{"rate_limit_reset_at": future.UTC().Format(time.RFC3339)},
		}}}

	svc.ObserveUpstreamSuccess(context.Background(), account, "gemini-2.5-pro")

	require.Equal(t, 1, repo.clearRateCalls)
	require.Equal(t, 1, repo.clearTempCalls)
	require.Equal(t, []string{"gemini-2.5-pro"}, repo.clearedModelScopes)
	require.Nil(t, account.RateLimitResetAt)
	require.Nil(t, account.TempUnschedulableUntil)
}

func TestObserveUpstreamSuccess_NoStateNoWrites(t *testing.T) {
	svc, repo, _ := newRelayTestService(t)
	svc.ObserveUpstreamSuccess(context.Background(), &Account{ID: 42, Type: AccountTypeAPIKey, Platform: PlatformOpenAI}, "gpt-4o")
	require.Zero(t, repo.clearRateCalls)
	require.Zero(t, repo.clearTempCalls)
	require.Empty(t, repo.clearedModelScopes)
}

func TestObserveUpstreamSuccess_OAuthUntouched(t *testing.T) {
	svc, repo, _ := newRelayTestService(t)
	past := time.Now().Add(-time.Minute)
	svc.ObserveUpstreamSuccess(context.Background(), &Account{ID: 43, Type: AccountTypeOAuth, Platform: PlatformAnthropic, RateLimitResetAt: &past}, "claude")
	require.Zero(t, repo.clearRateCalls)
}

func TestObserveUpstreamSuccess_KeepsActiveSchedulingThresholdPause(t *testing.T) {
	svc, repo, _ := newRelayTestService(t)
	future := time.Now().Add(time.Hour)
	reason := `{"source":"` + AccountSchedulingThresholdReasonSource + `","error_message":"threshold"}`
	svc.ObserveUpstreamSuccess(context.Background(), &Account{ID: 44, Type: AccountTypeAPIKey, Platform: PlatformKimi, TempUnschedulableUntil: &future, TempUnschedulableReason: reason}, "kimi")
	require.Zero(t, repo.clearTempCalls)
}
