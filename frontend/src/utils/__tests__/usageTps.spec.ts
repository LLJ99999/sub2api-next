import { describe, expect, it } from 'vitest'

import { USAGE_TPS_MIN_OUTPUT_TOKENS, formatUsageOutputTps, usageOutputTps } from '../usageTps'

describe('usageTps', () => {
  it('divides output tokens by the generation window after the first token', () => {
    const row = { output_tokens: 872, duration_ms: 31_260, first_token_ms: 2_910 }
    expect(usageOutputTps(row)).toBeCloseTo(872 / 28.35, 6)
    expect(formatUsageOutputTps(row)).toBe('30.8 t/s')
  })

  it.each([null, undefined])('uses the whole duration when first_token_ms is %s', (firstTokenMs) => {
    const row = { output_tokens: 500, duration_ms: 10_000, first_token_ms: firstTokenMs }
    expect(usageOutputTps(row)).toBe(50)
    expect(formatUsageOutputTps(row)).toBe('50.0 t/s')
  })

  it('counts exactly the minimum output tokens and hides anything below', () => {
    expect(usageOutputTps({ output_tokens: USAGE_TPS_MIN_OUTPUT_TOKENS, duration_ms: 2_000, first_token_ms: 1_000 })).toBe(20)
    expect(usageOutputTps({ output_tokens: USAGE_TPS_MIN_OUTPUT_TOKENS - 1, duration_ms: 2_000, first_token_ms: 1_000 })).toBeNull()
  })

  it.each([
    // Generation window <= 0.
    { output_tokens: 500, duration_ms: 10_000, first_token_ms: 10_000 },
    { output_tokens: 500, duration_ms: 10_000, first_token_ms: 10_001 },
    // Synthetic records reproduce the reported 109500 and 73000 t/s spikes
    // from buffered output flushed 10ms and 1ms after the first token.
    { output_tokens: 1_095, duration_ms: 9_250, first_token_ms: 9_240 },
    { output_tokens: 73, duration_ms: 6_761, first_token_ms: 6_760 },
  ])('hides TPS when the generation window is too short: %o', (row) => {
    expect(usageOutputTps(row)).toBeNull()
    expect(formatUsageOutputTps(row)).toBeNull()
  })

  it('rounds to an integer from 100 t/s upwards', () => {
    expect(formatUsageOutputTps({ output_tokens: 2_700, duration_ms: 10_000, first_token_ms: 1_000 })).toBe('300 t/s')
    expect(formatUsageOutputTps({ output_tokens: 999, duration_ms: 10_000, first_token_ms: 0 })).toBe('99.9 t/s')
  })

  it('returns null when there is nothing meaningful to measure', () => {
    expect(usageOutputTps(null)).toBeNull()
    expect(usageOutputTps(undefined)).toBeNull()
    expect(usageOutputTps({ output_tokens: 0, duration_ms: 5_000, first_token_ms: 1_000 })).toBeNull()
    expect(usageOutputTps({ output_tokens: 100, duration_ms: null, first_token_ms: null })).toBeNull()
    expect(usageOutputTps({ output_tokens: 100, duration_ms: 0, first_token_ms: null })).toBeNull()
    expect(formatUsageOutputTps({ output_tokens: 0, duration_ms: 5_000 })).toBeNull()
  })

  it('does not treat a single placeholder token from an interrupted stream as throughput', () => {
    const interrupted = { output_tokens: 1, duration_ms: 21_135, first_token_ms: 973 }
    expect(usageOutputTps(interrupted)).toBeNull()
    expect(formatUsageOutputTps(interrupted)).toBeNull()
  })

  it.each([-1, NaN, Infinity, -Infinity])('rejects invalid output, duration or first token %s', (invalid) => {
    expect(usageOutputTps({ output_tokens: invalid, duration_ms: 10_000 })).toBeNull()
    expect(formatUsageOutputTps({ output_tokens: 500, duration_ms: invalid })).toBeNull()
    expect(usageOutputTps({ output_tokens: 500, duration_ms: 10_000, first_token_ms: invalid })).toBeNull()
  })

  it('skips image and video requests', () => {
    expect(usageOutputTps({ output_tokens: 4_160, duration_ms: 40_000, image_count: 1 })).toBeNull()
    expect(usageOutputTps({ output_tokens: 4_160, duration_ms: 40_000, image_output_tokens: 4_160 })).toBeNull()
    expect(usageOutputTps({ output_tokens: 100, duration_ms: 40_000, billing_mode: 'video' })).toBeNull()
  })
})

describe('usageBadgeTps', () => {
  it('uses generation-phase TPS when computable', async () => {
    const { usageBadgeTps, formatUsageBadgeTps } = await import('../usageTps')
    const row = { output_tokens: 600, duration_ms: 12_000, first_token_ms: 2_000 }
    expect(usageBadgeTps(row)).toBeCloseTo(60)
    expect(formatUsageBadgeTps(row)).toBe('60.0 t/s')
  })

  it('falls back to output / total duration for short outputs', async () => {
    const { usageBadgeTps, formatUsageBadgeTps } = await import('../usageTps')
    const row = { output_tokens: 5, duration_ms: 2_381, first_token_ms: 2_300 }
    expect(usageBadgeTps(row)).toBeCloseTo(5 / 2.381)
    expect(formatUsageBadgeTps(row)).toBe('2.1 t/s')
    expect(formatUsageBadgeTps({ output_tokens: 3000, duration_ms: 10, first_token_ms: 9 })).toBe('300000 t/s')
  })

  it('returns null without output, duration, or for image/video', async () => {
    const { usageBadgeTps } = await import('../usageTps')
    expect(usageBadgeTps(null)).toBeNull()
    expect(usageBadgeTps({ output_tokens: 0, duration_ms: 1000 })).toBeNull()
    expect(usageBadgeTps({ output_tokens: 5, duration_ms: 0 })).toBeNull()
    expect(usageBadgeTps({ output_tokens: 5, duration_ms: null as any })).toBeNull()
    expect(usageBadgeTps({ output_tokens: 50, duration_ms: 1000, image_count: 1 })).toBeNull()
    expect(usageBadgeTps({ output_tokens: 50, duration_ms: 1000, image_output_tokens: 10 })).toBeNull()
    expect(usageBadgeTps({ output_tokens: 50, duration_ms: 1000, billing_mode: 'video' })).toBeNull()
  })
})
