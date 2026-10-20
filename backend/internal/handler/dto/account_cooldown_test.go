package dto

import (
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountCooldownsFromService(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	tempUntil := now.Add(90 * time.Second)
	expired := now.Add(-time.Second)
	longMsg := strings.Repeat("上游错误", 100)
	acc := &service.Account{
		ID:                      1,
		Type:                    service.AccountTypeAPIKey,
		TempUnschedulableUntil:  &tempUntil,
		TempUnschedulableReason: `{"status_code":503,"matched_keyword":"upstream_relay_breaker","error_message":"` + longMsg + `","breaker_level":2,"scope":"account"}`,
		OverloadUntil:           &expired,
		Extra: map[string]any{
			"model_rate_limits": map[string]any{
				"gpt-4o": map[string]any{
					"rate_limited_at":     now.Add(-time.Minute).Format(time.RFC3339),
					"rate_limit_reset_at": now.Add(30 * time.Second).Format(time.RFC3339),
					"reason":              `{"status_code":429,"matched_keyword":"upstream_relay_breaker","error_message":"too many","breaker_level":1,"scope":"model","model":"gpt-4o"}`,
				},
				"old": map[string]any{
					"rate_limit_reset_at": expired.Format(time.RFC3339),
				},
			},
		},
	}

	got := AccountCooldownsFromService(acc, now)
	require.Len(t, got, 2)

	model := got[0]
	require.Equal(t, AccountCooldownKindModelRateLimit, model.Kind)
	require.Equal(t, service.TempUnschedScopeModel, model.Scope)
	require.Equal(t, "gpt-4o", model.Model)
	require.Equal(t, 429, model.StatusCode)
	require.Equal(t, "too many", model.Message)
	require.Equal(t, 1, model.BreakerLevel)
	require.EqualValues(t, 30, model.RemainingSeconds)

	temp := got[1]
	require.Equal(t, AccountCooldownKindTempUnschedulable, temp.Kind)
	require.Equal(t, service.TempUnschedScopeAccount, temp.Scope)
	require.Equal(t, 503, temp.StatusCode)
	require.Equal(t, "upstream_relay_breaker", temp.Keyword)
	require.Equal(t, 2, temp.BreakerLevel)
	require.EqualValues(t, 90, temp.RemainingSeconds)
	require.LessOrEqual(t, len([]rune(temp.Message)), accountCooldownMessageMaxRunes+1)
	require.True(t, strings.HasSuffix(temp.Message, "…"))
}

func TestAccountCooldownsFromServicePlainReasonAndNone(t *testing.T) {
	now := time.Now()
	require.Nil(t, AccountCooldownsFromService(&service.Account{ID: 2}, now))

	until := now.Add(10 * time.Minute)
	acc := &service.Account{ID: 3, TempUnschedulableUntil: &until, TempUnschedulableReason: "manual pause", RateLimitResetAt: &until}
	got := AccountCooldownsFromService(acc, now)
	require.Len(t, got, 2)
	for _, c := range got {
		switch c.Kind {
		case AccountCooldownKindTempUnschedulable:
			require.Equal(t, "manual pause", c.Message)
			require.Zero(t, c.StatusCode)
		case AccountCooldownKindRateLimit:
			require.Equal(t, 429, c.StatusCode)
		default:
			t.Fatalf("unexpected kind %s", c.Kind)
		}
	}

	item := AccountListItemFromAccount(AccountFromService(acc))
	require.Len(t, item.Cooldowns, 2)
}
