export type UsageCacheRateAudience = 'admin' | 'user'

export interface UsageCacheRateSettings {
  usage_show_cache_rate?: boolean
  usage_show_cache_rate_to_users?: boolean
}

/** Feature defaults on. End users see it only when that switch is explicitly on. */
export function showUsageCacheRate(
  settings: UsageCacheRateSettings | null | undefined,
  audience: UsageCacheRateAudience,
): boolean {
  if (settings?.usage_show_cache_rate === false) return false
  if (audience === 'admin') return true
  return settings?.usage_show_cache_rate_to_users === true
}

export interface UsageCacheRateRow {
  input_tokens?: number | null
  cache_read_tokens?: number | null
  cache_creation_tokens?: number | null
}

export interface UsageCacheRateParts {
  input: number
  read: number
  write: number
  /** 0–1. Cache read divided by input + cache read + cache write. */
  rate: number
}

function bucket(value: number | null | undefined): number | null {
  if (value == null) return 0
  if (typeof value !== 'number' || !Number.isFinite(value) || value < 0) return null
  return value
}

/**
 * Prompt cache rate for one usage record.
 *
 * Stored usage logs keep three exclusive prompt buckets: input_tokens is the
 * uncached input, cache_read_tokens is the cache hit, and cache_creation_tokens
 * is the cache write. The rate is cache read ÷ (input + cache read + cache write),
 * the same definition used by channel monitor, the usage trend chart and the
 * backend (usagestats.CacheHitRate / usage analytics SQL).
 *
 * Protocol normalization happens when the log is written, not here: Anthropic
 * input_tokens already excludes cache tokens, while OpenAI prompt_tokens /
 * input_tokens include cached_tokens (and Gemini promptTokenCount includes
 * cachedContentTokenCount) — the gateway subtracts them before storing, so the
 * same formula is correct for every protocol.
 *
 * Returns null when the request has no prompt tokens, so image-only or empty
 * records show a placeholder instead of a misleading 0%.
 */
export function usageCacheRateParts(row: UsageCacheRateRow | null | undefined): UsageCacheRateParts | null {
  if (!row) return null
  const input = bucket(row.input_tokens)
  const read = bucket(row.cache_read_tokens)
  const write = bucket(row.cache_creation_tokens)
  if (input == null || read == null || write == null) return null
  const total = input + read + write
  if (total <= 0) return null
  const rate = read / total
  return Number.isFinite(rate) ? { input, read, write, rate } : null
}

/**
 * Aggregate cache rate over summed buckets (e.g. a trend bucket or an analytics
 * row). Null when there are no prompt tokens.
 */
export function cacheHitRateFromTokens(
  inputTokens: number | null | undefined,
  cacheReadTokens: number | null | undefined,
  cacheCreationTokens: number | null | undefined,
): number | null {
  return usageCacheRateParts({
    input_tokens: inputTokens,
    cache_read_tokens: cacheReadTokens,
    cache_creation_tokens: cacheCreationTokens,
  })?.rate ?? null
}

export function usageCacheRate(row: UsageCacheRateRow | null | undefined): number | null {
  return usageCacheRateParts(row)?.rate ?? null
}

/** "83.7%". Exact 0 and 100 stay compact. Null when the rate does not apply. */
export function formatUsageCacheRate(row: UsageCacheRateRow | null | undefined): string | null {
  const rate = usageCacheRate(row)
  if (rate == null) return null
  if (rate === 0) return '0%'
  const rounded = Math.round(rate * 1000) / 10
  if (rounded <= 0) return '<0.1%'
  if (rounded >= 100) return '100%'
  return `${rounded.toFixed(1)}%`
}
