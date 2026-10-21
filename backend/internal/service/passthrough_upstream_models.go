package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Passthrough OpenAI API-key accounts forward any model name, so their model_mapping
// says nothing about what the upstream serves. /v1/models asks the upstream's own
// /v1/models instead (#6647): results are cached per account, a failure is cached
// briefly and the caller falls back to the default catalog.
const (
	passthroughUpstreamModelsTTL      = 10 * time.Minute
	passthroughUpstreamModelsFailTTL  = time.Minute
	passthroughUpstreamModelsTimeout  = 4 * time.Second
	passthroughUpstreamModelsMaxItems = 2000
)

type passthroughUpstreamModelsFetcher func(ctx context.Context, account *Account) ([]string, error)

var passthroughModelsFetcher atomic.Pointer[passthroughUpstreamModelsFetcher]

// SetPassthroughUpstreamModelsFetcher installs the upstream model-list call
// (AccountTestService.SyncUpstreamModelCatalog in production). nil disables it.
func SetPassthroughUpstreamModelsFetcher(fetch passthroughUpstreamModelsFetcher) {
	if fetch == nil {
		passthroughModelsFetcher.Store(nil)
		return
	}
	passthroughModelsFetcher.Store(&fetch)
}

type passthroughModelsEntry struct {
	models  []string
	ok      bool
	expires time.Time
	key     string
}

type passthroughModelsCache struct {
	mu      sync.Mutex
	entries map[int64]passthroughModelsEntry
	now     func() time.Time
}

var passthroughUpstreamModels = &passthroughModelsCache{entries: map[int64]passthroughModelsEntry{}, now: time.Now}

func passthroughModelsCacheKey(account *Account) string {
	return strings.TrimRight(strings.TrimSpace(account.GetCredential("base_url")), "/") + "|" + account.GetCredential("api_key")
}

func usesUpstreamModelList(account *Account) bool {
	return account != nil && account.Platform == PlatformOpenAI && account.Type == AccountTypeAPIKey && account.IsOpenAIPassthroughEnabled()
}

// get returns the account's upstream models and whether they are usable.
func (c *passthroughModelsCache) get(ctx context.Context, account *Account) ([]string, bool) {
	if !usesUpstreamModelList(account) {
		return nil, false
	}
	key := passthroughModelsCacheKey(account)
	now := c.now()
	c.mu.Lock()
	entry, found := c.entries[account.ID]
	c.mu.Unlock()
	if found && entry.key == key && now.Before(entry.expires) {
		return entry.models, entry.ok
	}
	fetchPtr := passthroughModelsFetcher.Load()
	if fetchPtr == nil {
		return nil, false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), passthroughUpstreamModelsTimeout)
	defer cancel()
	list, err := safeFetchPassthroughModels(fetchCtx, *fetchPtr, account)
	models := make([]string, 0, len(list))
	seen := make(map[string]struct{}, len(list))
	for _, m := range list {
		if m = strings.TrimSpace(m); m == "" {
			continue
		}
		if _, dup := seen[m]; dup {
			continue
		}
		seen[m] = struct{}{}
		models = append(models, m)
		if len(models) >= passthroughUpstreamModelsMaxItems {
			break
		}
	}
	entry = passthroughModelsEntry{models: models, ok: err == nil && len(models) > 0, key: key}
	if entry.ok {
		entry.expires = now.Add(passthroughUpstreamModelsTTL)
	} else {
		entry.models = nil
		entry.expires = now.Add(passthroughUpstreamModelsFailTTL)
	}
	c.mu.Lock()
	c.entries[account.ID] = entry
	c.mu.Unlock()
	return entry.models, entry.ok
}

func safeFetchPassthroughModels(ctx context.Context, fetch passthroughUpstreamModelsFetcher, account *Account) (list []string, err error) {
	defer func() {
		if r := recover(); r != nil {
			list, err = nil, errPassthroughModelsPanic
		}
	}()
	return fetch(ctx, account)
}

var errPassthroughModelsPanic = errors.New("passthrough upstream model list panicked")
