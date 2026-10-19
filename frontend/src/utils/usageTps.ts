import type { UsageLog } from '@/types'
import { BILLING_MODE_VIDEO } from './billingMode'

type UsageTpsRow = Partial<
  Pick<UsageLog, 'output_tokens' | 'duration_ms' | 'first_token_ms' | 'image_count' | 'image_output_tokens' | 'billing_mode'>
>

/** 输出 token 少于该值不计算 TPS（与后端 usagestats.OutputTPSMinOutputTokens 一致）。 */
export const USAGE_TPS_MIN_OUTPUT_TOKENS = 20
/** 生成窗口短于该值（毫秒）不计算 TPS（与后端 usagestats.OutputTPSMinGenerationMs 一致）。 */
export const USAGE_TPS_MIN_GENERATION_MS = 100

/**
 * Generation-phase output throughput (tokens/s):
 *
 *   output_tokens / ((duration_ms - first_token_ms) / 1000)
 *
 * first_token_ms missing (non-streaming) counts as 0, so the whole duration is
 * the generation window. Returns null when:
 * - the request is an image/video generation;
 * - output_tokens < USAGE_TPS_MIN_OUTPUT_TOKENS;
 * - duration is missing, or the generation window is shorter than
 *   USAGE_TPS_MIN_GENERATION_MS (covers <= 0). Upstreams that buffer the
 *   whole answer and flush it within a few ms after the first token would
 *   otherwise produce 70k–110k t/s spikes.
 *
 * Must match backend usagestats.OutputTPS and the SQL used for server-side
 * sorting and P50/P95 aggregates.
 */
export const usageOutputTps = (row: UsageTpsRow | null | undefined): number | null => {
  const outputTokens = row?.output_tokens ?? 0
  const durationMs = row?.duration_ms
  const firstTokenMs = row?.first_token_ms ?? 0
  if (durationMs == null || !Number.isFinite(outputTokens) || !Number.isFinite(durationMs) || !Number.isFinite(firstTokenMs)) {
    return null
  }
  if (outputTokens < USAGE_TPS_MIN_OUTPUT_TOKENS || firstTokenMs < 0) return null
  if ((row?.image_count ?? 0) > 0 || (row?.image_output_tokens ?? 0) > 0 || row?.billing_mode === BILLING_MODE_VIDEO) {
    return null
  }
  const generationMs = durationMs - firstTokenMs
  if (generationMs <= 0 || generationMs < USAGE_TPS_MIN_GENERATION_MS) return null
  const tps = outputTokens / (generationMs / 1000)
  return Number.isFinite(tps) ? tps : null
}

/** "30.8 t/s"；100 t/s 及以上取整。不可计算时返回 null，由调用方显示占位符。 */
export const formatUsageOutputTps = (row: UsageTpsRow | null | undefined): string | null => {
  const tps = usageOutputTps(row)
  if (tps == null) return null
  return `${tps >= 100 ? Math.round(tps) : tps.toFixed(1)} t/s`
}
