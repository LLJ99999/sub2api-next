package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const turnstileVerifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

type turnstileVerifier struct {
	httpClient *http.Client
	verifyURL  string
	// capHTTPClient 用于自建 Cap 服务的 siteverify 请求。Cap 通常部署在内网/本机
	// （如 docker 内 http://cap:3000），因此不做解析后 IP 校验；其地址只来自运维配置。
	capHTTPClient *http.Client
	// capBaseURL 显式指定 Cap 服务基址（测试用）；为空时按 resolveCapBaseURL 规则解析。
	capBaseURL string
}

func NewTurnstileVerifier() service.TurnstileVerifier {
	sharedClient, err := httpclient.GetClient(httpclient.Options{
		Timeout:            10 * time.Second,
		ValidateResolvedIP: true,
	})
	if err != nil {
		sharedClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &turnstileVerifier{
		httpClient:    sharedClient,
		verifyURL:     turnstileVerifyURL,
		capHTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (v *turnstileVerifier) VerifyToken(ctx context.Context, secretKey, token, remoteIP string) (*service.TurnstileVerifyResponse, error) {
	if cfg, ok, err := parseCapSecret(secretKey); ok {
		if err != nil {
			return nil, err
		}
		return v.verifyCap(ctx, cfg, token)
	}
	formData := url.Values{}
	formData.Set("secret", secretKey)
	formData.Set("response", token)
	if remoteIP != "" {
		formData.Set("remoteip", remoteIP)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.verifyURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := v.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result service.TurnstileVerifyResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &result, nil
}

// ---------------------------------------------------------------------------
// 自建 Cap (https://capjs.js.org) 验证码支持
//
// 管理后台 turnstile_secret_key 填写:
//   cap:<site_key>:<secret_key>               使用默认 Cap 基址
//   cap:<base_url>|<site_key>:<secret_key>    显式指定 Cap 基址
// 默认基址解析顺序: 环境变量 CAP_BASE_URL → 环境变量 SERVER_FRONTEND_URL + "/cap"。
// 验证请求: POST <base>/<site_key>/siteverify  JSON {"secret": ..., "response": ...}
// ---------------------------------------------------------------------------

const capSecretPrefix = "cap:"

type capSecretConfig struct {
	BaseURL string // 可能为空，表示使用默认基址
	SiteKey string
	Secret  string
}

// parseCapSecret 解析 cap: 前缀的 secret。第二个返回值表示是否为 Cap 格式。
func parseCapSecret(raw string) (capSecretConfig, bool, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, capSecretPrefix) {
		return capSecretConfig{}, false, nil
	}
	rest := strings.TrimPrefix(raw, capSecretPrefix)
	cfg := capSecretConfig{}
	if idx := strings.LastIndex(rest, "|"); idx >= 0 {
		cfg.BaseURL = strings.TrimRight(strings.TrimSpace(rest[:idx]), "/")
		rest = rest[idx+1:]
	}
	parts := strings.SplitN(rest, ":", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return capSecretConfig{}, true, fmt.Errorf("invalid cap secret: expected cap:<site_key>:<secret>")
	}
	cfg.SiteKey = strings.TrimSpace(parts[0])
	cfg.Secret = strings.TrimSpace(parts[1])
	if cfg.BaseURL != "" {
		if u, err := url.Parse(cfg.BaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return capSecretConfig{}, true, fmt.Errorf("invalid cap base url: %q", cfg.BaseURL)
		}
	}
	return cfg, true, nil
}

// resolveCapBaseURL 返回 Cap 服务基址（无尾部斜杠）。
func resolveCapBaseURL(explicit string, getenv func(string) string) (string, error) {
	if b := strings.TrimRight(strings.TrimSpace(explicit), "/"); b != "" {
		return b, nil
	}
	if b := strings.TrimRight(strings.TrimSpace(getenv("CAP_BASE_URL")), "/"); b != "" {
		return b, nil
	}
	if f := strings.TrimRight(strings.TrimSpace(getenv("SERVER_FRONTEND_URL")), "/"); f != "" {
		return f + "/cap", nil
	}
	return "", fmt.Errorf("cap base url not configured: set CAP_BASE_URL or SERVER_FRONTEND_URL, or use cap:<base>|<key>:<secret>")
}

type capSiteverifyResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
}

func (v *turnstileVerifier) verifyCap(ctx context.Context, cfg capSecretConfig, token string) (*service.TurnstileVerifyResponse, error) {
	explicit := cfg.BaseURL
	if explicit == "" {
		explicit = v.capBaseURL
	}
	base, err := resolveCapBaseURL(explicit, os.Getenv)
	if err != nil {
		return nil, err
	}
	endpoint := base + "/" + url.PathEscape(cfg.SiteKey) + "/siteverify"
	payload, err := json.Marshal(map[string]string{"secret": cfg.Secret, "response": token})
	if err != nil {
		return nil, fmt.Errorf("encode cap request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create cap request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := v.capHTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send cap request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, fmt.Errorf("read cap response: %w", err)
	}
	var capResp capSiteverifyResponse
	if err := json.Unmarshal(body, &capResp); err != nil {
		return nil, fmt.Errorf("decode cap response (status %d): %w", resp.StatusCode, err)
	}
	result := &service.TurnstileVerifyResponse{Success: capResp.Success && resp.StatusCode < 400}
	if !result.Success {
		code := strings.TrimSpace(capResp.Error)
		if code == "" {
			code = fmt.Sprintf("cap-http-%d", resp.StatusCode)
		}
		result.ErrorCodes = []string{code}
	}
	return result, nil
}
