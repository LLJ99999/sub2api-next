package dto

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// accountCooldownMessageMaxRunes bounds the upstream message exposed in the
// admin account list (the full reason stays in temp_unschedulable_reason).
const accountCooldownMessageMaxRunes = 160

// Cooldown kinds.
const (
	AccountCooldownKindTempUnschedulable = "temp_unschedulable"
	AccountCooldownKindRateLimit         = "rate_limit"
	AccountCooldownKindOverload          = "overload"
	AccountCooldownKindModelRateLimit    = "model_rate_limit"
)

// AccountCooldown is one active recoverable cooldown of an account, flattened
// for the admin UI badge/tooltip.
type AccountCooldown struct {
	Kind             string    `json:"kind"`
	Scope            string    `json:"scope"` // account | model
	Model            string    `json:"model,omitempty"`
	StatusCode       int       `json:"status_code,omitempty"`
	Message          string    `json:"message,omitempty"`
	Keyword          string    `json:"keyword,omitempty"`
	BreakerLevel     int       `json:"breaker_level,omitempty"`
	Until            time.Time `json:"until"`
	RemainingSeconds int64     `json:"remaining_seconds"`
}

func truncateCooldownMessage(msg string) string {
	msg = strings.Join(strings.Fields(msg), " ")
	if utf8.RuneCountInString(msg) <= accountCooldownMessageMaxRunes {
		return msg
	}
	runes := []rune(msg)
	return string(runes[:accountCooldownMessageMaxRunes]) + "…"
}

func remainingSeconds(until, now time.Time) int64 {
	d := until.Sub(now)
	if d <= 0 {
		return 0
	}
	secs := int64(d / time.Second)
	if d%time.Second != 0 {
		secs++
	}
	return secs
}

// applyCooldownReason fills status/message/keyword/level/scope from a reason
// string that is either a TempUnschedState JSON payload or plain text.
func applyCooldownReason(entry *AccountCooldown, reason string) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return
	}
	if strings.HasPrefix(reason, "{") {
		var state service.TempUnschedState
		if err := json.Unmarshal([]byte(reason), &state); err == nil {
			entry.StatusCode = state.StatusCode
			entry.Message = truncateCooldownMessage(state.ErrorMessage)
			entry.Keyword = state.MatchedKeyword
			entry.BreakerLevel = state.BreakerLevel
			if state.Scope == service.TempUnschedScopeModel && state.Model != "" {
				entry.Scope = service.TempUnschedScopeModel
				entry.Model = state.Model
			}
			return
		}
	}
	entry.Message = truncateCooldownMessage(reason)
}

// AccountCooldownsFromService lists the account's active (not yet expired)
// recoverable cooldowns, soonest-ending first. Returns nil when none.
func AccountCooldownsFromService(a *service.Account, now time.Time) []AccountCooldown {
	if a == nil {
		return nil
	}
	out := make([]AccountCooldown, 0, 2)
	if a.TempUnschedulableUntil != nil && now.Before(*a.TempUnschedulableUntil) {
		entry := AccountCooldown{
			Kind:             AccountCooldownKindTempUnschedulable,
			Scope:            service.TempUnschedScopeAccount,
			Until:            *a.TempUnschedulableUntil,
			RemainingSeconds: remainingSeconds(*a.TempUnschedulableUntil, now),
		}
		applyCooldownReason(&entry, a.TempUnschedulableReason)
		out = append(out, entry)
	}
	if a.RateLimitResetAt != nil && now.Before(*a.RateLimitResetAt) {
		out = append(out, AccountCooldown{
			Kind:             AccountCooldownKindRateLimit,
			Scope:            service.TempUnschedScopeAccount,
			StatusCode:       429,
			Until:            *a.RateLimitResetAt,
			RemainingSeconds: remainingSeconds(*a.RateLimitResetAt, now),
		})
	}
	if a.OverloadUntil != nil && now.Before(*a.OverloadUntil) {
		out = append(out, AccountCooldown{
			Kind:             AccountCooldownKindOverload,
			Scope:            service.TempUnschedScopeAccount,
			StatusCode:       529,
			Until:            *a.OverloadUntil,
			RemainingSeconds: remainingSeconds(*a.OverloadUntil, now),
		})
	}
	if limits, ok := a.Extra["model_rate_limits"].(map[string]any); ok {
		for model, raw := range limits {
			payload, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			resetRaw, _ := payload["rate_limit_reset_at"].(string)
			resetAt, err := time.Parse(time.RFC3339, resetRaw)
			if err != nil || !now.Before(resetAt) {
				continue
			}
			entry := AccountCooldown{
				Kind:             AccountCooldownKindModelRateLimit,
				Scope:            service.TempUnschedScopeModel,
				Model:            model,
				Until:            resetAt,
				RemainingSeconds: remainingSeconds(resetAt, now),
			}
			if reason, _ := payload["reason"].(string); reason != "" {
				applyCooldownReason(&entry, reason)
			}
			// The map key is authoritative for model-scoped entries.
			entry.Scope, entry.Model = service.TempUnschedScopeModel, model
			out = append(out, entry)
		}
	}
	if len(out) == 0 {
		return nil
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Until.Equal(out[j].Until) {
			return out[i].Model < out[j].Model
		}
		return out[i].Until.Before(out[j].Until)
	})
	return out
}
