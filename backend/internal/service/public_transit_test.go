//go:build unit

package service

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type transitFakePlaza struct{ calls atomic.Int32 }

func (p *transitFakePlaza) ListGroups(context.Context) ([]PlazaGroup, error) {
	p.calls.Add(1)
	in, out, read, write := 0.000003, 0.000015, 0.0000003, 0.00000375
	per := 0.04
	return []PlazaGroup{
		{ID: 11, Name: "Claude 标准", Platform: PlatformAnthropic, RateMultiplier: 0.8, Models: []PlazaModel{
			{Name: "claude-x", Platform: PlatformAnthropic, Pricing: &ChannelModelPricing{ID: 99, ChannelID: 5, BillingMode: BillingModeToken, InputPrice: &in, OutputPrice: &out, CacheReadPrice: &read, CacheWritePrice: &write}},
			{Name: "image-y", Platform: PlatformAnthropic, Pricing: &ChannelModelPricing{BillingMode: BillingMode("per_request"), PerRequestPrice: &per}},
		}},
		{ID: 12, Name: "VIP 专属", Platform: PlatformOpenAI, RateMultiplier: 0.1, IsExclusive: true, Models: []PlazaModel{{Name: "secret-model", Platform: PlatformOpenAI}}},
	}, nil
}

type transitFakeMonitor struct{ err error }

func (m *transitFakeMonitor) ParseFilter(rangeValue string, _, _ []string, _ []int64) (ChannelMonitorV2Filter, error) {
	return ChannelMonitorV2Filter{Range: rangeValue}, nil
}

func (m *transitFakeMonitor) Snapshot(context.Context, ChannelMonitorV2Filter, bool) (*ChannelMonitorV2Snapshot, error) {
	if m.err != nil {
		return nil, m.err
	}
	p50 := int64(900)
	return &ChannelMonitorV2Snapshot{
		Metrics: ChannelMonitorV2Metric{HasSamples: true, RequestCount: 100, SuccessRate: 0.99, CacheRate: 0.42, CacheRateDenominator: 1000, TTFT: ChannelMonitorV2Latency{P50Ms: &p50}},
		Health:  ChannelMonitorV2Health{Overall: "healthy"},
	}, nil
}

func (m *transitFakeMonitor) Matrix(context.Context, ChannelMonitorV2Filter, ChannelMonitorV2GroupBy, bool) (*ChannelMonitorV2Matrix, error) {
	if m.err != nil {
		return nil, m.err
	}
	id11, id12 := int64(11), int64(12)
	p50 := int64(1500)
	return &ChannelMonitorV2Matrix{Items: []ChannelMonitorV2MatrixRow{
		{GroupID: &id11, GroupName: "Claude 标准", Metrics: ChannelMonitorV2Metric{HasSamples: true, RequestCount: 50, SuccessRate: 0.98, Duration: ChannelMonitorV2Latency{P50Ms: &p50}}, Health: ChannelMonitorV2Health{Overall: "warning"}},
		{GroupID: &id12, GroupName: "VIP 专属", Metrics: ChannelMonitorV2Metric{HasSamples: true, RequestCount: 5, SuccessRate: 1}},
	}}, nil
}

func newTransitTestService(t *testing.T, enabled bool) (*PublicTransitService, *transitFakePlaza, *upstreamChangeMemSettings) {
	t.Helper()
	settings := newUpstreamChangeMemSettings()
	if enabled {
		raw, _ := json.Marshal(PublicTransitSettings{Enabled: true, AvailabilityRange: "24h", SiteName: "Demo"})
		require.NoError(t, settings.Set(context.Background(), SettingKeyPublicTransitSettings, string(raw)))
		require.NoError(t, settings.Set(context.Background(), SettingBalanceRechargeMult, "7.2"))
	}
	plaza := &transitFakePlaza{}
	svc := &PublicTransitService{settings: settings, plaza: plaza, monitor: &transitFakeMonitor{}, now: time.Now}
	return svc, plaza, settings
}

func TestPublicTransitDisabledByDefault(t *testing.T) {
	svc, plaza, _ := newTransitTestService(t, false)
	_, err := svc.Snapshot(context.Background())
	require.ErrorIs(t, err, ErrPublicTransitDisabled)
	_, err = svc.Discovery(context.Background())
	require.ErrorIs(t, err, ErrPublicTransitDisabled)
	require.False(t, svc.PageEnabled(context.Background()))
	require.Zero(t, plaza.calls.Load())
}

func TestPublicTransitSnapshotIsAggregatedAndAnonymous(t *testing.T) {
	svc, _, _ := newTransitTestService(t, true)
	snap, err := svc.Snapshot(context.Background())
	require.NoError(t, err)
	require.Equal(t, PublicTransitSchema, snap.Schema)
	require.Equal(t, 7.2, snap.RechargeMultiplier)
	require.Len(t, snap.Groups, 1, "exclusive groups must not be published")
	g := snap.Groups[0]
	require.Equal(t, "Claude 标准", g.Name)
	require.Equal(t, 0.8, g.RateMultiplier)
	require.Equal(t, 2, g.ModelCount)
	require.NotNil(t, g.Availability)
	require.Equal(t, "warning", g.Availability.Status)
	require.Equal(t, 0.98, *g.Availability.Uptime)
	require.Equal(t, int64(1500), *g.Availability.LatencyP50Ms)

	require.Len(t, snap.Models, 2)
	claude := snap.Models[0]
	require.Equal(t, "claude-x", claude.Name)
	require.Equal(t, 3.0, *claude.InputPrice)
	require.Equal(t, 15.0, *claude.OutputPrice)
	require.Equal(t, 0.3, *claude.CacheReadPrice)
	require.Equal(t, 3.75, *claude.CacheWritePrice)
	require.Equal(t, 0.04, *snap.Models[1].PerRequestPrice)
	require.Equal(t, 0.42, *snap.Cache.HitRate)
	require.Equal(t, "30d", snap.Cache.Range)

	raw, err := json.Marshal(snap)
	require.NoError(t, err)
	body := string(raw)
	for _, forbidden := range []string{`"id"`, `"group_id"`, `"channel_id"`, "secret-model", "VIP", "account", "api_key", "proxy", "token\""} {
		require.False(t, strings.Contains(body, forbidden), "snapshot leaked %s: %s", forbidden, body)
	}
}

func TestPublicTransitSnapshotIsCached(t *testing.T) {
	svc, plaza, _ := newTransitTestService(t, true)
	for i := 0; i < 5; i++ {
		_, err := svc.Snapshot(context.Background())
		require.NoError(t, err)
	}
	require.Equal(t, int32(1), plaza.calls.Load())
	now := time.Now().Add(2 * publicTransitCacheTTL)
	svc.now = func() time.Time { return now }
	_, err := svc.Snapshot(context.Background())
	require.NoError(t, err)
	require.Equal(t, int32(2), plaza.calls.Load())
}

func TestPublicTransitMonitorDisabledOmitsAvailability(t *testing.T) {
	svc, _, _ := newTransitTestService(t, true)
	svc.monitor = &transitFakeMonitor{err: ErrChannelMonitorDisabled}
	snap, err := svc.Snapshot(context.Background())
	require.NoError(t, err)
	require.Nil(t, snap.Availability)
	require.Nil(t, snap.Cache)
	require.Nil(t, snap.Groups[0].Availability)
}

func TestPublicTransitSettingsValidation(t *testing.T) {
	s := PublicTransitSettings{PageEnabled: true}
	require.Error(t, ValidatePublicTransitSettings(&s))
	s = PublicTransitSettings{Enabled: true, AvailabilityRange: "1y"}
	require.Error(t, ValidatePublicTransitSettings(&s))
	s = PublicTransitSettings{Enabled: true, SiteName: "  a   b "}
	require.NoError(t, ValidatePublicTransitSettings(&s))
	require.Equal(t, "a b", s.SiteName)
	require.Equal(t, "24h", s.AvailabilityRange)
	require.False(t, DefaultPublicTransitSettings().Enabled)
}

func TestPublicTransitDiscoveryPage(t *testing.T) {
	svc, _, settings := newTransitTestService(t, true)
	d, err := svc.Discovery(context.Background())
	require.NoError(t, err)
	require.Equal(t, PublicTransitSnapshotPath, d.SnapshotURL)
	require.Empty(t, d.PageURL)
	raw, _ := json.Marshal(PublicTransitSettings{Enabled: true, PageEnabled: true})
	require.NoError(t, settings.Set(context.Background(), SettingKeyPublicTransitSettings, string(raw)))
	svc.mu.Lock()
	svc.cfg = nil
	svc.mu.Unlock()
	d, err = svc.Discovery(context.Background())
	require.NoError(t, err)
	require.Equal(t, PublicTransitPagePath, d.PageURL)
	require.True(t, svc.PageEnabled(context.Background()))
}
