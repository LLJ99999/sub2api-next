//go:build unit

package service

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type upstreamChangeMemSettings struct {
	mu     sync.Mutex
	values map[string]string
}

func newUpstreamChangeMemSettings() *upstreamChangeMemSettings {
	return &upstreamChangeMemSettings{values: map[string]string{}}
}

func (m *upstreamChangeMemSettings) Get(_ context.Context, key string) (*Setting, error) {
	v, err := m.GetValue(context.Background(), key)
	if err != nil {
		return nil, err
	}
	return &Setting{Key: key, Value: v}, nil
}
func (m *upstreamChangeMemSettings) GetValue(_ context.Context, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return v, nil
}
func (m *upstreamChangeMemSettings) Set(_ context.Context, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[key] = value
	return nil
}
func (m *upstreamChangeMemSettings) GetMultiple(context.Context, []string) (map[string]string, error) {
	return map[string]string{}, nil
}
func (m *upstreamChangeMemSettings) SetMultiple(context.Context, map[string]string) error { return nil }
func (m *upstreamChangeMemSettings) GetAll(context.Context) (map[string]string, error) {
	return map[string]string{}, nil
}
func (m *upstreamChangeMemSettings) Delete(context.Context, string) error { return nil }

type upstreamChangeMemRepo struct {
	mu        sync.Mutex
	snapshots []UpstreamRateSnapshotRecord
	events    []UpstreamChangeEvent
	nextID    int64
}

func (r *upstreamChangeMemRepo) InsertSnapshot(_ context.Context, rec *UpstreamRateSnapshotRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	rec.ID = r.nextID
	r.snapshots = append(r.snapshots, *rec)
	return nil
}
func (r *upstreamChangeMemRepo) InsertEvent(_ context.Context, ev *UpstreamChangeEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	ev.ID = r.nextID
	r.events = append(r.events, *ev)
	return nil
}
func (r *upstreamChangeMemRepo) ListEvents(context.Context, UpstreamChangeEventFilter) ([]UpstreamChangeEvent, int64, error) {
	return r.events, int64(len(r.events)), nil
}
func (r *upstreamChangeMemRepo) ListSnapshots(context.Context, int64, time.Time, int) ([]UpstreamRateSnapshotRecord, error) {
	return r.snapshots, nil
}
func (r *upstreamChangeMemRepo) ListSites(context.Context) ([]string, error) { return nil, nil }
func (r *upstreamChangeMemRepo) ListPendingEvents(context.Context, int) ([]UpstreamChangeEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []UpstreamChangeEvent{}
	for _, ev := range r.events {
		if ev.NotifyState == UpstreamChangeNotifyPending {
			out = append(out, ev)
		}
	}
	return out, nil
}
func (r *upstreamChangeMemRepo) LastNotifiedAt(_ context.Context, accountID int64, kind string) (*time.Time, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var last *time.Time
	for _, ev := range r.events {
		if ev.AccountID == accountID && ev.Kind == kind && ev.NotifyState == UpstreamChangeNotifySent && ev.NotifiedAt != nil {
			if last == nil || ev.NotifiedAt.After(*last) {
				t := *ev.NotifiedAt
				last = &t
			}
		}
	}
	return last, nil
}
func (r *upstreamChangeMemRepo) MarkEvents(_ context.Context, ids []int64, state string, at *time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.events {
		for _, id := range ids {
			if r.events[i].ID == id && r.events[i].NotifyState == UpstreamChangeNotifyPending {
				r.events[i].NotifyState = state
				r.events[i].NotifiedAt = at
			}
		}
	}
	return nil
}
func (r *upstreamChangeMemRepo) Prune(context.Context, time.Time) (int64, error) { return 0, nil }

func (r *upstreamChangeMemRepo) states() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.events))
	for _, ev := range r.events {
		out = append(out, ev.NotifyState)
	}
	return out
}

type upstreamChangeFakeNotifier struct {
	sent      []OpsBroadcastMessage
	ruleIDs   map[int64]bool
	failSends bool
}

func (n *upstreamChangeFakeNotifier) BroadcastOpsNotification(_ context.Context, msg OpsBroadcastMessage) (int, error) {
	if n.failSends {
		return 0, errOpsBroadcastNoDestination
	}
	n.sent = append(n.sent, msg)
	return 1, nil
}
func (n *upstreamChangeFakeNotifier) HasBalanceRule(_ context.Context, id int64) bool { return n.ruleIDs[id] }

func upstreamChangeTestService(t *testing.T, settings UpstreamChangeAlertSettings) (*UpstreamChangeService, *upstreamChangeMemRepo, *upstreamChangeFakeNotifier, *time.Time) {
	t.Helper()
	repo := &upstreamChangeMemRepo{}
	mem := newUpstreamChangeMemSettings()
	require.NoError(t, ValidateUpstreamChangeAlertSettings(&settings))
	raw, _ := json.Marshal(settings)
	require.NoError(t, mem.Set(context.Background(), SettingKeyUpstreamChangeAlertSettings, string(raw)))
	notifier := &upstreamChangeFakeNotifier{ruleIDs: map[int64]bool{}}
	now := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	svc := &UpstreamChangeService{repo: repo, settings: mem, notifier: notifier}
	svc.now = func() time.Time { return now }
	return svc, repo, notifier, &now
}

func upstreamTestRateSnapshot(at time.Time, rate float64, balance *float64) *UpstreamBillingProbeSnapshot {
	snap := &UpstreamBillingProbeSnapshot{
		Status: UpstreamBillingProbeStatusOK, ReceivedAt: &at, LastAttemptAt: at,
		Data: map[string]any{"group_rate_multiplier": rate, "resolved_rate_multiplier": rate, "effective_rate_multiplier": rate},
	}
	if balance != nil {
		snap.Balance = &UpstreamBalanceSnapshot{Status: UpstreamBillingProbeStatusOK, ReceivedAt: &at, LastAttemptAt: at, Data: map[string]any{"remaining": *balance, "unit": "USD"}}
	}
	return snap
}

func upstreamTestAccount() *Account {
	return &Account{ID: 7, Name: "relay-a", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://user:pw@relay.example.com/v1", "api_key": "sk-secret"}}
}

func TestUpstreamChangeDefaultsAreOff(t *testing.T) {
	d := DefaultUpstreamChangeAlertSettings()
	require.False(t, d.RecordHistory)
	require.False(t, d.RateAlertEnabled)
	require.False(t, d.BalanceAlertEnabled)
	require.NoError(t, ValidateUpstreamChangeAlertSettings(&d))

	svc := &UpstreamChangeService{repo: &upstreamChangeMemRepo{}, settings: newUpstreamChangeMemSettings(), now: time.Now}
	repo := svc.repo.(*upstreamChangeMemRepo)
	svc.ObserveProbe(context.Background(), upstreamTestAccount(), upstreamTestRateSnapshot(time.Now(), 1, nil), upstreamTestRateSnapshot(time.Now(), 2, nil))
	require.Empty(t, repo.events)
	require.Empty(t, repo.snapshots)
}

func TestUpstreamChangeValidation(t *testing.T) {
	bad := DefaultUpstreamChangeAlertSettings()
	bad.RateAlertDirection = "sideways"
	require.Error(t, ValidateUpstreamChangeAlertSettings(&bad))
	bad = DefaultUpstreamChangeAlertSettings()
	bad.RetentionDays = 0
	require.Error(t, ValidateUpstreamChangeAlertSettings(&bad))
	ok := DefaultUpstreamChangeAlertSettings()
	ok.MutedAccountIDs = []int64{3, 3, 1}
	require.NoError(t, ValidateUpstreamChangeAlertSettings(&ok))
	require.Equal(t, []int64{1, 3}, ok.MutedAccountIDs)
}

func TestUpstreamSiteKeyStripsCredentials(t *testing.T) {
	require.Equal(t, "https://relay.example.com", UpstreamSiteKey("https://relay.example.com/v1/"))
	key := UpstreamSiteKey("https://user:pw@relay.example.com/v1")
	require.NotContains(t, key, "pw")
	require.NotContains(t, key, "user")
}

func TestUpstreamChangeRecordsHistoryAndRateEvent(t *testing.T) {
	settings := DefaultUpstreamChangeAlertSettings()
	settings.RecordHistory = true
	svc, repo, _, now := upstreamChangeTestService(t, settings)
	bal := 12.5
	prev := upstreamTestRateSnapshot(now.Add(-time.Hour), 1.0, &bal)
	next := upstreamTestRateSnapshot(*now, 1.2, &bal)
	svc.ObserveProbe(context.Background(), upstreamTestAccount(), prev, next)
	require.Len(t, repo.snapshots, 1)
	require.Equal(t, 1.2, *repo.snapshots[0].ResolvedMultiplier)
	require.Equal(t, 12.5, *repo.snapshots[0].Balance)
	require.NotContains(t, repo.snapshots[0].Site, "pw")
	require.Len(t, repo.events, 1)
	ev := repo.events[0]
	require.Equal(t, UpstreamChangeKindRate, ev.Kind)
	require.Equal(t, UpstreamChangeDirectionUp, ev.Direction)
	require.Equal(t, 1.0, *ev.OldValue)
	require.Equal(t, 1.2, *ev.NewValue)
	// History only, alerts off: nothing to notify.
	require.Equal(t, UpstreamChangeNotifyNone, ev.NotifyState)
}

func TestUpstreamChangeIgnoresCarriedOverBalanceAndFailedRate(t *testing.T) {
	settings := DefaultUpstreamChangeAlertSettings()
	settings.RecordHistory = true
	svc, repo, _, now := upstreamChangeTestService(t, settings)
	old := now.Add(-time.Hour)
	bal := 1.0
	prev := upstreamTestRateSnapshot(old, 1.0, &bal)
	// Failed rate request keeps old data; balance carried from previous probe.
	next := &UpstreamBillingProbeSnapshot{Status: UpstreamBillingProbeStatusFailed, LastAttemptAt: *now, Data: prev.Data, ReceivedAt: &old, Balance: prev.Balance}
	svc.ObserveProbe(context.Background(), upstreamTestAccount(), prev, next)
	require.Empty(t, repo.snapshots)
	require.Empty(t, repo.events)
}

func TestUpstreamChangeFirstObservationIsNotAChange(t *testing.T) {
	settings := DefaultUpstreamChangeAlertSettings()
	settings.RecordHistory, settings.RateAlertEnabled = true, true
	svc, repo, _, now := upstreamChangeTestService(t, settings)
	svc.ObserveProbe(context.Background(), upstreamTestAccount(), nil, upstreamTestRateSnapshot(*now, 2, nil))
	require.Len(t, repo.snapshots, 1)
	require.Empty(t, repo.events)
}

func TestUpstreamChangeMergeWindowAndCooldown(t *testing.T) {
	settings := DefaultUpstreamChangeAlertSettings()
	settings.RateAlertEnabled = true
	settings.MergeWindowMinutes = 10
	settings.CooldownMinutes = 60
	svc, repo, notifier, now := upstreamChangeTestService(t, settings)
	account := upstreamTestAccount()
	start := *now

	svc.ObserveProbe(context.Background(), account, upstreamTestRateSnapshot(start, 1.0, nil), upstreamTestRateSnapshot(start, 1.1, nil))
	*now = start.Add(5 * time.Minute)
	svc.ObserveProbe(context.Background(), account, upstreamTestRateSnapshot(start, 1.1, nil), upstreamTestRateSnapshot(*now, 1.5, nil))
	require.Len(t, repo.events, 2)

	// Inside the merge window nothing is sent.
	require.NoError(t, svc.FlushPending(context.Background()))
	require.Empty(t, notifier.sent)

	*now = start.Add(11 * time.Minute)
	require.NoError(t, svc.FlushPending(context.Background()))
	require.Len(t, notifier.sent, 1)
	require.Equal(t, "上游倍率上调", notifier.sent[0].Title)
	require.Contains(t, notifier.sent[0].Value, "1 → 1.5")
	require.Equal(t, []string{UpstreamChangeNotifyMerged, UpstreamChangeNotifySent}, repo.states())

	// A new change inside the cooldown waits, then goes out after it.
	*now = start.Add(30 * time.Minute)
	svc.ObserveProbe(context.Background(), account, upstreamTestRateSnapshot(start, 1.5, nil), upstreamTestRateSnapshot(*now, 1.4, nil))
	*now = start.Add(45 * time.Minute)
	require.NoError(t, svc.FlushPending(context.Background()))
	require.Len(t, notifier.sent, 1)
	*now = start.Add(72 * time.Minute)
	require.NoError(t, svc.FlushPending(context.Background()))
	require.Len(t, notifier.sent, 2)
	require.Equal(t, "上游倍率下调", notifier.sent[1].Title)
}

func TestUpstreamChangeNetZeroDirectionFilterAndMute(t *testing.T) {
	settings := DefaultUpstreamChangeAlertSettings()
	settings.RateAlertEnabled = true
	settings.MergeWindowMinutes = 0
	settings.CooldownMinutes = 0
	settings.RateAlertDirection = UpstreamChangeDirectionUp
	svc, repo, notifier, now := upstreamChangeTestService(t, settings)
	account := upstreamTestAccount()

	svc.ObserveProbe(context.Background(), account, upstreamTestRateSnapshot(*now, 1.0, nil), upstreamTestRateSnapshot(*now, 0.8, nil))
	require.NoError(t, svc.FlushPending(context.Background()))
	require.Empty(t, notifier.sent)
	require.Equal(t, UpstreamChangeNotifyFiltered, repo.states()[0])

	// Up then back down within one flush: net zero is suppressed.
	svc.ObserveProbe(context.Background(), account, upstreamTestRateSnapshot(*now, 0.8, nil), upstreamTestRateSnapshot(*now, 1.0, nil))
	svc.ObserveProbe(context.Background(), account, upstreamTestRateSnapshot(*now, 1.0, nil), upstreamTestRateSnapshot(*now, 0.8, nil))
	require.NoError(t, svc.FlushPending(context.Background()))
	require.Empty(t, notifier.sent)

	// Muted account never notifies.
	settings.MutedAccountIDs = []int64{account.ID}
	_, err := svc.UpdateSettings(context.Background(), settings)
	require.NoError(t, err)
	svc.ObserveProbe(context.Background(), account, upstreamTestRateSnapshot(*now, 0.8, nil), upstreamTestRateSnapshot(*now, 2.0, nil))
	states := repo.states()
	require.Equal(t, UpstreamChangeNotifyMuted, states[len(states)-1])
}

func TestUpstreamChangeLowBalanceAlert(t *testing.T) {
	settings := DefaultUpstreamChangeAlertSettings()
	settings.BalanceAlertEnabled = true
	settings.BalanceThreshold = 5
	svc, repo, notifier, now := upstreamChangeTestService(t, settings)
	account := upstreamTestAccount()
	high, low, higher := 9.0, 3.0, 20.0

	svc.ObserveProbe(context.Background(), account, upstreamTestRateSnapshot(*now, 1, &high), upstreamTestRateSnapshot(*now, 1, &low))
	require.NoError(t, svc.FlushPending(context.Background()))
	require.Len(t, notifier.sent, 1)
	require.Equal(t, "$3", notifier.sent[0].Value)
	require.Equal(t, "$5", notifier.sent[0].Reference)

	// Staying low does not alert again; recovery is recorded only with history.
	svc.ObserveProbe(context.Background(), account, upstreamTestRateSnapshot(*now, 1, &low), upstreamTestRateSnapshot(*now, 1, &low))
	svc.ObserveProbe(context.Background(), account, upstreamTestRateSnapshot(*now, 1, &low), upstreamTestRateSnapshot(*now, 1, &higher))
	require.Len(t, repo.events, 1)

	// A per-account rule in account ops takes precedence.
	notifier.ruleIDs[account.ID] = true
	*now = now.Add(24 * time.Hour)
	svc.ObserveProbe(context.Background(), account, upstreamTestRateSnapshot(*now, 1, &higher), upstreamTestRateSnapshot(*now, 1, &low))
	require.NoError(t, svc.FlushPending(context.Background()))
	require.Len(t, notifier.sent, 1)
	states := repo.states()
	require.Equal(t, UpstreamChangeNotifySuppressed, states[len(states)-1])
}

func TestUpstreamChangeFailedSendIsMarkedFailed(t *testing.T) {
	settings := DefaultUpstreamChangeAlertSettings()
	settings.RateAlertEnabled = true
	settings.MergeWindowMinutes = 0
	svc, repo, notifier, now := upstreamChangeTestService(t, settings)
	notifier.failSends = true
	svc.ObserveProbe(context.Background(), upstreamTestAccount(), upstreamTestRateSnapshot(*now, 1, nil), upstreamTestRateSnapshot(*now, 3, nil))
	require.NoError(t, svc.FlushPending(context.Background()))
	states := repo.states()
	sort.Strings(states)
	require.Equal(t, []string{UpstreamChangeNotifyFailed}, states)
}

func TestOpsBroadcastMessageFillsTemplateVariables(t *testing.T) {
	s := NewAccountOpsService(nil, nil, nil)
	msg := s.opsBroadcastMessage(upstreamRateChangeMessage(UpstreamChangeEvent{AccountID: 3, AccountName: "a\nb", Site: "https://x"}, 1, 2, UpstreamChangeDirectionUp, 2))
	require.Equal(t, "上游倍率上调", msg.title)
	require.Equal(t, "a b (#3)", msg.account)
	require.Contains(t, msg.balance, "1 → 2")
	require.Equal(t, "上调", msg.threshold)
	require.Contains(t, msg.plain, "合并变动：2 次")
	require.Equal(t, "orange", msg.color)
}
