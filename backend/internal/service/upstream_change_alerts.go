package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// Upstream change alerts persist what the upstream billing probe already
// reads (group multiplier and wallet balance), record multiplier changes and
// low-balance crossings as events, and optionally notify through the account
// ops channels. Nothing here sends requests to upstreams: it only observes the
// snapshots that the probe writes. Every switch is off by default.

const (
	SettingKeyUpstreamChangeAlertSettings = "upstream_change_alert_settings"

	UpstreamChangeKindRate    = "rate_multiplier"
	UpstreamChangeKindBalance = "balance_low"

	UpstreamChangeDirectionUp        = "up"
	UpstreamChangeDirectionDown      = "down"
	UpstreamChangeDirectionLow       = "low"
	UpstreamChangeDirectionRecovered = "recovered"

	UpstreamChangeNotifyNone       = "none"
	UpstreamChangeNotifyPending    = "pending"
	UpstreamChangeNotifySent       = "sent"
	UpstreamChangeNotifyFailed     = "failed"
	UpstreamChangeNotifyMuted      = "muted"
	UpstreamChangeNotifySuppressed = "suppressed"
	UpstreamChangeNotifyFiltered   = "filtered"
	UpstreamChangeNotifyMerged     = "merged"

	UpstreamChangeDirectionFilterBoth = "both"

	upstreamChangeMinRetentionDays  = 1
	upstreamChangeMaxRetentionDays  = 365
	upstreamChangeMaxMutedAccounts  = 1000
	upstreamChangeObserveTimeout    = 5 * time.Second
	upstreamChangePruneEvery        = time.Hour
	upstreamChangeFlushBatch        = 500
	upstreamChangeFlushLockKey      = "upstream:change:flush:leader"
	upstreamChangeMultiplierEpsilon = 1e-9
)

var ErrUpstreamChangeInvalidSettings = infraerrors.BadRequest("UPSTREAM_CHANGE_INVALID_SETTINGS", "invalid upstream change alert settings")

// UpstreamChangeAlertSettings are global. Every switch defaults to off.
type UpstreamChangeAlertSettings struct {
	// RecordHistory persists one snapshot per successful probe and every change event.
	RecordHistory bool `json:"record_history"`
	RetentionDays int  `json:"retention_days"`

	RateAlertEnabled bool `json:"rate_alert_enabled"`
	// RateAlertDirection is both | up | down and applies to the net change of a merge window.
	RateAlertDirection string `json:"rate_alert_direction"`
	// RateMinChangePercent ignores smaller relative changes for notifications (events are still recorded).
	RateMinChangePercent float64 `json:"rate_min_change_percent"`
	// MergeWindowMinutes collects consecutive changes of one account into one notification.
	MergeWindowMinutes int `json:"merge_window_minutes"`
	// CooldownMinutes is the minimum gap between two multiplier notifications for one account.
	CooldownMinutes int `json:"cooldown_minutes"`

	BalanceAlertEnabled bool `json:"balance_alert_enabled"`
	// BalanceThreshold applies to accounts without a per-account balance rule in account ops.
	BalanceThreshold       float64 `json:"balance_threshold"`
	BalanceUnit            string  `json:"balance_unit"`
	BalanceCooldownMinutes int     `json:"balance_cooldown_minutes"`

	MutedAccountIDs []int64 `json:"muted_account_ids"`
}

func DefaultUpstreamChangeAlertSettings() UpstreamChangeAlertSettings {
	return UpstreamChangeAlertSettings{
		RetentionDays:          30,
		RateAlertDirection:     UpstreamChangeDirectionFilterBoth,
		MergeWindowMinutes:     10,
		CooldownMinutes:        60,
		BalanceThreshold:       5,
		BalanceUnit:            "USD",
		BalanceCooldownMinutes: 360,
		MutedAccountIDs:        []int64{},
	}
}

// ValidateUpstreamChangeAlertSettings checks ranges and normalizes sets.
func ValidateUpstreamChangeAlertSettings(s *UpstreamChangeAlertSettings) error {
	if s == nil {
		return ErrUpstreamChangeInvalidSettings
	}
	invalid := func(msg string) error {
		return infraerrors.BadRequest("UPSTREAM_CHANGE_INVALID_SETTINGS", msg)
	}
	if s.RetentionDays < upstreamChangeMinRetentionDays || s.RetentionDays > upstreamChangeMaxRetentionDays {
		return invalid(fmt.Sprintf("retention_days must be between %d and %d", upstreamChangeMinRetentionDays, upstreamChangeMaxRetentionDays))
	}
	switch s.RateAlertDirection {
	case "":
		s.RateAlertDirection = UpstreamChangeDirectionFilterBoth
	case UpstreamChangeDirectionFilterBoth, UpstreamChangeDirectionUp, UpstreamChangeDirectionDown:
	default:
		return invalid("rate_alert_direction must be both, up or down")
	}
	if math.IsNaN(s.RateMinChangePercent) || s.RateMinChangePercent < 0 || s.RateMinChangePercent > 1000 {
		return invalid("rate_min_change_percent must be between 0 and 1000")
	}
	if s.MergeWindowMinutes < 0 || s.MergeWindowMinutes > 1440 {
		return invalid("merge_window_minutes must be between 0 and 1440")
	}
	if s.CooldownMinutes < 0 || s.CooldownMinutes > 10080 {
		return invalid("cooldown_minutes must be between 0 and 10080")
	}
	if math.IsNaN(s.BalanceThreshold) || math.IsInf(s.BalanceThreshold, 0) || s.BalanceThreshold < 0 {
		return invalid("balance_threshold must be a non-negative number")
	}
	s.BalanceUnit = strings.TrimSpace(s.BalanceUnit)
	if s.BalanceUnit == "" {
		s.BalanceUnit = "USD"
	}
	if len([]rune(s.BalanceUnit)) > 16 {
		return invalid("balance_unit is too long")
	}
	if s.BalanceCooldownMinutes < 5 || s.BalanceCooldownMinutes > 10080 {
		return invalid("balance_cooldown_minutes must be between 5 and 10080")
	}
	s.MutedAccountIDs = normalizeInt64Set(s.MutedAccountIDs)
	if len(s.MutedAccountIDs) > upstreamChangeMaxMutedAccounts {
		return invalid("too many muted accounts")
	}
	for _, id := range s.MutedAccountIDs {
		if id <= 0 {
			return invalid("muted_account_ids must be positive")
		}
	}
	return nil
}

func (s UpstreamChangeAlertSettings) active() bool {
	return s.RecordHistory || s.RateAlertEnabled || s.BalanceAlertEnabled
}

func (s UpstreamChangeAlertSettings) muted(accountID int64) bool {
	for _, id := range s.MutedAccountIDs {
		if id == accountID {
			return true
		}
	}
	return false
}

// UpstreamRateSnapshotRecord is one persisted probe observation.
type UpstreamRateSnapshotRecord struct {
	ID                  int64     `json:"id"`
	AccountID           int64     `json:"account_id"`
	Site                string    `json:"site"`
	Provider            string    `json:"provider"`
	UpstreamGroup       string    `json:"upstream_group,omitempty"`
	GroupMultiplier     *float64  `json:"group_multiplier"`
	ResolvedMultiplier  *float64  `json:"resolved_multiplier"`
	EffectiveMultiplier *float64  `json:"effective_multiplier"`
	Balance             *float64  `json:"balance"`
	BalanceUnit         string    `json:"balance_unit,omitempty"`
	ObservedAt          time.Time `json:"observed_at"`
}

// UpstreamChangeEvent is one multiplier change or balance threshold crossing.
type UpstreamChangeEvent struct {
	ID          int64      `json:"id"`
	AccountID   int64      `json:"account_id"`
	AccountName string     `json:"account_name"`
	Site        string     `json:"site"`
	Kind        string     `json:"kind"`
	Direction   string     `json:"direction"`
	OldValue    *float64   `json:"old_value"`
	NewValue    *float64   `json:"new_value"`
	Threshold   *float64   `json:"threshold,omitempty"`
	Unit        string     `json:"unit,omitempty"`
	NotifyState string     `json:"notify_state"`
	NotifiedAt  *time.Time `json:"notified_at,omitempty"`
	ObservedAt  time.Time  `json:"observed_at"`
}

type UpstreamChangeEventFilter struct {
	AccountID int64
	Site      string
	Kind      string
	Direction string
	From      *time.Time
	To        *time.Time
	Page      int
	PageSize  int
}

type UpstreamChangeEventPage struct {
	Items    []UpstreamChangeEvent `json:"items"`
	Total    int64                 `json:"total"`
	Page     int                   `json:"page"`
	PageSize int                   `json:"page_size"`
}

type UpstreamChangeRepository interface {
	InsertSnapshot(ctx context.Context, rec *UpstreamRateSnapshotRecord) error
	InsertEvent(ctx context.Context, ev *UpstreamChangeEvent) error
	ListEvents(ctx context.Context, filter UpstreamChangeEventFilter) ([]UpstreamChangeEvent, int64, error)
	ListSnapshots(ctx context.Context, accountID int64, since time.Time, limit int) ([]UpstreamRateSnapshotRecord, error)
	ListSites(ctx context.Context) ([]string, error)
	ListPendingEvents(ctx context.Context, limit int) ([]UpstreamChangeEvent, error)
	LastNotifiedAt(ctx context.Context, accountID int64, kind string) (*time.Time, error)
	MarkEvents(ctx context.Context, ids []int64, state string, at *time.Time) error
	Prune(ctx context.Context, before time.Time) (int64, error)
}

// upstreamChangeNotifier is the narrow part of AccountOpsService used here.
type upstreamChangeNotifier interface {
	BroadcastOpsNotification(ctx context.Context, msg OpsBroadcastMessage) (int, error)
	HasBalanceRule(ctx context.Context, accountID int64) bool
}

// UpstreamChangeService records snapshots/events and flushes notifications.
type UpstreamChangeService struct {
	repo     UpstreamChangeRepository
	settings SettingRepository
	notifier upstreamChangeNotifier
	now      func() time.Time

	pruneMu   sync.Mutex
	lastPrune time.Time
	flushMu   sync.Mutex
}

func NewUpstreamChangeService(repo UpstreamChangeRepository, settings SettingRepository, notifier *AccountOpsService) *UpstreamChangeService {
	svc := &UpstreamChangeService{repo: repo, settings: settings, now: time.Now}
	if notifier != nil {
		svc.notifier = notifier
	}
	return svc
}

func (s *UpstreamChangeService) currentTime() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// GetSettings returns defaults when absent or unreadable JSON.
func (s *UpstreamChangeService) GetSettings(ctx context.Context) (UpstreamChangeAlertSettings, error) {
	defaults := DefaultUpstreamChangeAlertSettings()
	if s == nil || s.settings == nil {
		return defaults, nil
	}
	raw, err := s.settings.GetValue(ctx, SettingKeyUpstreamChangeAlertSettings)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return defaults, nil
		}
		return defaults, fmt.Errorf("get upstream change alert settings: %w", err)
	}
	if strings.TrimSpace(raw) == "" {
		return defaults, nil
	}
	out := defaults
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return defaults, fmt.Errorf("parse upstream change alert settings: %w", err)
	}
	if ValidateUpstreamChangeAlertSettings(&out) != nil {
		// A stored value outside the current ranges falls back to safe defaults
		// for that read; switches stay as stored only when the whole value is valid.
		return defaults, nil
	}
	return out, nil
}

func (s *UpstreamChangeService) UpdateSettings(ctx context.Context, in UpstreamChangeAlertSettings) (UpstreamChangeAlertSettings, error) {
	if s == nil || s.settings == nil {
		return in, ErrUpstreamBillingProbeUnavailable
	}
	if err := ValidateUpstreamChangeAlertSettings(&in); err != nil {
		return in, err
	}
	data, err := json.Marshal(in)
	if err != nil {
		return in, err
	}
	if err := s.settings.Set(ctx, SettingKeyUpstreamChangeAlertSettings, string(data)); err != nil {
		return in, err
	}
	return in, nil
}

// upstreamProbeValues is what one snapshot says about the account at probe time.
type upstreamProbeValues struct {
	provider    string
	group       string
	groupRate   *float64
	resolved    *float64
	effective   *float64
	balance     *float64
	balanceUnit string
	rateFresh   bool
	balFresh    bool
}

func upstreamProbeValuesFromSnapshot(snap *UpstreamBillingProbeSnapshot) upstreamProbeValues {
	var v upstreamProbeValues
	if snap == nil {
		return v
	}
	if snap.Status == UpstreamBillingProbeStatusOK && snap.Data != nil {
		v.rateFresh = snap.ReceivedAt != nil && !snap.ReceivedAt.Before(snap.LastAttemptAt)
		v.provider, _ = snap.Data["provider"].(string)
		v.group, _ = snap.Data["group"].(string)
		if n, ok := opsNumber(snap.Data["group_rate_multiplier"]); ok && n >= 0 {
			v.groupRate = &n
		}
		if n, ok := opsNumber(snap.Data["resolved_rate_multiplier"]); ok && n >= 0 {
			v.resolved = &n
		}
		if n, ok := opsNumber(snap.Data["effective_rate_multiplier"]); ok && n >= 0 {
			v.effective = &n
		}
	}
	if v.provider == "" {
		v.provider = "sub2api"
	}
	if b := snap.Balance; b != nil && b.Status == UpstreamBillingProbeStatusOK && b.Data != nil {
		unlimited, _ := b.Data["unlimited"].(bool)
		mode, _ := b.Data["mode"].(string)
		if n, ok := opsNumber(b.Data["remaining"]); ok && !unlimited && !(mode == "subscription" && n < 0) {
			v.balance = &n
			// A rate-only probe carries the previous balance forward; only a
			// balance read in this same probe counts as a new observation.
			v.balFresh = b.ReceivedAt != nil && !b.ReceivedAt.Before(snap.LastAttemptAt)
		}
		v.balanceUnit, _ = b.Data["unit"].(string)
		if v.balanceUnit == "" {
			v.balanceUnit = "USD"
		}
	}
	return v
}

// chargedRate is the multiplier compared between probes: the resolved rate
// (user override if any, otherwise group rate), excluding time-of-day peaks.
func (v upstreamProbeValues) chargedRate() *float64 {
	if v.resolved != nil {
		return v.resolved
	}
	return v.groupRate
}

// UpstreamSiteKey reduces a base URL to scheme://host[:port][/path] without
// credentials, query or a trailing /v1, so history can be filtered by site.
func UpstreamSiteKey(baseURL string) string {
	if site, err := CanonicalNewAPISite(baseURL); err == nil {
		return site
	}
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Hostname() == "" {
		return ""
	}
	return strings.ToLower(u.Scheme + "://" + u.Host)
}

// ObserveProbe is called after a probe snapshot was stored. prev is the
// snapshot stored before this probe (decoded from the account row the probe
// loaded). It never returns an error to the probe; failures are logged.
func (s *UpstreamChangeService) ObserveProbe(ctx context.Context, account *Account, prev, next *UpstreamBillingProbeSnapshot) {
	if s == nil || s.repo == nil || account == nil || next == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), upstreamChangeObserveTimeout)
	defer cancel()
	settings, err := s.GetSettings(ctx)
	if err != nil || !settings.active() {
		return
	}
	now := s.currentTime().UTC()
	cur := upstreamProbeValuesFromSnapshot(next)
	old := upstreamProbeValuesFromSnapshot(prev)
	site := UpstreamSiteKey(account.GetCredential("base_url"))

	if settings.RecordHistory && ((cur.rateFresh && cur.chargedRate() != nil) || (cur.balFresh && cur.balance != nil)) {
		rec := &UpstreamRateSnapshotRecord{
			AccountID: account.ID, Site: site, Provider: cur.provider, UpstreamGroup: cur.group, ObservedAt: now,
		}
		if cur.rateFresh {
			rec.GroupMultiplier, rec.ResolvedMultiplier, rec.EffectiveMultiplier = cur.groupRate, cur.resolved, cur.effective
		}
		if cur.balFresh {
			rec.Balance, rec.BalanceUnit = cur.balance, cur.balanceUnit
		}
		if err := s.repo.InsertSnapshot(ctx, rec); err != nil {
			logger.LegacyPrintf("service.upstream_change", "insert snapshot failed: account_id=%d err=%v", account.ID, err)
		}
	}

	for _, ev := range upstreamChangeEventsFor(account, site, old, cur, settings, now) {
		ev := ev
		if !settings.RecordHistory && ev.NotifyState != UpstreamChangeNotifyPending {
			continue
		}
		if err := s.repo.InsertEvent(ctx, &ev); err != nil {
			logger.LegacyPrintf("service.upstream_change", "insert event failed: account_id=%d kind=%s err=%v", account.ID, ev.Kind, err)
		}
	}
	s.maybePrune(ctx, settings, now)
}

// upstreamChangeEventsFor derives events from two consecutive snapshots.
// Notification state is decided here only as "pending" or a terminal reason;
// the flush applies merge window, cooldown and direction to pending events.
func upstreamChangeEventsFor(account *Account, site string, old, cur upstreamProbeValues, settings UpstreamChangeAlertSettings, now time.Time) []UpstreamChangeEvent {
	var out []UpstreamChangeEvent
	oldRate, newRate := old.chargedRate(), cur.chargedRate()
	if cur.rateFresh && oldRate != nil && newRate != nil && !equalBillingMultiplierEps(*oldRate, *newRate) {
		ev := UpstreamChangeEvent{
			AccountID: account.ID, AccountName: account.Name, Site: site, Kind: UpstreamChangeKindRate,
			Direction: UpstreamChangeDirectionUp, OldValue: upstreamChangeFloat(*oldRate), NewValue: upstreamChangeFloat(*newRate),
			NotifyState: UpstreamChangeNotifyNone, ObservedAt: now,
		}
		if *newRate < *oldRate {
			ev.Direction = UpstreamChangeDirectionDown
		}
		if settings.RateAlertEnabled {
			ev.NotifyState = UpstreamChangeNotifyPending
			if settings.muted(account.ID) {
				ev.NotifyState = UpstreamChangeNotifyMuted
			}
		}
		out = append(out, ev)
	}
	if cur.balFresh && cur.balance != nil && strings.EqualFold(cur.balanceUnit, settings.BalanceUnit) {
		threshold := settings.BalanceThreshold
		wasLow := old.balance != nil && strings.EqualFold(old.balanceUnit, cur.balanceUnit) && *old.balance <= threshold
		isLow := *cur.balance <= threshold
		var direction string
		switch {
		case isLow && !wasLow:
			direction = UpstreamChangeDirectionLow
		case !isLow && wasLow:
			direction = UpstreamChangeDirectionRecovered
		}
		if direction != "" {
			ev := UpstreamChangeEvent{
				AccountID: account.ID, AccountName: account.Name, Site: site, Kind: UpstreamChangeKindBalance,
				Direction: direction, NewValue: upstreamChangeFloat(*cur.balance), Threshold: upstreamChangeFloat(threshold), Unit: cur.balanceUnit,
				NotifyState: UpstreamChangeNotifyNone, ObservedAt: now,
			}
			if old.balance != nil {
				ev.OldValue = upstreamChangeFloat(*old.balance)
			}
			if direction == UpstreamChangeDirectionLow && settings.BalanceAlertEnabled {
				ev.NotifyState = UpstreamChangeNotifyPending
				if settings.muted(account.ID) {
					ev.NotifyState = UpstreamChangeNotifyMuted
				}
			}
			out = append(out, ev)
		}
	}
	return out
}

func equalBillingMultiplierEps(a, b float64) bool {
	return math.Abs(a-b) <= upstreamChangeMultiplierEpsilon*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}

func upstreamChangeFloat(v float64) *float64 { return &v }

func (s *UpstreamChangeService) maybePrune(ctx context.Context, settings UpstreamChangeAlertSettings, now time.Time) {
	s.pruneMu.Lock()
	if now.Sub(s.lastPrune) < upstreamChangePruneEvery {
		s.pruneMu.Unlock()
		return
	}
	s.lastPrune = now
	s.pruneMu.Unlock()
	if _, err := s.repo.Prune(ctx, now.Add(-time.Duration(settings.RetentionDays)*24*time.Hour)); err != nil {
		logger.LegacyPrintf("service.upstream_change", "prune failed: %v", err)
	}
}

// FlushPending sends notifications for pending events. Callers serialize it
// across instances with a leader lock; flushMu serializes it in-process.
func (s *UpstreamChangeService) FlushPending(ctx context.Context) error {
	if s == nil || s.repo == nil {
		return nil
	}
	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return err
	}
	pending, err := s.repo.ListPendingEvents(ctx, upstreamChangeFlushBatch)
	if err != nil || len(pending) == 0 {
		return err
	}
	now := s.currentTime().UTC()
	type key struct {
		account int64
		kind    string
	}
	groups := map[key][]UpstreamChangeEvent{}
	order := []key{}
	for _, ev := range pending {
		k := key{ev.AccountID, ev.Kind}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], ev)
	}
	for _, k := range order {
		events := groups[k]
		sort.SliceStable(events, func(i, j int) bool { return events[i].ObservedAt.Before(events[j].ObservedAt) })
		decision := s.decideFlush(ctx, settings, k.kind, events, now)
		if decision.wait {
			continue
		}
		ids := make([]int64, 0, len(events))
		for _, ev := range events {
			ids = append(ids, ev.ID)
		}
		state := decision.state
		var at *time.Time
		if state == UpstreamChangeNotifySent {
			if s.notifier == nil {
				state = UpstreamChangeNotifyFailed
			} else if delivered, sendErr := s.notifier.BroadcastOpsNotification(ctx, decision.message); sendErr != nil || delivered == 0 {
				state = UpstreamChangeNotifyFailed
				if sendErr != nil {
					logger.LegacyPrintf("service.upstream_change", "notify failed: account_id=%d kind=%s err=%v", k.account, k.kind, sendErr)
				}
			} else {
				t := now
				at = &t
			}
		}
		// The newest event carries the notification; older ones in the same
		// window are marked merged so history still shows every change.
		if len(ids) > 1 && (state == UpstreamChangeNotifySent || state == UpstreamChangeNotifyFailed) {
			if err := s.repo.MarkEvents(ctx, ids[:len(ids)-1], UpstreamChangeNotifyMerged, at); err != nil {
				return err
			}
			ids = ids[len(ids)-1:]
		}
		if err := s.repo.MarkEvents(ctx, ids, state, at); err != nil {
			return err
		}
	}
	return nil
}

type upstreamChangeFlushDecision struct {
	wait    bool
	state   string
	message OpsBroadcastMessage
}

func (s *UpstreamChangeService) decideFlush(ctx context.Context, settings UpstreamChangeAlertSettings, kind string, events []UpstreamChangeEvent, now time.Time) upstreamChangeFlushDecision {
	first, last := events[0], events[len(events)-1]
	if settings.muted(last.AccountID) {
		return upstreamChangeFlushDecision{state: UpstreamChangeNotifyMuted}
	}
	switch kind {
	case UpstreamChangeKindRate:
		if !settings.RateAlertEnabled {
			return upstreamChangeFlushDecision{state: UpstreamChangeNotifySuppressed}
		}
		if now.Sub(first.ObservedAt) < time.Duration(settings.MergeWindowMinutes)*time.Minute {
			return upstreamChangeFlushDecision{wait: true}
		}
		if settings.CooldownMinutes > 0 {
			if lastSent, err := s.repo.LastNotifiedAt(ctx, last.AccountID, kind); err == nil && lastSent != nil &&
				now.Sub(*lastSent) < time.Duration(settings.CooldownMinutes)*time.Minute {
				return upstreamChangeFlushDecision{wait: true}
			}
		}
		if first.OldValue == nil || last.NewValue == nil {
			return upstreamChangeFlushDecision{state: UpstreamChangeNotifySuppressed}
		}
		oldValue, newValue := *first.OldValue, *last.NewValue
		if equalBillingMultiplierEps(oldValue, newValue) {
			return upstreamChangeFlushDecision{state: UpstreamChangeNotifySuppressed}
		}
		direction := UpstreamChangeDirectionUp
		if newValue < oldValue {
			direction = UpstreamChangeDirectionDown
		}
		if settings.RateAlertDirection != UpstreamChangeDirectionFilterBoth && settings.RateAlertDirection != direction {
			return upstreamChangeFlushDecision{state: UpstreamChangeNotifyFiltered}
		}
		if settings.RateMinChangePercent > 0 && oldValue > 0 && math.Abs(newValue-oldValue)/oldValue*100 < settings.RateMinChangePercent {
			return upstreamChangeFlushDecision{state: UpstreamChangeNotifyFiltered}
		}
		return upstreamChangeFlushDecision{state: UpstreamChangeNotifySent, message: upstreamRateChangeMessage(last, oldValue, newValue, direction, len(events))}
	case UpstreamChangeKindBalance:
		if !settings.BalanceAlertEnabled {
			return upstreamChangeFlushDecision{state: UpstreamChangeNotifySuppressed}
		}
		if s.notifier != nil && s.notifier.HasBalanceRule(ctx, last.AccountID) {
			// The per-account rule in account ops already alerts for this account.
			return upstreamChangeFlushDecision{state: UpstreamChangeNotifySuppressed}
		}
		if lastSent, err := s.repo.LastNotifiedAt(ctx, last.AccountID, kind); err == nil && lastSent != nil &&
			now.Sub(*lastSent) < time.Duration(settings.BalanceCooldownMinutes)*time.Minute {
			return upstreamChangeFlushDecision{state: UpstreamChangeNotifySuppressed}
		}
		return upstreamChangeFlushDecision{state: UpstreamChangeNotifySent, message: upstreamBalanceLowMessage(last)}
	}
	return upstreamChangeFlushDecision{state: UpstreamChangeNotifySuppressed}
}

func upstreamAccountLabel(ev UpstreamChangeEvent) string {
	name := strings.Join(strings.Fields(ev.AccountName), " ")
	if name == "" {
		name = "未命名账号"
	}
	return fmt.Sprintf("%s (#%d)", name, ev.AccountID)
}

func upstreamRateChangeMessage(ev UpstreamChangeEvent, oldValue, newValue float64, direction string, count int) OpsBroadcastMessage {
	title := "上游倍率上调"
	if direction == UpstreamChangeDirectionDown {
		title = "上游倍率下调"
	}
	change := fmt.Sprintf("%g → %g", oldValue, newValue)
	if oldValue > 0 {
		change += fmt.Sprintf("（%+.1f%%）", (newValue-oldValue)/oldValue*100)
	}
	fields := [][2]string{{"账户", upstreamAccountLabel(ev)}}
	if ev.Site != "" {
		fields = append(fields, [2]string{"站点", ev.Site})
	}
	fields = append(fields, [2]string{"倍率", change})
	if count > 1 {
		fields = append(fields, [2]string{"合并变动", fmt.Sprintf("%d 次", count)})
	}
	return OpsBroadcastMessage{Title: title, Account: upstreamAccountLabel(ev), Value: change, Reference: map[string]string{UpstreamChangeDirectionUp: "上调", UpstreamChangeDirectionDown: "下调"}[direction], ObservedAt: ev.ObservedAt, Fields: fields, Warning: direction == UpstreamChangeDirectionUp}
}

func upstreamBalanceLowMessage(ev UpstreamChangeEvent) OpsBroadcastMessage {
	balance, threshold := "未获取", "未获取"
	if ev.NewValue != nil {
		balance = opsRobotAmount(*ev.NewValue, ev.Unit)
	}
	if ev.Threshold != nil {
		threshold = opsRobotAmount(*ev.Threshold, ev.Unit)
	}
	fields := [][2]string{{"账户", upstreamAccountLabel(ev)}}
	if ev.Site != "" {
		fields = append(fields, [2]string{"站点", ev.Site})
	}
	fields = append(fields, [2]string{"余额", balance}, [2]string{"阈值", threshold})
	return OpsBroadcastMessage{Title: "上游余额低于全局阈值", Account: upstreamAccountLabel(ev), Value: balance, Reference: threshold, ObservedAt: ev.ObservedAt, Fields: fields, Warning: true}
}

func (s *UpstreamChangeService) ListEvents(ctx context.Context, filter UpstreamChangeEventFilter) (*UpstreamChangeEventPage, error) {
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 || filter.PageSize > 200 {
		filter.PageSize = 50
	}
	switch filter.Kind {
	case "", UpstreamChangeKindRate, UpstreamChangeKindBalance:
	default:
		return nil, infraerrors.BadRequest("UPSTREAM_CHANGE_INVALID_FILTER", "invalid kind")
	}
	switch filter.Direction {
	case "", UpstreamChangeDirectionUp, UpstreamChangeDirectionDown, UpstreamChangeDirectionLow, UpstreamChangeDirectionRecovered:
	default:
		return nil, infraerrors.BadRequest("UPSTREAM_CHANGE_INVALID_FILTER", "invalid direction")
	}
	items, total, err := s.repo.ListEvents(ctx, filter)
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []UpstreamChangeEvent{}
	}
	return &UpstreamChangeEventPage{Items: items, Total: total, Page: filter.Page, PageSize: filter.PageSize}, nil
}

func (s *UpstreamChangeService) ListSnapshots(ctx context.Context, accountID int64, days int) ([]UpstreamRateSnapshotRecord, error) {
	if accountID <= 0 {
		return nil, infraerrors.BadRequest("UPSTREAM_CHANGE_INVALID_FILTER", "account_id is required")
	}
	if days < 1 || days > upstreamChangeMaxRetentionDays {
		days = 7
	}
	items, err := s.repo.ListSnapshots(ctx, accountID, s.currentTime().Add(-time.Duration(days)*24*time.Hour), 2000)
	if items == nil {
		items = []UpstreamRateSnapshotRecord{}
	}
	return items, err
}

func (s *UpstreamChangeService) ListSites(ctx context.Context) ([]string, error) {
	sites, err := s.repo.ListSites(ctx)
	if sites == nil {
		sites = []string{}
	}
	return sites, err
}
