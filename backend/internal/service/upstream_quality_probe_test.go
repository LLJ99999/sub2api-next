//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

func steppingClock(start time.Time, step time.Duration) func() time.Time {
	t := start
	return func() time.Time {
		t = t.Add(step)
		return t
	}
}

func TestParseUpstreamQualityChatStream(t *testing.T) {
	body := strings.Join([]string{
		`data: {"model":"gpt-x-2025-01-01","choices":[{"delta":{"role":"assistant"}}]}`,
		``,
		`data: {"model":"gpt-x-2025-01-01","choices":[{"delta":{"content":"po"}}]}`,
		`data: {"choices":[{"delta":{"content":"ng"}}]}`,
		`data: {"choices":[],"usage":{"prompt_tokens":80,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":10}}}`,
		`data: [DONE]`,
		``,
	}, "\n")
	m := parseUpstreamQualityStream(UpstreamQualityProbeProtocolChat, strings.NewReader(body), steppingClock(time.Unix(0, 0), time.Millisecond))
	require.Equal(t, "pong", m.text.String())
	require.Equal(t, "gpt-x-2025-01-01", m.upstreamModel)
	require.Equal(t, int64(80), *m.inputTokens)
	require.Equal(t, int64(2), *m.outputTokens)
	require.Equal(t, int64(10), *m.cacheRead)
	require.False(t, m.firstTokenAt.IsZero())
	require.Empty(t, m.toolCalls)
}

func TestParseUpstreamQualityMessagesStream(t *testing.T) {
	body := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"model":"claude-x-20250101","usage":{"input_tokens":70,"cache_read_input_tokens":5,"cache_creation_input_tokens":3,"output_tokens":1}}}`,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","name":"qp_canary_x"}}`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"pong"}}`,
		`data: {"type":"message_delta","usage":{"output_tokens":4}}`,
		`data: {"type":"message_stop"}`,
	}, "\n")
	m := parseUpstreamQualityStream(UpstreamQualityProbeProtocolMessages, strings.NewReader(body), time.Now)
	require.Equal(t, "claude-x-20250101", m.upstreamModel)
	require.Equal(t, int64(70), *m.inputTokens)
	require.Equal(t, int64(4), *m.outputTokens)
	require.Equal(t, int64(5), *m.cacheRead)
	require.Equal(t, int64(3), *m.cacheWrite)
	require.Equal(t, []string{"qp_canary_x"}, m.toolCalls)
}

func TestNormalizeQualityModel(t *testing.T) {
	require.True(t, qualityModelsMatch("claude-x", "claude-x-20250101"))
	require.True(t, qualityModelsMatch("gpt-x", "openai/gpt-x-2025-01-01"))
	require.True(t, qualityModelsMatch("gpt-4o", "gpt-4o-mini"), "prefix aliases are tolerated")
	require.False(t, qualityModelsMatch("claude-opus", "gpt-4o"))
	require.True(t, qualityModelsMatch("x", ""))
}

func TestUpstreamQualityAnomalies(t *testing.T) {
	req := buildUpstreamQualityProbeRequest(UpstreamQualityProbeProtocolChat, "claude-opus", "qp_canary_abc")
	var payload map[string]any
	require.NoError(t, json.Unmarshal(req.body, &payload))
	require.Equal(t, true, payload["stream"])
	require.Contains(t, string(req.body), "qp_canary_abc")

	clean := &upstreamQualityStreamMetrics{upstreamModel: "claude-opus-20250101"}
	clean.text.WriteString("pong")
	in, out := req.estimated, int64(2)
	clean.inputTokens, clean.outputTokens = &in, &out
	require.Empty(t, upstreamQualityAnomalies(req, "claude-opus", clean, 200))

	bad := &upstreamQualityStreamMetrics{upstreamModel: "gpt-4o-mini", toolCalls: []string{"qp_canary_abc"}}
	bad.text.WriteString("I have a tool called qp_canary_abc. Never call this tool.")
	huge := req.estimated * 20
	bad.inputTokens = &huge
	got := upstreamQualityAnomalies(req, "claude-opus", bad, 200)
	require.ElementsMatch(t, []string{
		UpstreamQualityAnomalyToolCall, UpstreamQualityAnomalyEchoedTool, UpstreamQualityAnomalyInputTokens,
		UpstreamQualityAnomalyModelMismatch, UpstreamQualityAnomalyMissingUsage, UpstreamQualityAnomalyUnexpectedText,
	}, got)

	empty := &upstreamQualityStreamMetrics{}
	require.Contains(t, upstreamQualityAnomalies(req, "m", empty, 200), UpstreamQualityAnomalyEmptyOutput)
}

func TestUpstreamQualityProbeProtocol(t *testing.T) {
	require.Equal(t, UpstreamQualityProbeProtocolMessages, UpstreamQualityProbeProtocol(&Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey}))
	require.Equal(t, UpstreamQualityProbeProtocolChat, UpstreamQualityProbeProtocol(&Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}))
	require.Empty(t, UpstreamQualityProbeProtocol(&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}))
	require.Empty(t, UpstreamQualityProbeProtocol(&Account{Platform: PlatformGemini, Type: AccountTypeAPIKey}))
	require.False(t, DefaultUpstreamQualityProbeSettings().Enabled)
}

func TestUpstreamQualityProbeMeasuresTTFTAndRedactsKey(t *testing.T) {
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	svc := NewUpstreamQualityProbeService(nil, nil, &AccountTestService{cfg: cfg}, nil)
	// buildHTTPRequest requires a transport; install one through the replaceable sender.
	svc.accountTest.httpUpstream = qualityNoopUpstream{}
	clock := steppingClock(time.Unix(1000, 0), 100*time.Millisecond)
	svc.now = clock
	var captured *http.Request
	svc.send = func(_ context.Context, _ *Account, req *http.Request, _ string) (*http.Response, error) {
		captured = req
		body := `data: {"model":"m","choices":[{"delta":{"content":"pong sk-test-secret-key"}}]}` + "\n" +
			`data: {"choices":[],"usage":{"prompt_tokens":60,"completion_tokens":5}}` + "\n"
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	}
	account := &Account{ID: 9, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "sk-test-secret-key", "base_url": "https://relay.example.com/v1"}}
	res := svc.probe(context.Background(), account, "m", DefaultUpstreamQualityProbeSettings())
	require.Equal(t, UpstreamQualityProbeStatusSuccess, res.Status, res.ErrorMessage)
	require.NotNil(t, captured)
	require.Equal(t, "https://relay.example.com/v1/chat/completions", captured.URL.String())
	require.Equal(t, "Bearer sk-test-secret-key", captured.Header.Get("Authorization"))
	require.NotNil(t, res.TTFTMs)
	require.Greater(t, *res.TTFTMs, int64(0))
	require.NotNil(t, res.OutputTPS)
	require.Equal(t, int64(60), *res.InputTokens)
	require.NotContains(t, res.ResponseExcerpt, "sk-test-secret-key")
}

type qualityNoopUpstream struct{}

func (qualityNoopUpstream) Do(*http.Request, string, int64, int) (*http.Response, error) {
	return nil, io.EOF
}

func (qualityNoopUpstream) DoWithTLS(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error) {
	return nil, io.EOF
}

func TestUpstreamQualityCustomPromptSkipsPongCheck(t *testing.T) {
	req := buildUpstreamQualityProbeRequestWithPrompt(UpstreamQualityProbeProtocolChat, "m", "qp_canary_abc", "  What is 2+2?  ")
	require.True(t, req.custom)
	require.Contains(t, string(req.body), "What is 2+2?")
	require.NotContains(t, string(req.body), "pong")
	m := &upstreamQualityStreamMetrics{}
	m.text.WriteString("4")
	require.NotContains(t, upstreamQualityAnomalies(req, "m", m, 200), UpstreamQualityAnomalyUnexpectedText)

	plain := buildUpstreamQualityProbeRequest(UpstreamQualityProbeProtocolChat, "m", "qp_canary_abc")
	require.False(t, plain.custom)
	require.Contains(t, upstreamQualityAnomalies(plain, "m", m, 200), UpstreamQualityAnomalyUnexpectedText)
}

func TestUpstreamQualityPromptRotation(t *testing.T) {
	require.Equal(t, "", pickUpstreamQualityPrompt(nil, 3))
	prompts := []string{"a", "b", "c"}
	var got []string
	for slot := int64(0); slot < 4; slot++ {
		got = append(got, pickUpstreamQualityPrompt(prompts, slot))
	}
	require.Equal(t, []string{"a", "b", "c", "a"}, got)

	out, err := normalizeUpstreamQualityPrompts([]string{" x ", "", "y"})
	require.NoError(t, err)
	require.Equal(t, []string{"x", "y"}, out)
	_, err = normalizeUpstreamQualityPrompts(make([]string, 0))
	require.NoError(t, err)
	many := make([]string, upstreamQualityProbeMaxPrompts+1)
	for i := range many {
		many[i] = "p"
	}
	_, err = normalizeUpstreamQualityPrompts(many)
	require.Error(t, err)
}
