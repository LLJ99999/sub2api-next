package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"golang.org/x/sync/singleflight"
)

// Public transit snapshot publishes an aggregated, anonymous view of what this
// site offers: public groups, their models and prices, the cumulative cache
// hit rate and availability from channel monitor V2. It is built only from the
// model plaza (anonymous visibility: exclusive groups are excluded) and the
// monitor aggregates. It never contains upstream accounts, keys, tokens, proxy
// configuration, internal IDs or any user data. Disabled by default.

const (
	SettingKeyPublicTransitSettings = "public_transit_settings"

	PublicTransitSchema          = "ai-transit/v1"
	PublicTransitSnapshotPath    = "/api/public/transit/v1/snapshot"
	PublicTransitDiscoveryPath   = "/.well-known/ai-transit.json"
	PublicTransitPagePath        = "/public/transit"
	publicTransitCacheTTL        = 60 * time.Second
	publicTransitSettingsTTL     = 10 * time.Second
	publicTransitBuildTimeout    = 20 * time.Second
	publicTransitMaxSiteNameRune = 80
	publicTransitPerMillion      = 1_000_000
)

var ErrPublicTransitDisabled = infraerrors.NotFound("PUBLIC_TRANSIT_DISABLED", "public transit snapshot is not enabled")

type PublicTransitSettings struct {
	Enabled     bool `json:"enabled"`
	PageEnabled bool `json:"page_enabled"`
	// SiteName is an optional public display name. Empty omits it.
	SiteName string `json:"site_name"`
	// AvailabilityRange is the monitor window used for availability: 24h | 7d | 30d.
	AvailabilityRange string `json:"availability_range"`
}

func DefaultPublicTransitSettings() PublicTransitSettings {
	return PublicTransitSettings{AvailabilityRange: "24h"}
}

func ValidatePublicTransitSettings(s *PublicTransitSettings) error {
	if s == nil {
		return infraerrors.BadRequest("PUBLIC_TRANSIT_INVALID_SETTINGS", "settings are required")
	}
	s.SiteName = strings.Join(strings.Fields(s.SiteName), " ")
	if len([]rune(s.SiteName)) > publicTransitMaxSiteNameRune {
		return infraerrors.BadRequest("PUBLIC_TRANSIT_INVALID_SETTINGS", "site_name is too long")
	}
	switch s.AvailabilityRange {
	case "":
		s.AvailabilityRange = "24h"
	case "24h", "7d", "30d":
	default:
		return infraerrors.BadRequest("PUBLIC_TRANSIT_INVALID_SETTINGS", "availability_range must be 24h, 7d or 30d")
	}
	if s.PageEnabled && !s.Enabled {
		return infraerrors.BadRequest("PUBLIC_TRANSIT_INVALID_SETTINGS", "enable the snapshot before the public page")
	}
	return nil
}

// Snapshot payload. Field names are part of the public contract.

type PublicTransitDiscovery struct {
	Schema      string `json:"schema"`
	SnapshotURL string `json:"snapshot_url"`
	PageURL     string `json:"page_url,omitempty"`
	SiteName    string `json:"site_name,omitempty"`
	CacheTTL    int    `json:"cache_ttl_seconds"`
}

type PublicTransitSnapshot struct {
	Schema             string                     `json:"schema"`
	GeneratedAt        time.Time                  `json:"generated_at"`
	SiteName           string                     `json:"site_name,omitempty"`
	PriceUnit          string                     `json:"price_unit"`
	RechargeMultiplier float64                    `json:"recharge_multiplier"`
	Groups             []PublicTransitGroup       `json:"groups"`
	Models             []PublicTransitModel       `json:"models"`
	Cache              *PublicTransitCache        `json:"cache"`
	Availability       *PublicTransitAvailability `json:"availability"`
}

type PublicTransitGroup struct {
	Name               string                     `json:"name"`
	Platform           string                     `json:"platform"`
	SubscriptionType   string                     `json:"subscription_type,omitempty"`
	RateMultiplier     float64                    `json:"rate_multiplier"`
	PeakRateEnabled    bool                       `json:"peak_rate_enabled,omitempty"`
	PeakStart          string                     `json:"peak_start,omitempty"`
	PeakEnd            string                     `json:"peak_end,omitempty"`
	PeakRateMultiplier float64                    `json:"peak_rate_multiplier,omitempty"`
	ModelCount         int                        `json:"model_count"`
	Availability       *PublicTransitAvailability `json:"availability,omitempty"`
}

type PublicTransitModel struct {
	Name            string   `json:"name"`
	Group           string   `json:"group"`
	Platform        string   `json:"platform"`
	BillingMode     string   `json:"billing_mode"`
	InputPrice      *float64 `json:"input_price,omitempty"`
	OutputPrice     *float64 `json:"output_price,omitempty"`
	CacheReadPrice  *float64 `json:"cache_read_price,omitempty"`
	CacheWritePrice *float64 `json:"cache_write_price,omitempty"`
	PerRequestPrice *float64 `json:"per_request_price,omitempty"`
}

type PublicTransitCache struct {
	Range   string   `json:"range"`
	HitRate *float64 `json:"hit_rate"`
}

type PublicTransitAvailability struct {
	Range        string   `json:"range"`
	Status       string   `json:"status"`
	Uptime       *float64 `json:"uptime"`
	LatencyP50Ms *int64   `json:"latency_p50_ms"`
	TTFTP50Ms    *int64   `json:"ttft_p50_ms"`
	SampleCount  int64    `json:"sample_count"`
}

type publicTransitPlazaSource interface {
	ListGroups(ctx context.Context) ([]PlazaGroup, error)
}

type publicTransitMonitorSource interface {
	ParseFilter(rangeValue string, platforms, models []string, groupIDs []int64) (ChannelMonitorV2Filter, error)
	Snapshot(ctx context.Context, filter ChannelMonitorV2Filter, admin bool) (*ChannelMonitorV2Snapshot, error)
	Matrix(ctx context.Context, filter ChannelMonitorV2Filter, groupBy ChannelMonitorV2GroupBy, admin bool) (*ChannelMonitorV2Matrix, error)
}

type PublicTransitService struct {
	settings SettingRepository
	plaza    publicTransitPlazaSource
	monitor  publicTransitMonitorSource
	now      func() time.Time

	mu         sync.Mutex
	cached     *PublicTransitSnapshot
	cachedAt   time.Time
	cfg        *PublicTransitSettings
	cfgAt      time.Time
	buildGroup singleflight.Group
}

func NewPublicTransitService(settings SettingRepository, plaza *ModelPlazaService, monitor *ChannelMonitorV2Service) *PublicTransitService {
	s := &PublicTransitService{settings: settings, now: time.Now}
	if plaza != nil {
		s.plaza = plaza
	}
	if monitor != nil {
		s.monitor = monitor
	}
	return s
}

func (s *PublicTransitService) GetSettings(ctx context.Context) (PublicTransitSettings, error) {
	defaults := DefaultPublicTransitSettings()
	if s == nil || s.settings == nil {
		return defaults, nil
	}
	raw, err := s.settings.GetValue(ctx, SettingKeyPublicTransitSettings)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return defaults, nil
		}
		return defaults, err
	}
	if strings.TrimSpace(raw) == "" {
		return defaults, nil
	}
	out := defaults
	if err := json.Unmarshal([]byte(raw), &out); err != nil || ValidatePublicTransitSettings(&out) != nil {
		// Unreadable settings fail closed.
		return defaults, nil
	}
	return out, nil
}

func (s *PublicTransitService) UpdateSettings(ctx context.Context, in PublicTransitSettings) (PublicTransitSettings, error) {
	if err := ValidatePublicTransitSettings(&in); err != nil {
		return in, err
	}
	data, err := json.Marshal(in)
	if err != nil {
		return in, err
	}
	if err := s.settings.Set(ctx, SettingKeyPublicTransitSettings, string(data)); err != nil {
		return in, err
	}
	s.mu.Lock()
	s.cfg, s.cached = nil, nil
	s.mu.Unlock()
	return in, nil
}

// runtimeSettings caches the switch briefly so anonymous traffic does not hit the settings table.
func (s *PublicTransitService) runtimeSettings(ctx context.Context) PublicTransitSettings {
	now := s.now()
	s.mu.Lock()
	if s.cfg != nil && now.Sub(s.cfgAt) < publicTransitSettingsTTL {
		cfg := *s.cfg
		s.mu.Unlock()
		return cfg
	}
	s.mu.Unlock()
	cfg, err := s.GetSettings(ctx)
	if err != nil {
		cfg = DefaultPublicTransitSettings()
	}
	s.mu.Lock()
	s.cfg, s.cfgAt = &cfg, now
	s.mu.Unlock()
	return cfg
}

// Discovery returns the well-known document, or ErrPublicTransitDisabled.
func (s *PublicTransitService) Discovery(ctx context.Context) (*PublicTransitDiscovery, error) {
	cfg := s.runtimeSettings(ctx)
	if !cfg.Enabled {
		return nil, ErrPublicTransitDisabled
	}
	d := &PublicTransitDiscovery{Schema: PublicTransitSchema, SnapshotURL: PublicTransitSnapshotPath, SiteName: cfg.SiteName, CacheTTL: int(publicTransitCacheTTL / time.Second)}
	if cfg.PageEnabled {
		d.PageURL = PublicTransitPagePath
	}
	return d, nil
}

// PageEnabled reports whether the optional public page may render.
func (s *PublicTransitService) PageEnabled(ctx context.Context) bool {
	cfg := s.runtimeSettings(ctx)
	return cfg.Enabled && cfg.PageEnabled
}

// Snapshot returns the cached snapshot, rebuilding at most once per TTL.
func (s *PublicTransitService) Snapshot(ctx context.Context) (*PublicTransitSnapshot, error) {
	cfg := s.runtimeSettings(ctx)
	if !cfg.Enabled {
		return nil, ErrPublicTransitDisabled
	}
	now := s.now()
	s.mu.Lock()
	if s.cached != nil && now.Sub(s.cachedAt) < publicTransitCacheTTL {
		snap := s.cached
		s.mu.Unlock()
		return snap, nil
	}
	s.mu.Unlock()
	value, err, _ := s.buildGroup.Do("snapshot", func() (any, error) {
		buildCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), publicTransitBuildTimeout)
		defer cancel()
		snap, err := s.build(buildCtx, cfg)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.cached, s.cachedAt = snap, s.now()
		s.mu.Unlock()
		return snap, nil
	})
	if err != nil {
		s.mu.Lock()
		stale := s.cached
		s.mu.Unlock()
		if stale != nil {
			return stale, nil
		}
		return nil, err
	}
	return value.(*PublicTransitSnapshot), nil
}

func (s *PublicTransitService) build(ctx context.Context, cfg PublicTransitSettings) (*PublicTransitSnapshot, error) {
	if s.plaza == nil {
		return nil, fmt.Errorf("model plaza unavailable")
	}
	groups, err := s.plaza.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	snap := &PublicTransitSnapshot{
		Schema: PublicTransitSchema, GeneratedAt: s.now().UTC(), SiteName: cfg.SiteName,
		PriceUnit: "USD per 1M tokens (per_request_price: USD per request)", RechargeMultiplier: s.rechargeMultiplier(ctx),
		Groups: []PublicTransitGroup{}, Models: []PublicTransitModel{},
	}
	availability := s.groupAvailability(ctx, cfg.AvailabilityRange)
	for i := range groups {
		g := &groups[i]
		// Anonymous visibility, the same rule as the model plaza: exclusive groups stay private.
		if g.IsExclusive {
			continue
		}
		name := strings.TrimSpace(g.Name)
		pg := PublicTransitGroup{
			Name: name, Platform: g.Platform, SubscriptionType: g.SubscriptionType, RateMultiplier: roundTransit(g.RateMultiplier, 6),
			ModelCount: len(g.Models), Availability: availability[g.ID],
		}
		if g.PeakRateEnabled {
			pg.PeakRateEnabled, pg.PeakStart, pg.PeakEnd, pg.PeakRateMultiplier = true, g.PeakStart, g.PeakEnd, roundTransit(g.PeakRateMultiplier, 6)
		}
		snap.Groups = append(snap.Groups, pg)
		for j := range g.Models {
			snap.Models = append(snap.Models, publicTransitModel(name, &g.Models[j]))
		}
	}
	sort.SliceStable(snap.Models, func(i, j int) bool {
		if snap.Models[i].Group != snap.Models[j].Group {
			return snap.Models[i].Group < snap.Models[j].Group
		}
		return snap.Models[i].Name < snap.Models[j].Name
	})
	snap.Cache = s.cacheRate(ctx)
	snap.Availability = s.overallAvailability(ctx, cfg.AvailabilityRange)
	return snap, nil
}

func publicTransitModel(group string, m *PlazaModel) PublicTransitModel {
	out := PublicTransitModel{Name: m.Name, Group: group, Platform: m.Platform, BillingMode: string(BillingModeToken)}
	p := m.Pricing
	if p == nil {
		return out
	}
	if p.BillingMode != "" {
		out.BillingMode = string(p.BillingMode)
	}
	perMillion := func(v *float64) *float64 {
		if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
			return nil
		}
		r := roundTransit(*v*publicTransitPerMillion, 6)
		return &r
	}
	out.InputPrice = perMillion(p.InputPrice)
	out.OutputPrice = perMillion(p.OutputPrice)
	out.CacheReadPrice = perMillion(p.CacheReadPrice)
	out.CacheWritePrice = perMillion(p.CacheWritePrice)
	if p.PerRequestPrice != nil && !math.IsNaN(*p.PerRequestPrice) && !math.IsInf(*p.PerRequestPrice, 0) {
		r := roundTransit(*p.PerRequestPrice, 6)
		out.PerRequestPrice = &r
	}
	return out
}

func roundTransit(v float64, digits int) float64 {
	scale := math.Pow(10, float64(digits))
	return math.Round(v*scale) / scale
}

func (s *PublicTransitService) rechargeMultiplier(ctx context.Context) float64 {
	if s.settings == nil {
		return defaultBalanceRechargeMultiplier
	}
	raw, err := s.settings.GetValue(ctx, SettingBalanceRechargeMult)
	if err != nil {
		return defaultBalanceRechargeMultiplier
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return defaultBalanceRechargeMultiplier
	}
	return normalizeBalanceRechargeMultiplier(v)
}

func publicTransitAvailabilityFrom(rangeValue string, m ChannelMonitorV2Metric, h ChannelMonitorV2Health) *PublicTransitAvailability {
	a := &PublicTransitAvailability{Range: rangeValue, Status: h.Overall, SampleCount: m.RequestCount}
	if a.Status == "" {
		a.Status = "unknown"
	}
	if m.HasSamples && m.RequestCount > 0 {
		up := roundTransit(m.SuccessRate, 6)
		a.Uptime = &up
	}
	a.LatencyP50Ms = m.Duration.P50Ms
	a.TTFTP50Ms = m.TTFT.P50Ms
	return a
}

// groupAvailability maps internal group IDs to availability. The IDs are only
// used for the join and never leave this service.
func (s *PublicTransitService) groupAvailability(ctx context.Context, rangeValue string) map[int64]*PublicTransitAvailability {
	out := map[int64]*PublicTransitAvailability{}
	if s.monitor == nil {
		return out
	}
	filter, err := s.monitor.ParseFilter(rangeValue, nil, nil, nil)
	if err != nil {
		return out
	}
	matrix, err := s.monitor.Matrix(ctx, filter, ChannelMonitorV2GroupByPlatformGroup, true)
	if err != nil || matrix == nil {
		if err != nil && !errors.Is(err, ErrChannelMonitorDisabled) {
			logger.LegacyPrintf("service.public_transit", "monitor matrix failed: %v", err)
		}
		return out
	}
	for _, row := range matrix.Items {
		if row.GroupID == nil {
			continue
		}
		out[*row.GroupID] = publicTransitAvailabilityFrom(rangeValue, row.Metrics, row.Health)
	}
	return out
}

func (s *PublicTransitService) overallAvailability(ctx context.Context, rangeValue string) *PublicTransitAvailability {
	if s.monitor == nil {
		return nil
	}
	filter, err := s.monitor.ParseFilter(rangeValue, nil, nil, nil)
	if err != nil {
		return nil
	}
	snap, err := s.monitor.Snapshot(ctx, filter, true)
	if err != nil || snap == nil {
		return nil
	}
	return publicTransitAvailabilityFrom(rangeValue, snap.Metrics, snap.Health)
}

// cacheRate is the cumulative cache hit rate over the longest monitor window.
func (s *PublicTransitService) cacheRate(ctx context.Context) *PublicTransitCache {
	if s.monitor == nil {
		return nil
	}
	filter, err := s.monitor.ParseFilter("30d", nil, nil, nil)
	if err != nil {
		return nil
	}
	snap, err := s.monitor.Snapshot(ctx, filter, true)
	if err != nil || snap == nil {
		return nil
	}
	out := &PublicTransitCache{Range: "30d"}
	if snap.Metrics.CacheRateDenominator > 0 {
		rate := roundTransit(snap.Metrics.CacheRate, 6)
		out.HitRate = &rate
	}
	return out
}
