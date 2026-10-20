package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const tempUnschedPrefix = "temp_unsched:account:"

const openAIAPIKeyHealthFailurePrefix = "openai_apikey_health:"

var openAIAPIKeyHealthFailureScript = redis.NewScript(`
	local key = KEYS[1]
	local sequence_key = key .. ':sequence'
	local now = redis.call('TIME')
	local now_ms = (tonumber(now[1]) * 1000) + math.floor(tonumber(now[2]) / 1000)
	local window_ms = tonumber(ARGV[1]) * 60 * 1000
	local threshold = tonumber(ARGV[2])
	local sequence = redis.call('INCR', sequence_key)

	redis.call('ZREMRANGEBYSCORE', key, '-inf', now_ms - window_ms)
	redis.call('ZADD', key, now_ms, tostring(now_ms) .. ':' .. tostring(sequence))
	local count = redis.call('ZCARD', key)
	local ttl = math.max(60, (tonumber(ARGV[1]) + 1) * 60)
	redis.call('EXPIRE', key, ttl)
	redis.call('EXPIRE', sequence_key, ttl)
	if count >= threshold then
		redis.call('DEL', key, sequence_key)
		return {count, 1}
	end
	return {count, 0}
`)

var tempUnschedSetScript = redis.NewScript(`
	local key = KEYS[1]
	local new_until = tonumber(ARGV[1])
	local new_value = ARGV[2]
	local new_ttl = tonumber(ARGV[3])

	local existing = redis.call('GET', key)
	if existing then
		local ok, existing_data = pcall(cjson.decode, existing)
		if ok and existing_data and existing_data.until_unix then
			local existing_until = tonumber(existing_data.until_unix)
			if existing_until and new_until <= existing_until then
				return 0
			end
		end
	end

	redis.call('SET', key, new_value, 'EX', new_ttl)
	return 1
`)

type tempUnschedCache struct {
	rdb *redis.Client
}

func NewTempUnschedCache(rdb *redis.Client) service.TempUnschedCache {
	return &tempUnschedCache{rdb: rdb}
}

// SetTempUnsched 设置临时不可调度状态（只延长不缩短）
func (c *tempUnschedCache) SetTempUnsched(ctx context.Context, accountID int64, state *service.TempUnschedState) error {
	key := fmt.Sprintf("%s%d", tempUnschedPrefix, accountID)

	stateJSON, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}

	ttl := time.Until(time.Unix(state.UntilUnix, 0))
	if ttl <= 0 {
		return nil // 已过期，不设置
	}

	ttlSeconds := int(ttl.Seconds())
	if ttlSeconds < 1 {
		ttlSeconds = 1
	}

	_, err = tempUnschedSetScript.Run(ctx, c.rdb, []string{key}, state.UntilUnix, string(stateJSON), ttlSeconds).Result()
	return err
}

// GetTempUnsched 获取临时不可调度状态
func (c *tempUnschedCache) GetTempUnsched(ctx context.Context, accountID int64) (*service.TempUnschedState, error) {
	key := fmt.Sprintf("%s%d", tempUnschedPrefix, accountID)

	val, err := c.rdb.Get(ctx, key).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var state service.TempUnschedState
	if err := json.Unmarshal([]byte(val), &state); err != nil {
		return nil, fmt.Errorf("unmarshal state: %w", err)
	}

	return &state, nil
}

// DeleteTempUnsched 删除临时不可调度状态
func (c *tempUnschedCache) DeleteTempUnsched(ctx context.Context, accountID int64) error {
	key := fmt.Sprintf("%s%d", tempUnschedPrefix, accountID)
	return c.rdb.Del(ctx, key).Err()
}

func (c *tempUnschedCache) openAIAPIKeyHealthKey(accountID int64) string {
	// The hash tag keeps the rolling window and sequence key in one Redis
	// Cluster slot even though the Lua script derives the latter dynamically.
	return fmt.Sprintf("%s{%d}:failures", openAIAPIKeyHealthFailurePrefix, accountID)
}

func (c *tempUnschedCache) RecordOpenAIAPIKeyHealthFailure(ctx context.Context, accountID int64, windowMinutes, threshold int) (int64, bool, error) {
	if windowMinutes < 1 {
		windowMinutes = 1
	}
	if threshold < 1 {
		threshold = 1
	}
	result, err := openAIAPIKeyHealthFailureScript.Run(ctx, c.rdb, []string{c.openAIAPIKeyHealthKey(accountID)}, windowMinutes, threshold).Slice()
	if err != nil {
		return 0, false, fmt.Errorf("record OpenAI API key health failure: %w", err)
	}
	if len(result) != 2 {
		return 0, false, fmt.Errorf("record OpenAI API key health failure: unexpected result length %d", len(result))
	}
	count, countOK := result[0].(int64)
	tripped, trippedOK := result[1].(int64)
	if !countOK || !trippedOK {
		return 0, false, fmt.Errorf("record OpenAI API key health failure: unexpected result types %T/%T", result[0], result[1])
	}
	return count, tripped == 1, nil
}

// ---------------------------------------------------------------------------
// Upstream relay circuit breaker (service.UpstreamRelayBreakerStore) and the
// all-cooled fallback probe lock (service.CooldownFallbackProbeLocker).
// Both are optional TempUnschedCache extensions discovered by type assertion.
// ---------------------------------------------------------------------------

var (
	_ service.UpstreamRelayBreakerStore   = (*tempUnschedCache)(nil)
	_ service.CooldownFallbackProbeLocker = (*tempUnschedCache)(nil)
)

const (
	upstreamRelayBreakerPrefix  = "upstream_relay:"
	cooldownFallbackProbePrefix = "cooldown_fallback_probe:"
)

var upstreamRelayRecordScript = redis.NewScript(`
	local now = redis.call('TIME')
	local now_ms = (tonumber(now[1]) * 1000) + math.floor(tonumber(now[2]) / 1000)
	local window_ms = tonumber(ARGV[2])
	local level = tonumber(redis.call('HGET', KEYS[3], 'level') or '0')
	local engaged = 0
	if level > 0 then
		engaged = 1
	end
	if ARGV[1] == '1' and engaged == 0 and redis.call('EXISTS', KEYS[1]) == 0 then
		return {0, 0, 0}
	end
	redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now_ms - window_ms)
	redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', now_ms - window_ms)
	local seq = redis.call('INCR', KEYS[4])
	local member = tostring(now_ms) .. ':' .. tostring(seq)
	local ttl = math.ceil(window_ms / 1000) + 60
	if ARGV[1] ~= '1' then
		redis.call('ZADD', KEYS[1], now_ms, member)
		redis.call('EXPIRE', KEYS[1], ttl)
	end
	redis.call('ZADD', KEYS[2], now_ms, member)
	redis.call('EXPIRE', KEYS[2], ttl)
	redis.call('EXPIRE', KEYS[4], ttl)
	return {redis.call('ZCARD', KEYS[1]), redis.call('ZCARD', KEYS[2]), engaged}
`)

var upstreamRelayTripScript = redis.NewScript(`
	local now = redis.call('TIME')
	local now_ms = (tonumber(now[1]) * 1000) + math.floor(tonumber(now[2]) / 1000)
	local level = tonumber(redis.call('HGET', KEYS[3], 'level') or '0')
	local last = tonumber(redis.call('HGET', KEYS[3], 'last_trip_ms') or '0')
	if level == 0 or (now_ms - last) >= tonumber(ARGV[2]) then
		level = level + 1
		redis.call('HSET', KEYS[3], 'level', level, 'last_trip_ms', now_ms)
	end
	redis.call('EXPIRE', KEYS[3], tonumber(ARGV[1]))
	redis.call('DEL', KEYS[1], KEYS[2])
	return level
`)

// upstreamRelayKeys returns fail/all/state/seq keys sharing one hash tag so the
// scripts stay within a single Redis Cluster slot.
func upstreamRelayKeys(accountID int64, scope string) []string {
	tag := fmt.Sprintf("%s{%d|%s}", upstreamRelayBreakerPrefix, accountID, scope)
	return []string{tag + ":fail", tag + ":all", tag + ":state", tag + ":seq"}
}

func (c *tempUnschedCache) RecordUpstreamRelayOutcome(ctx context.Context, accountID int64, scope string, success bool, window time.Duration) (service.UpstreamRelayOutcome, error) {
	windowMS := window.Milliseconds()
	if windowMS < 1000 {
		windowMS = 1000
	}
	flag := "0"
	if success {
		flag = "1"
	}
	res, err := upstreamRelayRecordScript.Run(ctx, c.rdb, upstreamRelayKeys(accountID, scope), flag, windowMS).Slice()
	if err != nil {
		return service.UpstreamRelayOutcome{}, fmt.Errorf("record upstream relay outcome: %w", err)
	}
	if len(res) != 3 {
		return service.UpstreamRelayOutcome{}, fmt.Errorf("record upstream relay outcome: unexpected result length %d", len(res))
	}
	failures, ok1 := res[0].(int64)
	total, ok2 := res[1].(int64)
	engaged, ok3 := res[2].(int64)
	if !ok1 || !ok2 || !ok3 {
		return service.UpstreamRelayOutcome{}, fmt.Errorf("record upstream relay outcome: unexpected result types")
	}
	return service.UpstreamRelayOutcome{Failures: failures, Total: total, Engaged: engaged == 1}, nil
}

func (c *tempUnschedCache) TripUpstreamRelayBreaker(ctx context.Context, accountID int64, scope string, levelTTL time.Duration) (int, error) {
	ttl := int64(levelTTL / time.Second)
	if ttl < 60 {
		ttl = 60
	}
	level, err := upstreamRelayTripScript.Run(ctx, c.rdb, upstreamRelayKeys(accountID, scope), ttl, int64(2000)).Int64()
	if err != nil {
		return 0, fmt.Errorf("trip upstream relay breaker: %w", err)
	}
	return int(level), nil
}

func (c *tempUnschedCache) CloseUpstreamRelayBreaker(ctx context.Context, accountID int64, scope string) error {
	keys := upstreamRelayKeys(accountID, scope)
	return c.rdb.Del(ctx, keys...).Err()
}

func (c *tempUnschedCache) TryAcquireCooldownFallbackProbe(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if ttl < time.Second {
		ttl = time.Second
	}
	return c.rdb.SetNX(ctx, cooldownFallbackProbePrefix+key, "1", ttl).Result()
}

var _ service.UpstreamRelayHalfOpenStore = (*tempUnschedCache)(nil)

const upstreamRelayHalfOpenPrefix = "upstream_relay_half_open:"

var upstreamRelayHalfOpenAcquireScript = redis.NewScript(`
	local count = redis.call('INCR', KEYS[1])
	if count == 1 then
		redis.call('PEXPIRE', KEYS[1], tonumber(ARGV[2]))
	end
	if count > tonumber(ARGV[1]) then
		redis.call('DECR', KEYS[1])
		return 0
	end
	return 1
`)

func upstreamRelayHalfOpenKey(accountID int64, scope string) string {
	return fmt.Sprintf("%s{%d|%s}", upstreamRelayHalfOpenPrefix, accountID, scope)
}

// TryAcquireUpstreamRelayHalfOpenTrial admits at most limit concurrent
// half-open trial requests per account/scope until ttl or a reset.
func (c *tempUnschedCache) TryAcquireUpstreamRelayHalfOpenTrial(ctx context.Context, accountID int64, scope string, limit int, ttl time.Duration) (bool, error) {
	if limit < 1 {
		limit = 1
	}
	ttlMS := ttl.Milliseconds()
	if ttlMS < 1000 {
		ttlMS = 1000
	}
	ok, err := upstreamRelayHalfOpenAcquireScript.Run(ctx, c.rdb, []string{upstreamRelayHalfOpenKey(accountID, scope)}, limit, ttlMS).Int64()
	if err != nil {
		return false, fmt.Errorf("acquire upstream relay half-open trial: %w", err)
	}
	return ok == 1, nil
}

// ResetUpstreamRelayHalfOpenTrials clears the trial counter.
func (c *tempUnschedCache) ResetUpstreamRelayHalfOpenTrials(ctx context.Context, accountID int64, scope string) error {
	return c.rdb.Del(ctx, upstreamRelayHalfOpenKey(accountID, scope)).Err()
}
