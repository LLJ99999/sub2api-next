package service

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

// Upstream quality probes complement the existing tools rather than replace
// them: channel monitor V2 aggregates real traffic per platform/group/model
// (not per account), and the scheduled connectivity/Pelican tests run through
// the account test path, which records only total latency and the answer text.
// A quality probe sends one small streaming request straight to an API-key
// account's upstream and keeps the numbers the other tools do not see: TTFT,
// output TPS, upstream-reported input/output/cache tokens, the upstream model
// name, and anomaly hints. It runs on the scheduled test runner tick, consumes
// upstream quota, and is therefore off globally and opt-in per account.

const (
	SettingKeyUpstreamQualityProbeSettings = "upstream_quality_probe_settings"

	UpstreamQualityProbeProtocolChat     = "chat_completions"
	UpstreamQualityProbeProtocolMessages = "messages"

	UpstreamQualityProbeStatusSuccess = "success"
	UpstreamQualityProbeStatusFailed  = "failed"

	UpstreamQualityAnomalyToolCall       = "unexpected_tool_call"
	UpstreamQualityAnomalyEchoedTool     = "echoed_tool_config"
	UpstreamQualityAnomalyInputTokens    = "input_tokens_mismatch"
	UpstreamQualityAnomalyModelMismatch  = "upstream_model_mismatch"
	UpstreamQualityAnomalyMissingUsage   = "missing_usage"
	UpstreamQualityAnomalyEmptyOutput    = "empty_output"
	UpstreamQualityAnomalyUnexpectedText = "unexpected_answer"

	upstreamQualityProbeMinInterval   = 5
	upstreamQualityProbeMaxInterval   = 24 * 60
	upstreamQualityProbeDefaultEvery  = 60
	upstreamQualityProbeTimeout       = 90 * time.Second
	upstreamQualityProbeLease         = 5 * time.Minute
	upstreamQualityProbeMaxBody       = 1 << 20
	upstreamQualityProbeMaxPerTick    = 10
	upstreamQualityProbeWorkers       = 4
	upstreamQualityProbeMaxTokens     = 64
	upstreamQualityProbeExcerptRunes  = 300
	upstreamQualityProbeExpected      = "pong"
	upstreamQualityProbeMaxModelRunes = 128
)

var (
	ErrUpstreamQualityProbeInvalid = infraerrors.BadRequest("UPSTREAM_QUALITY_PROBE_INVALID", "invalid upstream quality probe request")
	ErrUpstreamQualityProbeAccount = infraerrors.BadRequest("UPSTREAM_QUALITY_PROBE_ACCOUNT", "quality probes support active API key accounts on OpenAI-compatible or Anthropic upstreams")
)

type UpstreamQualityProbeSettings struct {
	// Enabled is the global switch for scheduled probes. Manual runs ignore it.
	Enabled                bool `json:"enabled"`
	MaxResultsPerAccount   int  `json:"max_results_per_account"`
	InputTokenTolerancePct int  `json:"input_token_tolerance_pct"`
}

func DefaultUpstreamQualityProbeSettings() UpstreamQualityProbeSettings {
	return UpstreamQualityProbeSettings{MaxResultsPerAccount: 100, InputTokenTolerancePct: 200}
}

func ValidateUpstreamQualityProbeSettings(s *UpstreamQualityProbeSettings) error {
	if s.MaxResultsPerAccount < 10 || s.MaxResultsPerAccount > 1000 {
		return infraerrors.BadRequest("UPSTREAM_QUALITY_PROBE_INVALID", "max_results_per_account must be between 10 and 1000")
	}
	if s.InputTokenTolerancePct < 50 || s.InputTokenTolerancePct > 10000 {
		return infraerrors.BadRequest("UPSTREAM_QUALITY_PROBE_INVALID", "input_token_tolerance_pct must be between 50 and 10000")
	}
	return nil
}

type UpstreamQualityProbeConfig struct {
	AccountID       int64  `json:"account_id"`
	AccountName     string `json:"account_name,omitempty"`
	Platform        string `json:"platform,omitempty"`
	Enabled         bool   `json:"enabled"`
	Model           string `json:"model"`
	IntervalMinutes int    `json:"interval_minutes"`
	// Prompts are optional custom probe prompts used round-robin; with any set
	// the fixed "reply pong" content check is disabled.
	Prompts []string `json:"prompts"`
	// PromptSlot is the rotation slot a claimed run uses (scheduler only).
	PromptSlot int64      `json:"-"`
	NextRunAt  *time.Time `json:"next_run_at"`
	LastRunAt  *time.Time `json:"last_run_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	// Latest is filled by list endpoints.
	Latest *UpstreamQualityProbeResult `json:"latest,omitempty"`
}

type UpstreamQualityProbeResult struct {
	ID                   int64     `json:"id"`
	AccountID            int64     `json:"account_id"`
	Trigger              string    `json:"trigger"`
	Protocol             string    `json:"protocol"`
	Model                string    `json:"model"`
	UpstreamModel        string    `json:"upstream_model"`
	Status               string    `json:"status"`
	HTTPStatus           int       `json:"http_status"`
	ErrorMessage         string    `json:"error_message,omitempty"`
	TTFTMs               *int64    `json:"ttft_ms"`
	TotalMs              int64     `json:"total_ms"`
	OutputTPS            *float64  `json:"output_tps"`
	InputTokens          *int64    `json:"input_tokens"`
	OutputTokens         *int64    `json:"output_tokens"`
	CacheReadTokens      *int64    `json:"cache_read_tokens"`
	CacheWriteTokens     *int64    `json:"cache_write_tokens"`
	EstimatedInputTokens int64     `json:"estimated_input_tokens"`
	Anomalies            []string  `json:"anomalies"`
	ResponseExcerpt      string    `json:"response_excerpt,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
}

type UpstreamQualityProbeRepository interface {
	GetConfig(ctx context.Context, accountID int64) (*UpstreamQualityProbeConfig, error)
	UpsertConfig(ctx context.Context, cfg *UpstreamQualityProbeConfig) error
	DeleteConfig(ctx context.Context, accountID int64) error
	ListConfigs(ctx context.Context) ([]UpstreamQualityProbeConfig, error)
	// ClaimDue leases due enabled configs (FOR UPDATE SKIP LOCKED) and moves
	// next_run_at forward, so concurrent instances never probe one account twice.
	ClaimDue(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]UpstreamQualityProbeConfig, error)
	FinishRun(ctx context.Context, accountID int64, finishedAt time.Time) error
	InsertResult(ctx context.Context, r *UpstreamQualityProbeResult) error
	ListResults(ctx context.Context, accountID int64, limit int) ([]UpstreamQualityProbeResult, error)
	LatestResults(ctx context.Context) (map[int64]UpstreamQualityProbeResult, error)
	PruneResults(ctx context.Context, accountID int64, keep int) error
}

type UpstreamQualityProbeService struct {
	repo        UpstreamQualityProbeRepository
	accounts    AccountRepository
	accountTest *AccountTestService
	settings    SettingRepository
	now         func() time.Time
	// send is replaceable in tests.
	send  func(ctx context.Context, account *Account, req *http.Request, proxyURL string) (*http.Response, error)
	runMu sync.Mutex
}

func NewUpstreamQualityProbeService(repo UpstreamQualityProbeRepository, accounts AccountRepository, accountTest *AccountTestService, settings SettingRepository) *UpstreamQualityProbeService {
	s := &UpstreamQualityProbeService{repo: repo, accounts: accounts, accountTest: accountTest, settings: settings, now: time.Now}
	s.send = s.defaultSend
	return s
}

func (s *UpstreamQualityProbeService) GetSettings(ctx context.Context) (UpstreamQualityProbeSettings, error) {
	defaults := DefaultUpstreamQualityProbeSettings()
	if s == nil || s.settings == nil {
		return defaults, nil
	}
	raw, err := s.settings.GetValue(ctx, SettingKeyUpstreamQualityProbeSettings)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return defaults, nil
		}
		return defaults, err
	}
	out := defaults
	if strings.TrimSpace(raw) == "" {
		return defaults, nil
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || ValidateUpstreamQualityProbeSettings(&out) != nil {
		return defaults, nil
	}
	return out, nil
}

func (s *UpstreamQualityProbeService) UpdateSettings(ctx context.Context, in UpstreamQualityProbeSettings) (UpstreamQualityProbeSettings, error) {
	if err := ValidateUpstreamQualityProbeSettings(&in); err != nil {
		return in, err
	}
	data, _ := json.Marshal(in)
	return in, s.settings.Set(ctx, SettingKeyUpstreamQualityProbeSettings, string(data))
}

// UpstreamQualityProbeProtocol chooses the request shape for an account, or "" if unsupported.
func UpstreamQualityProbeProtocol(a *Account) string {
	if a == nil || a.Type != AccountTypeAPIKey {
		return ""
	}
	switch a.Platform {
	case PlatformGemini, PlatformAntigravity:
		return ""
	case PlatformAnthropic:
		return UpstreamQualityProbeProtocolMessages
	}
	return UpstreamQualityProbeProtocolChat
}

func (s *UpstreamQualityProbeService) loadProbeAccount(ctx context.Context, accountID int64) (*Account, error) {
	if s.accounts == nil {
		return nil, ErrUpstreamQualityProbeAccount
	}
	a, err := s.accounts.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if UpstreamQualityProbeProtocol(a) == "" {
		return nil, ErrUpstreamQualityProbeAccount
	}
	return a, nil
}

func (s *UpstreamQualityProbeService) SaveConfig(ctx context.Context, cfg UpstreamQualityProbeConfig) (*UpstreamQualityProbeConfig, error) {
	cfg.Model = strings.TrimSpace(cfg.Model)
	if cfg.IntervalMinutes == 0 {
		cfg.IntervalMinutes = upstreamQualityProbeDefaultEvery
	}
	if cfg.IntervalMinutes < upstreamQualityProbeMinInterval || cfg.IntervalMinutes > upstreamQualityProbeMaxInterval {
		return nil, infraerrors.BadRequest("UPSTREAM_QUALITY_PROBE_INVALID", fmt.Sprintf("interval_minutes must be between %d and %d", upstreamQualityProbeMinInterval, upstreamQualityProbeMaxInterval))
	}
	if cfg.Model == "" || utf8.RuneCountInString(cfg.Model) > upstreamQualityProbeMaxModelRunes || strings.ContainsAny(cfg.Model, "\r\n\t ") {
		return nil, infraerrors.BadRequest("UPSTREAM_QUALITY_PROBE_INVALID", "model is required and must be one model name")
	}
	if cfg.Prompts == nil {
		// Omitted prompts keep what is saved (bulk enable/disable must not wipe them).
		if current, err := s.repo.GetConfig(ctx, cfg.AccountID); err == nil && current != nil {
			cfg.Prompts = current.Prompts
		}
	}
	prompts, err := normalizeUpstreamQualityPrompts(cfg.Prompts)
	if err != nil {
		return nil, err
	}
	cfg.Prompts = prompts
	if _, err := s.loadProbeAccount(ctx, cfg.AccountID); err != nil {
		return nil, err
	}
	// A saved change runs on the next tick instead of waiting for the old schedule.
	cfg.NextRunAt = nil
	if err := s.repo.UpsertConfig(ctx, &cfg); err != nil {
		return nil, err
	}
	return s.repo.GetConfig(ctx, cfg.AccountID)
}

func (s *UpstreamQualityProbeService) DeleteConfig(ctx context.Context, accountID int64) error {
	return s.repo.DeleteConfig(ctx, accountID)
}

func (s *UpstreamQualityProbeService) ListConfigs(ctx context.Context) ([]UpstreamQualityProbeConfig, error) {
	configs, err := s.repo.ListConfigs(ctx)
	if err != nil {
		return nil, err
	}
	latest, err := s.repo.LatestResults(ctx)
	if err != nil {
		return nil, err
	}
	for i := range configs {
		if r, ok := latest[configs[i].AccountID]; ok {
			r := r
			configs[i].Latest = &r
		}
	}
	if configs == nil {
		configs = []UpstreamQualityProbeConfig{}
	}
	return configs, nil
}

type UpstreamQualityProbeAccountView struct {
	Config  *UpstreamQualityProbeConfig  `json:"config"`
	Results []UpstreamQualityProbeResult `json:"results"`
}

func (s *UpstreamQualityProbeService) AccountView(ctx context.Context, accountID int64, limit int) (*UpstreamQualityProbeAccountView, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	cfg, err := s.repo.GetConfig(ctx, accountID)
	if err != nil {
		return nil, err
	}
	results, err := s.repo.ListResults(ctx, accountID, limit)
	if err != nil {
		return nil, err
	}
	if results == nil {
		results = []UpstreamQualityProbeResult{}
	}
	return &UpstreamQualityProbeAccountView{Config: cfg, Results: results}, nil
}

const (
	upstreamQualityProbeMaxPrompts     = 20
	upstreamQualityProbeMaxPromptRunes = 8000
	// upstreamQualityProbeCustomMaxTokens leaves room for a real answer to a custom prompt.
	upstreamQualityProbeCustomMaxTokens = 512
)

func normalizeUpstreamQualityPrompts(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	for _, p := range in {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if utf8.RuneCountInString(p) > upstreamQualityProbeMaxPromptRunes {
			return nil, infraerrors.BadRequest("UPSTREAM_QUALITY_PROBE_INVALID", fmt.Sprintf("each prompt must be at most %d characters", upstreamQualityProbeMaxPromptRunes))
		}
		out = append(out, p)
	}
	if len(out) > upstreamQualityProbeMaxPrompts {
		return nil, infraerrors.BadRequest("UPSTREAM_QUALITY_PROBE_INVALID", fmt.Sprintf("at most %d prompts", upstreamQualityProbeMaxPrompts))
	}
	return out, nil
}

func pickUpstreamQualityPrompt(prompts []string, slot int64) string {
	if len(prompts) == 0 {
		return ""
	}
	n := int64(len(prompts))
	return prompts[((slot%n)+n)%n]
}

type upstreamQualityPromptRotator interface {
	NextPromptIndex(ctx context.Context, accountID int64) (int64, error)
}

// RunNow probes one account immediately with an explicit model. It ignores
// the global switch because an admin asked for it, and records the result.
func (s *UpstreamQualityProbeService) RunNow(ctx context.Context, accountID int64, model string) (*UpstreamQualityProbeResult, error) {
	model = strings.TrimSpace(model)
	var prompt string
	if cfg, err := s.repo.GetConfig(ctx, accountID); err == nil && cfg != nil {
		if model == "" {
			model = cfg.Model
		}
		if len(cfg.Prompts) > 0 {
			var slot int64
			if rotator, ok := s.repo.(upstreamQualityPromptRotator); ok {
				slot, _ = rotator.NextPromptIndex(ctx, accountID)
			}
			prompt = pickUpstreamQualityPrompt(cfg.Prompts, slot)
		}
	}
	if model == "" || strings.ContainsAny(model, "\r\n\t ") || utf8.RuneCountInString(model) > upstreamQualityProbeMaxModelRunes {
		return nil, infraerrors.BadRequest("UPSTREAM_QUALITY_PROBE_INVALID", "model is required")
	}
	account, err := s.loadProbeAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	settings, _ := s.GetSettings(ctx)
	result := s.probe(ctx, account, model, prompt, settings)
	result.Trigger = "manual"
	if err := s.repo.InsertResult(ctx, result); err != nil {
		return nil, err
	}
	_ = s.repo.PruneResults(ctx, accountID, settings.MaxResultsPerAccount)
	return result, nil
}

// RunDue is called from the scheduled test runner every minute.
func (s *UpstreamQualityProbeService) RunDue(ctx context.Context, now time.Time) {
	if s == nil || s.repo == nil {
		return
	}
	if !s.runMu.TryLock() {
		return
	}
	defer s.runMu.Unlock()
	settings, err := s.GetSettings(ctx)
	if err != nil || !settings.Enabled {
		return
	}
	due, err := s.repo.ClaimDue(ctx, now, upstreamQualityProbeLease, upstreamQualityProbeMaxPerTick)
	if err != nil {
		logger.LegacyPrintf("service.upstream_quality_probe", "claim due failed: %v", err)
		return
	}
	sem := make(chan struct{}, upstreamQualityProbeWorkers)
	var wg sync.WaitGroup
	for _, cfg := range due {
		cfg := cfg
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				if recover() != nil {
					logger.LegacyPrintf("service.upstream_quality_probe", "probe panicked: account_id=%d", cfg.AccountID)
				}
				_ = s.repo.FinishRun(context.WithoutCancel(ctx), cfg.AccountID, s.now())
			}()
			account, err := s.loadProbeAccount(ctx, cfg.AccountID)
			if err != nil || !account.IsActive() {
				return
			}
			result := s.probe(ctx, account, cfg.Model, pickUpstreamQualityPrompt(cfg.Prompts, cfg.PromptSlot), settings)
			result.Trigger = "scheduled"
			if err := s.repo.InsertResult(ctx, result); err != nil {
				logger.LegacyPrintf("service.upstream_quality_probe", "insert result failed: account_id=%d err=%v", cfg.AccountID, err)
				return
			}
			_ = s.repo.PruneResults(ctx, cfg.AccountID, settings.MaxResultsPerAccount)
		}()
	}
	wg.Wait()
}

// upstreamQualityProbeRequest is the request we send plus what we expect back.
type upstreamQualityProbeRequest struct {
	protocol  string
	body      []byte
	canary    string
	estimated int64
	// custom marks a run with an admin prompt: the reply is not checked for "pong".
	custom bool
}

func newUpstreamQualityProbeCanary() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "qp_canary_" + hex.EncodeToString(b)
}

// buildUpstreamQualityProbeRequestWithPrompt sends an admin's custom prompt in
// place of ping/pong; the canary tool stays so tool-call/echo checks still apply.
func buildUpstreamQualityProbeRequestWithPrompt(protocol, model, canary, prompt string) upstreamQualityProbeRequest {
	system := "You are a connectivity check. Reply with exactly the single word: pong. Do not call any tool."
	user := "ping"
	maxTokens := upstreamQualityProbeMaxTokens
	custom := strings.TrimSpace(prompt) != ""
	if custom {
		system = "Answer the user's message directly. Do not call any tool."
		user = strings.TrimSpace(prompt)
		maxTokens = upstreamQualityProbeCustomMaxTokens
	}
	toolDesc := "Internal diagnostic tool. Never call this tool."
	var payload map[string]any
	if protocol == UpstreamQualityProbeProtocolMessages {
		payload = map[string]any{
			"model": model, "max_tokens": maxTokens, "stream": true, "system": system,
			"messages": []map[string]any{{"role": "user", "content": user}},
			"tools": []map[string]any{{
				"name": canary, "description": toolDesc,
				"input_schema": map[string]any{"type": "object", "properties": map[string]any{}},
			}},
			"tool_choice": map[string]any{"type": "auto"},
		}
	} else {
		payload = map[string]any{
			"model": model, "max_tokens": maxTokens, "stream": true,
			"stream_options": map[string]any{"include_usage": true},
			"messages":       []map[string]any{{"role": "system", "content": system}, {"role": "user", "content": user}},
			"tools": []map[string]any{{"type": "function", "function": map[string]any{
				"name": canary, "description": toolDesc,
				"parameters": map[string]any{"type": "object", "properties": map[string]any{}},
			}}},
			"tool_choice": "auto",
		}
	}
	body, _ := json.Marshal(payload)
	return upstreamQualityProbeRequest{protocol: protocol, body: body, canary: canary, custom: custom, estimated: estimateUpstreamQualityInputTokens(system, user, canary, toolDesc)}
}

// estimateUpstreamQualityInputTokens is a deliberately rough local estimate:
// ~4 characters per token for the text, plus a fixed allowance for message,
// tool-schema and role framing that providers add.
func estimateUpstreamQualityInputTokens(parts ...string) int64 {
	chars := 0
	for _, p := range parts {
		chars += len(p)
	}
	return int64(math.Ceil(float64(chars)/4.0)) + 60
}

// upstreamQualityStreamMetrics is what we read from one streamed response.
type upstreamQualityStreamMetrics struct {
	firstTokenAt  time.Time
	upstreamModel string
	text          strings.Builder
	toolCalls     []string
	inputTokens   *int64
	outputTokens  *int64
	cacheRead     *int64
	cacheWrite    *int64
	streamError   string
}

func jsonInt(v any) (*int64, bool) {
	n, ok := opsNumber(v)
	if !ok || n < 0 {
		return nil, false
	}
	out := int64(n)
	return &out, true
}

// parseUpstreamQualityStream reads an SSE body. now is called when a line
// arrives, so TTFT is the time of the first content or tool delta.
func parseUpstreamQualityStream(protocol string, body io.Reader, now func() time.Time) *upstreamQualityStreamMetrics {
	m := &upstreamQualityStreamMetrics{}
	reader := bufio.NewReaderSize(io.LimitReader(body, upstreamQualityProbeMaxBody), 64*1024)
	markFirst := func() {
		if m.firstTokenAt.IsZero() {
			m.firstTokenAt = now()
		}
	}
	for {
		line, err := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data != "" && data != "[DONE]" {
				var event map[string]any
				if json.Unmarshal([]byte(data), &event) == nil {
					if protocol == UpstreamQualityProbeProtocolMessages {
						parseQualityMessagesEvent(m, event, markFirst)
					} else {
						parseQualityChatEvent(m, event, markFirst)
					}
				}
			}
		}
		if err != nil {
			break
		}
	}
	return m
}

func parseQualityChatEvent(m *upstreamQualityStreamMetrics, event map[string]any, markFirst func()) {
	if e, ok := event["error"].(map[string]any); ok {
		msg, _ := e["message"].(string)
		m.streamError = truncateRunes(msg, upstreamQualityProbeExcerptRunes)
		return
	}
	if model, ok := event["model"].(string); ok && model != "" {
		m.upstreamModel = model
	}
	if choices, ok := event["choices"].([]any); ok {
		for _, c := range choices {
			choice, _ := c.(map[string]any)
			delta, _ := choice["delta"].(map[string]any)
			if delta == nil {
				delta, _ = choice["message"].(map[string]any)
			}
			if delta == nil {
				continue
			}
			if text, _ := delta["content"].(string); text != "" {
				markFirst()
				_, _ = m.text.WriteString(text)
			}
			if r, _ := delta["reasoning_content"].(string); r != "" {
				markFirst()
			}
			if calls, ok := delta["tool_calls"].([]any); ok && len(calls) > 0 {
				markFirst()
				for _, call := range calls {
					cm, _ := call.(map[string]any)
					fn, _ := cm["function"].(map[string]any)
					if name, _ := fn["name"].(string); name != "" {
						m.toolCalls = append(m.toolCalls, name)
					} else if len(m.toolCalls) == 0 {
						m.toolCalls = append(m.toolCalls, "?")
					}
				}
			}
		}
	}
	if usage, ok := event["usage"].(map[string]any); ok {
		if v, ok := jsonInt(usage["prompt_tokens"]); ok {
			m.inputTokens = v
		}
		if v, ok := jsonInt(usage["completion_tokens"]); ok {
			m.outputTokens = v
		}
		if details, ok := usage["prompt_tokens_details"].(map[string]any); ok {
			if v, ok := jsonInt(details["cached_tokens"]); ok {
				m.cacheRead = v
			}
		}
		// Some OpenAI-compatible relays forward Anthropic-style cache fields.
		if v, ok := jsonInt(usage["cache_read_input_tokens"]); ok {
			m.cacheRead = v
		}
		if v, ok := jsonInt(usage["cache_creation_input_tokens"]); ok {
			m.cacheWrite = v
		}
	}
}

func parseQualityMessagesEvent(m *upstreamQualityStreamMetrics, event map[string]any, markFirst func()) {
	readUsage := func(usage map[string]any) {
		if usage == nil {
			return
		}
		if v, ok := jsonInt(usage["input_tokens"]); ok {
			m.inputTokens = v
		}
		if v, ok := jsonInt(usage["output_tokens"]); ok {
			m.outputTokens = v
		}
		if v, ok := jsonInt(usage["cache_read_input_tokens"]); ok {
			m.cacheRead = v
		}
		if v, ok := jsonInt(usage["cache_creation_input_tokens"]); ok {
			m.cacheWrite = v
		}
	}
	switch event["type"] {
	case "message_start":
		msg, _ := event["message"].(map[string]any)
		if model, _ := msg["model"].(string); model != "" {
			m.upstreamModel = model
		}
		usage, _ := msg["usage"].(map[string]any)
		readUsage(usage)
	case "content_block_start":
		block, _ := event["content_block"].(map[string]any)
		if t, _ := block["type"].(string); t == "tool_use" || t == "server_tool_use" {
			markFirst()
			name, _ := block["name"].(string)
			if name == "" {
				name = "?"
			}
			m.toolCalls = append(m.toolCalls, name)
		}
	case "content_block_delta":
		delta, _ := event["delta"].(map[string]any)
		switch delta["type"] {
		case "text_delta":
			if text, _ := delta["text"].(string); text != "" {
				markFirst()
				_, _ = m.text.WriteString(text)
			}
		case "thinking_delta", "input_json_delta":
			markFirst()
		}
	case "message_delta":
		usage, _ := event["usage"].(map[string]any)
		readUsage(usage)
	case "error":
		e, _ := event["error"].(map[string]any)
		msg, _ := e["message"].(string)
		m.streamError = truncateRunes(msg, upstreamQualityProbeExcerptRunes)
	}
}

func truncateRunes(s string, n int) string {
	s = strings.ToValidUTF8(s, "")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

// normalizeQualityModel drops a provider prefix and a trailing date stamp so
// "anthropic/claude-x-20250101" and "claude-x" compare equal.
func normalizeQualityModel(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(model, "/"); i >= 0 {
		model = model[i+1:]
	}
	parts := strings.Split(model, "-")
	if n := len(parts); n > 1 {
		last := parts[n-1]
		if len(last) == 8 && strings.Trim(last, "0123456789") == "" {
			parts = parts[:n-1]
		} else if n > 3 && len(parts[n-3]) == 4 && len(parts[n-2]) == 2 && len(last) == 2 &&
			strings.Trim(parts[n-3]+parts[n-2]+last, "0123456789") == "" {
			parts = parts[:n-3]
		}
	}
	return strings.Join(parts, "-")
}

func qualityModelsMatch(requested, upstream string) bool {
	a, b := normalizeQualityModel(requested), normalizeQualityModel(upstream)
	if a == "" || b == "" {
		return true
	}
	return a == b || strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

// upstreamQualityAnomalies turns metrics into hint codes. They are hints, not
// verdicts: a relay may legitimately add a system prompt or alias a model.
func upstreamQualityAnomalies(req upstreamQualityProbeRequest, requestedModel string, m *upstreamQualityStreamMetrics, tolerancePct int) []string {
	out := []string{}
	if len(m.toolCalls) > 0 {
		out = append(out, UpstreamQualityAnomalyToolCall)
	}
	text := m.text.String()
	lower := strings.ToLower(text)
	if strings.Contains(lower, strings.ToLower(req.canary)) || strings.Contains(lower, "never call this tool") {
		out = append(out, UpstreamQualityAnomalyEchoedTool)
	}
	if m.inputTokens == nil || m.outputTokens == nil {
		out = append(out, UpstreamQualityAnomalyMissingUsage)
	}
	if m.inputTokens != nil && req.estimated > 0 {
		reported := float64(*m.inputTokens + valueOrZero(m.cacheRead) + valueOrZero(m.cacheWrite))
		if req.protocol == UpstreamQualityProbeProtocolChat {
			// OpenAI reports cached tokens inside prompt_tokens.
			reported = float64(*m.inputTokens)
		}
		limit := float64(req.estimated) * (1 + float64(tolerancePct)/100)
		if reported > limit || reported < float64(req.estimated)/(1+float64(tolerancePct)/100)/2 {
			out = append(out, UpstreamQualityAnomalyInputTokens)
		}
	}
	if m.upstreamModel != "" && !qualityModelsMatch(requestedModel, m.upstreamModel) {
		out = append(out, UpstreamQualityAnomalyModelMismatch)
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" && len(m.toolCalls) == 0 {
		out = append(out, UpstreamQualityAnomalyEmptyOutput)
	} else if trimmed != "" && !req.custom && !strings.Contains(strings.ToLower(trimmed), upstreamQualityProbeExpected) {
		out = append(out, UpstreamQualityAnomalyUnexpectedText)
	}
	return out
}

func (s *UpstreamQualityProbeService) currentTime() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *UpstreamQualityProbeService) probe(ctx context.Context, account *Account, model, prompt string, settings UpstreamQualityProbeSettings) *UpstreamQualityProbeResult {
	protocol := UpstreamQualityProbeProtocol(account)
	upstreamModel := account.GetMappedModel(model)
	if upstreamModel == "" {
		upstreamModel = model
	}
	result := &UpstreamQualityProbeResult{
		AccountID: account.ID, Protocol: protocol, Model: model, Status: UpstreamQualityProbeStatusFailed,
		Anomalies: []string{}, CreatedAt: s.currentTime().UTC(),
	}
	req := buildUpstreamQualityProbeRequestWithPrompt(protocol, upstreamModel, newUpstreamQualityProbeCanary(), prompt)
	result.EstimatedInputTokens = req.estimated
	httpReq, proxyURL, err := s.buildHTTPRequest(ctx, account, req)
	if err != nil {
		result.ErrorMessage = err.Error()
		return result
	}
	probeCtx, cancel := context.WithTimeout(ctx, upstreamQualityProbeTimeout)
	defer cancel()
	httpReq = httpReq.WithContext(WithHTTPUpstreamRedirectsDisabled(WithHTTPUpstreamProfile(probeCtx, upstreamQualityProfile(account))))
	start := s.currentTime()
	resp, err := s.send(probeCtx, account, httpReq, proxyURL)
	if err != nil {
		result.TotalMs = s.currentTime().Sub(start).Milliseconds()
		result.ErrorMessage = "request_failed"
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
			result.ErrorMessage = "timeout"
		}
		return result
	}
	defer func() { _ = resp.Body.Close() }()
	result.HTTPStatus = resp.StatusCode
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		result.TotalMs = s.currentTime().Sub(start).Milliseconds()
		result.ErrorMessage = fmt.Sprintf("http_%d", resp.StatusCode)
		result.ResponseExcerpt = truncateRunes(sanitizeQualityExcerpt(string(body), account), upstreamQualityProbeExcerptRunes)
		return result
	}
	metrics := parseUpstreamQualityStream(protocol, resp.Body, s.currentTime)
	end := s.currentTime()
	result.TotalMs = end.Sub(start).Milliseconds()
	result.UpstreamModel = truncateRunes(metrics.upstreamModel, upstreamQualityProbeMaxModelRunes)
	result.InputTokens, result.OutputTokens = metrics.inputTokens, metrics.outputTokens
	result.CacheReadTokens, result.CacheWriteTokens = metrics.cacheRead, metrics.cacheWrite
	result.ResponseExcerpt = truncateRunes(sanitizeQualityExcerpt(metrics.text.String(), account), upstreamQualityProbeExcerptRunes)
	if !metrics.firstTokenAt.IsZero() {
		ttft := metrics.firstTokenAt.Sub(start).Milliseconds()
		result.TTFTMs = &ttft
		if gen := end.Sub(metrics.firstTokenAt).Seconds(); gen > 0 && metrics.outputTokens != nil && *metrics.outputTokens > 0 {
			tps := math.Round(float64(*metrics.outputTokens)/gen*100) / 100
			result.OutputTPS = &tps
		}
	}
	if metrics.streamError != "" {
		result.ErrorMessage = "stream_error: " + sanitizeQualityExcerpt(metrics.streamError, account)
		result.Anomalies = upstreamQualityAnomalies(req, upstreamModel, metrics, settings.InputTokenTolerancePct)
		return result
	}
	result.Anomalies = upstreamQualityAnomalies(req, upstreamModel, metrics, settings.InputTokenTolerancePct)
	result.Status = UpstreamQualityProbeStatusSuccess
	return result
}

// sanitizeQualityExcerpt removes the account key from echoed upstream text.
func sanitizeQualityExcerpt(text string, account *Account) string {
	if key := account.GetCredential("api_key"); len(key) >= 8 {
		text = strings.ReplaceAll(text, key, "[redacted]")
	}
	return text
}

func upstreamQualityProfile(account *Account) HTTPUpstreamProfile {
	if account.Platform == PlatformOpenAI {
		return HTTPUpstreamProfileOpenAI
	}
	return HTTPUpstreamProfileDefault
}

func (s *UpstreamQualityProbeService) buildHTTPRequest(ctx context.Context, account *Account, req upstreamQualityProbeRequest) (*http.Request, string, error) {
	if s.accountTest == nil || s.accountTest.httpUpstream == nil {
		return nil, "", errors.New("transport_unavailable")
	}
	apiKey := account.GetCredential("api_key")
	if apiKey == "" {
		return nil, "", errors.New("missing_api_key")
	}
	baseURL := account.GetCredential("base_url")
	if baseURL == "" {
		switch account.Platform {
		case PlatformOpenAI:
			baseURL = "https://api.openai.com"
		case PlatformAnthropic:
			baseURL = "https://api.anthropic.com"
		default:
			return nil, "", errors.New("missing_base_url")
		}
	}
	normalized, err := s.accountTest.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return nil, "", errors.New("invalid_base_url")
	}
	endpoint := "/v1/chat/completions"
	if req.protocol == UpstreamQualityProbeProtocolMessages {
		endpoint = "/v1/messages"
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, buildOpenAIEndpointURL(normalized, endpoint), bytes.NewReader(req.body))
	if err != nil {
		return nil, "", errors.New("request_build_failed")
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if req.protocol == UpstreamQualityProbeProtocolMessages {
		httpReq.Header.Set("x-api-key", apiKey)
		httpReq.Header.Set("anthropic-version", "2023-06-01")
	} else {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}
	account.ApplyHeaderOverrides(httpReq.Header)
	proxyURL := ""
	if account.ProxyID != nil {
		if account.Proxy == nil {
			return nil, "", errors.New("proxy_unavailable")
		}
		proxyURL = account.Proxy.URL()
	}
	return httpReq, proxyURL, nil
}

func (s *UpstreamQualityProbeService) defaultSend(_ context.Context, account *Account, req *http.Request, proxyURL string) (*http.Response, error) {
	var tlsProfile *tlsfingerprint.Profile
	if s.accountTest.tlsFPProfileService != nil {
		tlsProfile = s.accountTest.tlsFPProfileService.ResolveTLSProfile(account)
	}
	return s.accountTest.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, tlsProfile)
}
