package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// PaymentRequired402ReasonKeyword marks a temp-unschedulable reason written by
// the recoverable HTTP 402 handler. The upstream balance probe uses it to lift
// the cooldown early once a positive balance is observed.
const PaymentRequired402ReasonKeyword = "payment_required_402"

const defaultPaymentRequired402CooldownMinutes = 30

// isOpenAIDeactivatedWorkspace402 reports the only 402 that stays permanent:
// an OpenAI workspace that has been deactivated cannot recover by topping up.
func isOpenAIDeactivatedWorkspace402(account *Account, responseBody []byte) bool {
	return account != nil && account.Platform == PlatformOpenAI &&
		gjson.GetBytes(responseBody, "detail.code").String() == "deactivated_workspace"
}

func (s *RateLimitService) paymentRequired402Cooldown() time.Duration {
	minutes := defaultPaymentRequired402CooldownMinutes
	if s != nil && s.cfg != nil && s.cfg.RateLimit.PaymentRequired402CooldownMinutes > 0 {
		minutes = s.cfg.RateLimit.PaymentRequired402CooldownMinutes
	}
	return time.Duration(minutes) * time.Minute
}

// handleRecoverable402 applies the recoverable 402 policy. It returns false only
// for the OpenAI deactivated_workspace case, which callers keep permanent.
func (s *RateLimitService) handleRecoverable402(ctx context.Context, account *Account, responseBody []byte, upstreamMsg string) bool {
	if account == nil {
		return false
	}
	if account.IsCNProvider() || account.IsOpenCodeZen() || account.IsCommandCode() {
		s.handleCNProviderInsufficientBalance(ctx, account, upstreamMsg)
		return true
	}
	if isOpenAIDeactivatedWorkspace402(account, responseBody) {
		return false
	}
	s.handlePaymentRequired402(ctx, account, upstreamMsg)
	return true
}

// handlePaymentRequired402 parks the account in a recoverable temp-unschedulable
// state instead of status=error. A relay upstream that runs out of balance comes
// back after a top-up; permanent disabling would require manual recovery.
func (s *RateLimitService) handlePaymentRequired402(ctx context.Context, account *Account, upstreamMsg string) {
	if s == nil || account == nil || s.accountRepo == nil {
		return
	}
	now := time.Now()
	until := now.Add(s.paymentRequired402Cooldown())
	msg := strings.TrimSpace(upstreamMsg)
	if msg == "" {
		msg = "Payment required (402): insufficient balance or billing issue"
	}
	state := &TempUnschedState{
		UntilUnix:       until.Unix(),
		TriggeredAtUnix: now.Unix(),
		StatusCode:      402,
		MatchedKeyword:  PaymentRequired402ReasonKeyword,
		RuleIndex:       -1,
		ErrorMessage:    msg,
		Scope:           TempUnschedScopeAccount,
	}
	reason := msg
	if raw, err := json.Marshal(state); err == nil {
		reason = string(raw)
	}
	account.TempUnschedulableUntil = &until
	account.TempUnschedulableReason = reason
	s.notifyAccountSchedulingBlocked(account, until, PaymentRequired402ReasonKeyword)
	if err := s.accountRepo.SetTempUnschedulable(ctx, account.ID, until, reason); err != nil {
		slog.Warn("payment_required_402_set_temp_unschedulable_failed", "account_id", account.ID, "error", err)
		return
	}
	slog.Warn("account_payment_required_402_cooldown",
		"account_id", account.ID,
		"platform", account.Platform,
		"type", account.Type,
		"until", until.UTC(),
		"upstream_msg", msg,
	)
}

// isPaymentRequired402Cooldown reports whether the account is currently parked
// by the recoverable 402 handler.
func isPaymentRequired402Cooldown(account *Account, now time.Time) bool {
	if account == nil || account.TempUnschedulableUntil == nil || !now.Before(*account.TempUnschedulableUntil) {
		return false
	}
	var state TempUnschedState
	if err := json.Unmarshal([]byte(strings.TrimSpace(account.TempUnschedulableReason)), &state); err != nil {
		return false
	}
	return state.MatchedKeyword == PaymentRequired402ReasonKeyword
}

// upstreamBalanceSnapshotIsPositive reports whether a fresh, successful balance
// probe shows money left on the upstream key/wallet.
func upstreamBalanceSnapshotIsPositive(balance *UpstreamBalanceSnapshot) bool {
	if balance == nil || balance.Status != UpstreamBillingProbeStatusOK || balance.Data == nil {
		return false
	}
	if valid, ok := balance.Data["is_valid"].(bool); ok && !valid {
		return false
	}
	if unlimited, ok := balance.Data["unlimited"].(bool); ok && unlimited {
		return true
	}
	for _, key := range []string{"wallet_balance", "remaining"} {
		if value, ok := upstreamBalanceNumber(balance.Data[key]); ok {
			return value > 0
		}
	}
	return false
}

func upstreamBalanceNumber(raw any) (float64, bool) {
	switch v := raw.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	}
	return 0, false
}

// clearPaymentRequired402CooldownIfRecovered lifts a recoverable 402 cooldown as
// soon as the upstream balance probe reports a positive balance.
func (s *UpstreamBillingProbeService) clearPaymentRequired402CooldownIfRecovered(ctx context.Context, account *Account, snapshot *UpstreamBillingProbeSnapshot) {
	if s == nil || s.accountRepo == nil || account == nil || snapshot == nil {
		return
	}
	if !isPaymentRequired402Cooldown(account, s.currentTime()) || !upstreamBalanceSnapshotIsPositive(snapshot.Balance) {
		return
	}
	if err := s.accountRepo.ClearTempUnschedulable(ctx, account.ID); err != nil {
		slog.Warn("payment_required_402_probe_clear_failed", "account_id", account.ID, "error", err)
		return
	}
	slog.Info("payment_required_402_cooldown_cleared_by_balance_probe", "account_id", account.ID)
}

// handlePoolMode402 intercepts 402 before the pool-mode skip so a pool account
// whose upstream balance is exhausted is cooled down instead of re-selected.
func (s *RateLimitService) handlePoolMode402(ctx context.Context, account *Account, responseBody []byte) bool {
	if account == nil || isOpenAIDeactivatedWorkspace402(account, responseBody) {
		return false
	}
	upstreamMsg := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(responseBody)))
	if upstreamMsg != "" {
		upstreamMsg = truncateForLog([]byte(upstreamMsg), 512)
	}
	return s.handleRecoverable402(ctx, account, responseBody, upstreamMsg)
}
