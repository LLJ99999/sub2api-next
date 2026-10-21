package service

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// SettingRechargeTabs stores the admin-configured recharge tabs as a JSON array
// of RechargeTab. Empty / missing means the default single 「充值」 tab that
// lists every available payment method (legacy behavior).
const SettingRechargeTabs = "RECHARGE_TABS"

const (
	maxRechargeTabs           = 10
	maxRechargeTabNameRunes   = 32
	maxRechargeTabMethods     = 50
	maxRechargeTabIDLength    = 32
	maxRechargeTabMethodBytes = 64
)

var (
	rechargeTabIDPattern     = regexp.MustCompile(`^[a-z0-9_-]+$`)
	rechargeTabMethodPattern = regexp.MustCompile(`^[a-z0-9_.-]+$`)
)

// RechargeTab is one user-facing tab on the recharge page. Methods lists the
// payment method keys (the keys of checkout-info `methods`, e.g. alipay, wxpay,
// stripe, or an EasyPay customMethods type such as "binance") shown on the tab.
type RechargeTab struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	SortOrder int      `json:"sort_order"`
	Enabled   bool     `json:"enabled"`
	Methods   []string `json:"methods"`
}

// NormalizeRechargeTabs validates tabs on the write path. Any invalid entry is
// an error; the result is sorted by SortOrder (stable on input order). An empty
// list is valid and restores the default single tab.
func NormalizeRechargeTabs(raw []RechargeTab) ([]RechargeTab, error) {
	if len(raw) == 0 {
		return []RechargeTab{}, nil
	}
	if len(raw) > maxRechargeTabs {
		return nil, fmt.Errorf("recharge tabs exceed limit of %d", maxRechargeTabs)
	}
	out := make([]RechargeTab, 0, len(raw))
	seenID := make(map[string]struct{}, len(raw))
	enabledCount := 0
	for i, tab := range raw {
		normalized, err := normalizeRechargeTab(tab, i)
		if err != nil {
			return nil, err
		}
		if _, dup := seenID[normalized.ID]; dup {
			return nil, fmt.Errorf("recharge tab #%d: duplicate id %q", i+1, normalized.ID)
		}
		seenID[normalized.ID] = struct{}{}
		if normalized.Enabled {
			if len(normalized.Methods) == 0 {
				return nil, fmt.Errorf("recharge tab #%d: an enabled tab must bind at least one payment method", i+1)
			}
			enabledCount++
		}
		out = append(out, normalized)
	}
	if enabledCount == 0 {
		return nil, fmt.Errorf("at least one recharge tab must be enabled (or remove all tabs to use the default)")
	}
	sortRechargeTabs(out)
	return out, nil
}

// normalizeRechargeTab validates one tab's id, name and method keys.
func normalizeRechargeTab(tab RechargeTab, i int) (RechargeTab, error) {
	id := strings.ToLower(strings.TrimSpace(tab.ID))
	if id == "" || len(id) > maxRechargeTabIDLength || !rechargeTabIDPattern.MatchString(id) {
		return RechargeTab{}, fmt.Errorf("recharge tab #%d: id must be 1-%d lowercase letters, digits, underscores or hyphens", i+1, maxRechargeTabIDLength)
	}
	name := strings.TrimSpace(tab.Name)
	if name == "" || utf8.RuneCountInString(name) > maxRechargeTabNameRunes {
		return RechargeTab{}, fmt.Errorf("recharge tab #%d: name must be 1-%d characters", i+1, maxRechargeTabNameRunes)
	}
	if len(tab.Methods) > maxRechargeTabMethods {
		return RechargeTab{}, fmt.Errorf("recharge tab #%d: too many methods (max %d)", i+1, maxRechargeTabMethods)
	}
	methods := make([]string, 0, len(tab.Methods))
	seenMethod := make(map[string]struct{}, len(tab.Methods))
	for _, m := range tab.Methods {
		m = strings.ToLower(strings.TrimSpace(m))
		if m == "" || len(m) > maxRechargeTabMethodBytes || !rechargeTabMethodPattern.MatchString(m) {
			return RechargeTab{}, fmt.Errorf("recharge tab #%d: invalid payment method %q", i+1, m)
		}
		if _, dup := seenMethod[m]; dup {
			continue
		}
		seenMethod[m] = struct{}{}
		methods = append(methods, m)
	}
	return RechargeTab{ID: id, Name: name, SortOrder: tab.SortOrder, Enabled: tab.Enabled, Methods: methods}, nil
}

func sortRechargeTabs(tabs []RechargeTab) {
	sort.SliceStable(tabs, func(i, j int) bool { return tabs[i].SortOrder < tabs[j].SortOrder })
}

// encodeRechargeTabs serializes tabs; an empty list is stored as "" (same as unset).
func encodeRechargeTabs(tabs []RechargeTab) (string, error) {
	if len(tabs) == 0 {
		return "", nil
	}
	b, err := json.Marshal(tabs)
	if err != nil {
		return "", fmt.Errorf("encode recharge tabs: %w", err)
	}
	return string(b), nil
}

// parseRechargeTabs is the lenient read path: a corrupt value or invalid
// entries are dropped instead of breaking the recharge page; a list with no
// usable enabled tab falls back to the default (empty).
func parseRechargeTabs(raw string) []RechargeTab {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []RechargeTab{}
	}
	var items []RechargeTab
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		slog.Warn("[Payment] parseRechargeTabs: unmarshal failed", "error", err)
		return []RechargeTab{}
	}
	out := make([]RechargeTab, 0, len(items))
	seenID := map[string]struct{}{}
	enabled := 0
	for i, item := range items {
		if len(out) >= maxRechargeTabs {
			break
		}
		tab, err := normalizeRechargeTab(item, i)
		if err != nil {
			continue
		}
		if _, dup := seenID[tab.ID]; dup {
			continue
		}
		seenID[tab.ID] = struct{}{}
		if tab.Enabled && len(tab.Methods) == 0 {
			tab.Enabled = false
		}
		if tab.Enabled {
			enabled++
		}
		out = append(out, tab)
	}
	if enabled == 0 {
		return []RechargeTab{}
	}
	sortRechargeTabs(out)
	return out
}

// EnabledRechargeTabs returns the enabled tabs in display order (user-facing).
// An empty result means "use the default single tab with all methods".
func EnabledRechargeTabs(tabs []RechargeTab) []RechargeTab {
	out := make([]RechargeTab, 0, len(tabs))
	for _, tab := range tabs {
		if tab.Enabled && len(tab.Methods) > 0 {
			out = append(out, tab)
		}
	}
	return out
}

// resolveRechargeTabsUpdate validates an update; the value is meaningful only
// when req.RechargeTabs is non-nil.
func resolveRechargeTabsUpdate(req UpdatePaymentConfigRequest) (string, error) {
	if req.RechargeTabs == nil {
		return "", nil
	}
	tabs, err := NormalizeRechargeTabs(*req.RechargeTabs)
	if err != nil {
		return "", err
	}
	return encodeRechargeTabs(tabs)
}
