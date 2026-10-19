//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type paymentRequiredClearRepoStub struct {
	rateLimitAccountRepoStub
	clearTempCalls int
}

func (r *paymentRequiredClearRepoStub) ClearTempUnschedulable(ctx context.Context, id int64) error {
	r.clearTempCalls++
	return nil
}

func decodeTempUnschedReason(t *testing.T, reason string) TempUnschedState {
	t.Helper()
	var state TempUnschedState
	require.NoError(t, json.Unmarshal([]byte(reason), &state))
	return state
}

func TestHandleUpstreamError_Generic402IsRecoverableCooldown(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{ID: 11, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}

	before := time.Now()
	shouldDisable := svc.HandleUpstreamError(context.Background(), account, http.StatusPaymentRequired, http.Header{},
		[]byte(`{"error":{"message":"余额不足，请充值"}}`))

	require.True(t, shouldDisable)
	require.Zero(t, repo.setErrorCalls, "402 must not permanently set status=error")
	require.Equal(t, 1, repo.tempCalls)
	state := decodeTempUnschedReason(t, repo.lastTempReason)
	require.Equal(t, 402, state.StatusCode)
	require.Equal(t, PaymentRequired402ReasonKeyword, state.MatchedKeyword)
	require.Equal(t, TempUnschedScopeAccount, state.Scope)
	require.Contains(t, state.ErrorMessage, "余额不足")
	until := time.Unix(state.UntilUnix, 0)
	require.WithinDuration(t, before.Add(30*time.Minute), until, 5*time.Second)
}

func TestHandleUpstreamError_402CooldownIsConfigurable(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	cfg := &config.Config{}
	cfg.RateLimit.PaymentRequired402CooldownMinutes = 5
	svc := NewRateLimitService(repo, nil, cfg, nil, nil)
	account := &Account{ID: 12, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}

	before := time.Now()
	svc.HandleUpstreamError(context.Background(), account, http.StatusPaymentRequired, http.Header{}, []byte(`{"error":{"message":"quota exhausted"}}`))

	state := decodeTempUnschedReason(t, repo.lastTempReason)
	require.WithinDuration(t, before.Add(5*time.Minute), time.Unix(state.UntilUnix, 0), 5*time.Second)
}

func TestHandleUpstreamError_OpenAIDeactivatedWorkspaceStaysPermanent(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{ID: 13, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}

	shouldDisable := svc.HandleUpstreamError(context.Background(), account, http.StatusPaymentRequired, http.Header{},
		[]byte(`{"detail":{"code":"deactivated_workspace"}}`))

	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.setErrorCalls)
	require.Contains(t, repo.lastErrorMsg, "Workspace deactivated (402)")
	require.Zero(t, repo.tempCalls)
}

func TestHandleUpstreamError_PoolMode402IsCooledDown(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{ID: 14, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"pool_mode": true}}

	shouldDisable := svc.HandleUpstreamError(context.Background(), account, http.StatusPaymentRequired, http.Header{},
		[]byte(`{"error":{"message":"insufficient balance"}}`))

	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.tempCalls)
	require.Zero(t, repo.setErrorCalls)
	require.Equal(t, PaymentRequired402ReasonKeyword, decodeTempUnschedReason(t, repo.lastTempReason).MatchedKeyword)
}

func TestHandleUpstreamError_PoolModeOther4xxStillSkipped(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{ID: 15, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"pool_mode": true}}

	require.False(t, svc.HandleUpstreamError(context.Background(), account, http.StatusForbidden, http.Header{}, []byte(`{"error":{"message":"forbidden"}}`)))
	require.Zero(t, repo.tempCalls)
	require.Zero(t, repo.setErrorCalls)
}

func TestHandleUpstreamError_402UserRuleKeepsPriority(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{ID: 16, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{
			"pool_mode":                  true,
			"temp_unschedulable_enabled": true,
			"temp_unschedulable_rules": []any{map[string]any{
				"error_code": 402, "keywords": []any{"balance"}, "duration_minutes": 3,
			}},
		}}

	require.True(t, svc.HandleUpstreamError(context.Background(), account, http.StatusPaymentRequired, http.Header{},
		[]byte(`{"error":{"message":"insufficient balance"}}`)))
	state := decodeTempUnschedReason(t, repo.lastTempReason)
	require.Equal(t, "balance", state.MatchedKeyword)
	require.Equal(t, 0, state.RuleIndex)
	require.Equal(t, 1, repo.tempCalls)
}

func TestCheckErrorPolicy_PoolMode402TempUnscheduled(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{ID: 17, Platform: PlatformGemini, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"pool_mode": true}}

	require.Equal(t, ErrorPolicyTempUnscheduled, svc.CheckErrorPolicy(context.Background(), account, http.StatusPaymentRequired, []byte(`{"error":{"message":"no credit"}}`)))
	require.Equal(t, ErrorPolicySkipped, svc.CheckErrorPolicy(context.Background(), account, http.StatusForbidden, []byte(`{}`)))
	require.Equal(t, 1, repo.tempCalls)
}

func TestClassifyAccountOpsFailure_402IsBalanceLow(t *testing.T) {
	kind, signal := classifyAccountOpsFailure(PlatformAnthropic, http.StatusPaymentRequired, http.Header{}, []byte(`{"error":{"message":"payment required"}}`))
	require.Equal(t, "balance_low", kind)
	require.Equal(t, "payment_required_402", signal)

	kind, _ = classifyAccountOpsFailure(PlatformOpenAI, http.StatusPaymentRequired, http.Header{}, []byte(`{"detail":{"code":"deactivated_workspace"}}`))
	require.Empty(t, kind)

	// A more specific signal still wins.
	_, signal = classifyAccountOpsFailure(PlatformAnthropic, http.StatusPaymentRequired, http.Header{}, []byte(`{"error":{"code":"insufficient_balance"}}`))
	require.Equal(t, "balance_error_code", signal)
}

func TestUpstreamBalanceSnapshotIsPositive(t *testing.T) {
	ok := func(data map[string]any) *UpstreamBalanceSnapshot {
		return &UpstreamBalanceSnapshot{Status: UpstreamBillingProbeStatusOK, Data: data}
	}
	require.True(t, upstreamBalanceSnapshotIsPositive(ok(map[string]any{"is_valid": true, "wallet_balance": 2.5})))
	require.True(t, upstreamBalanceSnapshotIsPositive(ok(map[string]any{"is_valid": true, "remaining": 1.0})))
	require.True(t, upstreamBalanceSnapshotIsPositive(ok(map[string]any{"is_valid": true, "unlimited": true})))
	require.False(t, upstreamBalanceSnapshotIsPositive(ok(map[string]any{"is_valid": true, "wallet_balance": 0.0})))
	require.False(t, upstreamBalanceSnapshotIsPositive(ok(map[string]any{"is_valid": false, "wallet_balance": 3.0})))
	require.False(t, upstreamBalanceSnapshotIsPositive(&UpstreamBalanceSnapshot{Status: UpstreamBillingProbeStatusFailed, Data: map[string]any{"wallet_balance": 3.0}}))
	require.False(t, upstreamBalanceSnapshotIsPositive(nil))
}

func TestClearPaymentRequired402CooldownIfRecovered(t *testing.T) {
	until := time.Now().Add(10 * time.Minute)
	reason, _ := json.Marshal(TempUnschedState{StatusCode: 402, MatchedKeyword: PaymentRequired402ReasonKeyword, UntilUnix: until.Unix()})
	positive := &UpstreamBillingProbeSnapshot{Balance: &UpstreamBalanceSnapshot{Status: UpstreamBillingProbeStatusOK, Data: map[string]any{"is_valid": true, "wallet_balance": 4.0}}}
	empty := &UpstreamBillingProbeSnapshot{Balance: &UpstreamBalanceSnapshot{Status: UpstreamBillingProbeStatusOK, Data: map[string]any{"is_valid": true, "wallet_balance": 0.0}}}

	t.Run("positive balance clears 402 cooldown", func(t *testing.T) {
		repo := &paymentRequiredClearRepoStub{}
		probe := NewUpstreamBillingProbeService(repo, nil, nil)
		account := &Account{ID: 21, TempUnschedulableUntil: &until, TempUnschedulableReason: string(reason)}
		probe.clearPaymentRequired402CooldownIfRecovered(context.Background(), account, positive)
		require.Equal(t, 1, repo.clearTempCalls)
	})
	t.Run("zero balance keeps cooldown", func(t *testing.T) {
		repo := &paymentRequiredClearRepoStub{}
		probe := NewUpstreamBillingProbeService(repo, nil, nil)
		account := &Account{ID: 22, TempUnschedulableUntil: &until, TempUnschedulableReason: string(reason)}
		probe.clearPaymentRequired402CooldownIfRecovered(context.Background(), account, empty)
		require.Zero(t, repo.clearTempCalls)
	})
	t.Run("other cooldown reasons untouched", func(t *testing.T) {
		repo := &paymentRequiredClearRepoStub{}
		probe := NewUpstreamBillingProbeService(repo, nil, nil)
		other, _ := json.Marshal(TempUnschedState{StatusCode: 429, MatchedKeyword: "rate", UntilUnix: until.Unix()})
		account := &Account{ID: 23, TempUnschedulableUntil: &until, TempUnschedulableReason: string(other)}
		probe.clearPaymentRequired402CooldownIfRecovered(context.Background(), account, positive)
		require.Zero(t, repo.clearTempCalls)
	})
}
