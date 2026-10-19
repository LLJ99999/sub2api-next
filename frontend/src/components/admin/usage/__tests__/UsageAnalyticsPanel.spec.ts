import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const { getAnalytics } = vi.hoisted(() => ({ getAnalytics: vi.fn() }))

vi.mock('@/api/admin/usage', () => ({ adminUsageAPI: { getAnalytics } }))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => (params ? `${key}:${JSON.stringify(params)}` : key),
    }),
  }
})

import UsageAnalyticsPanel from '../UsageAnalyticsPanel.vue'

const baseRow = {
  platform: '',
  error_count: 0,
  input_tokens: 0,
  output_tokens: 0,
  cache_creation_tokens: 0,
  cache_read_tokens: 0,
  total_tokens: 0,
  cache_hit_rate: null,
  first_token_p50_ms: null,
  first_token_p95_ms: null,
  output_tps_p50: null,
  output_tps_p95: null,
  margin_pct: null,
}

const response = {
  dimension: 'account',
  start_time: '',
  end_time: '',
  truncated: false,
  error_count_available: true,
  rows: [
    {
      ...baseRow, key: '7', id: 7, name: 'acc-a', requests: 20, error_count: 2, total_tokens: 1_900,
      cache_hit_rate: 0.5625, first_token_p50_ms: 700, first_token_p95_ms: 2_000, output_tps_p50: 45, output_tps_p95: 120.4,
      revenue: 10, upstream_cost: 6, gross_margin: 4, margin_pct: 0.4,
    },
    { ...baseRow, key: '8', id: 8, name: '', requests: 10, revenue: 2, upstream_cost: 3, gross_margin: -1, margin_pct: -0.5 },
  ],
  totals: { ...baseRow, key: '', name: '', requests: 30, error_count: 5, revenue: 12, upstream_cost: 9, gross_margin: 3, margin_pct: 0.25 },
}

const mountPanel = (props: Record<string, unknown> = {}) => mount(UsageAnalyticsPanel, {
  props: { startDate: '2026-10-01', endDate: '2026-10-07', showUpstreamCost: true, ...props },
})

describe('UsageAnalyticsPanel', () => {
  beforeEach(() => {
    getAnalytics.mockReset().mockResolvedValue(response)
  })

  it('loads account analytics with filters and renders rows plus totals', async () => {
    const wrapper = mountPanel({ groupId: 3, model: 'gpt-5' })
    await flushPromises()

    expect(getAnalytics).toHaveBeenCalledWith(
      { dimension: 'account', start_date: '2026-10-01', end_date: '2026-10-07', group_id: 3, account_id: undefined, model: 'gpt-5' },
      expect.objectContaining({ signal: expect.anything() }),
    )
    const rows = wrapper.findAll('[data-testid="usage-analytics-row"]')
    expect(rows).toHaveLength(2)
    // Default sort: revenue desc.
    expect(rows[0].text()).toContain('acc-a')
    expect(rows[0].text()).toContain('56.3%')
    expect(rows[0].text()).toContain('700ms')
    expect(rows[0].text()).toContain('2.00s')
    expect(rows[0].text()).toContain('45.0 t/s')
    expect(rows[0].text()).toContain('120 t/s')
    expect(rows[0].text()).toContain('$6.0000')
    expect(rows[0].text()).toContain('40.0%')
    expect(rows[1].text()).toContain('#8')
    expect(rows[1].text()).toContain('-$1.0000')
    const total = wrapper.get('[data-testid="usage-analytics-total"]')
    expect(total.text()).toContain('usage.analytics.total')
    expect(total.text()).toContain('25.0%')
  })

  it('hides upstream cost, margin and margin % when the toggle is off', async () => {
    const wrapper = mountPanel({ showUpstreamCost: false })
    await flushPromises()
    expect(wrapper.find('[data-testid="usage-analytics-col-upstream_cost"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="usage-analytics-col-gross_margin"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="usage-analytics-col-margin_pct"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('$6.0000')
    expect(wrapper.text()).not.toContain('-$1.0000')
    expect(wrapper.find('[data-testid="usage-analytics-cost-hidden"]').exists()).toBe(true)
  })

  it('resets a cost-based sort when upstream cost becomes hidden', async () => {
    const wrapper = mountPanel()
    await flushPromises()
    // Click twice: upstream cost ascending puts the cheaper account (#8) first.
    await wrapper.get('[data-testid="usage-analytics-col-upstream_cost"]').trigger('click')
    await wrapper.get('[data-testid="usage-analytics-col-upstream_cost"]').trigger('click')
    expect(wrapper.findAll('[data-testid="usage-analytics-row"]')[0].text()).toContain('#8')
    await wrapper.setProps({ showUpstreamCost: false })
    expect(wrapper.findAll('[data-testid="usage-analytics-row"]')[0].text()).toContain('acc-a')
  })

  it('switches dimension and reloads when the range changes', async () => {
    const wrapper = mountPanel()
    await flushPromises()
    getAnalytics.mockResolvedValue({ ...response, dimension: 'group', rows: [{ ...response.rows[1], key: '0', name: '' }] })
    await wrapper.get('[data-testid="usage-analytics-dimension-group"]').trigger('click')
    await flushPromises()
    expect(getAnalytics).toHaveBeenLastCalledWith(expect.objectContaining({ dimension: 'group' }), expect.anything())
    expect(wrapper.text()).toContain('usage.analytics.noGroup')

    await wrapper.setProps({ endDate: '2026-10-08' })
    await flushPromises()
    expect(getAnalytics).toHaveBeenLastCalledWith(expect.objectContaining({ dimension: 'group', end_date: '2026-10-08' }), expect.anything())
  })

  it('explains a range that is too large', async () => {
    getAnalytics.mockRejectedValue({ status: 400, reason: 'USAGE_ANALYTICS_RANGE_TOO_LARGE' })
    const wrapper = mountPanel({ startDate: '2026-01-01' })
    await flushPromises()
    expect(wrapper.get('[data-testid="usage-analytics-error"]').text()).toBe('usage.analytics.rangeTooLarge')
    expect(wrapper.find('[data-testid="usage-analytics-empty"]').exists()).toBe(true)
  })
})
