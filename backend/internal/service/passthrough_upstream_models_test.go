package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

func TestPassthroughUpstreamModelsCache(t *testing.T) {
	prev := passthroughModelsFetcher.Load()
	t.Cleanup(func() { passthroughModelsFetcher.Store(prev) })
	now := time.Unix(1_800_000_000, 0)
	cache := &passthroughModelsCache{entries: map[int64]passthroughModelsEntry{}, now: func() time.Time { return now }}
	account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://up.example", "api_key": "k"}, Extra: map[string]any{"openai_passthrough": true}}

	calls := 0
	fail := false
	SetPassthroughUpstreamModelsFetcher(func(context.Context, *Account) ([]string, error) {
		calls++
		if fail {
			return nil, errors.New("down")
		}
		return []string{"m1", " m2 ", "m1", ""}, nil
	})
	got, ok := cache.get(context.Background(), account)
	require.True(t, ok)
	require.Equal(t, []string{"m1", "m2"}, got)
	_, _ = cache.get(context.Background(), account)
	require.Equal(t, 1, calls, "cached within TTL")

	now = now.Add(passthroughUpstreamModelsTTL + time.Second)
	fail = true
	got, ok = cache.get(context.Background(), account)
	require.False(t, ok)
	require.Nil(t, got)
	require.Equal(t, 2, calls)

	oauth := &Account{ID: 43, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"openai_passthrough": true}}
	_, ok = cache.get(context.Background(), oauth)
	require.False(t, ok)

	SetPassthroughUpstreamModelsFetcher(func(context.Context, *Account) ([]string, error) { panic("boom") })
	now = now.Add(time.Hour)
	_, ok = cache.get(context.Background(), account)
	require.False(t, ok)
}

func TestSupplementUnmappedOpenAIModelsExceptSkipsListedAccounts(t *testing.T) {
	accounts := []Account{{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{"openai_passthrough": true}}}
	got := supplementUnmappedOpenAIModelsExcept(accounts, nil, []string{"upstream-only"}, map[int64]struct{}{1: {}})
	require.Equal(t, []string{"upstream-only"}, got)
	got = supplementUnmappedOpenAIModelsExcept(accounts, nil, []string{"upstream-only"}, nil)
	require.Contains(t, got, openai.DefaultModelIDs()[0])
}
