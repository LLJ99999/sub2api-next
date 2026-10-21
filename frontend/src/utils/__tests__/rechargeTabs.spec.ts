import { describe, expect, it } from 'vitest'
import {
  filterMethodsForTab,
  isBinanceMethod,
  nextRechargeTabId,
  normalizeRechargeTabs,
  normalizeRechargeTabsForAdmin,
  validateRechargeTabsDraft,
} from '@/utils/rechargeTabs'

describe('rechargeTabs utils', () => {
  it('normalizes user tabs: drops disabled/invalid, dedups, sorts', () => {
    expect(normalizeRechargeTabs(undefined)).toEqual([])
    expect(normalizeRechargeTabs([
      { id: 'b', name: 'B', sort_order: 2, enabled: true, methods: ['binance', 'binance'] },
      { id: 'off', name: 'Off', sort_order: 0, enabled: false, methods: ['alipay'] },
      { id: 'bad id', name: 'x', sort_order: 0, enabled: true, methods: ['alipay'] },
      { id: 'empty', name: 'E', sort_order: 0, enabled: true, methods: [] },
      { id: 'a', name: 'A', sort_order: 1, enabled: true, methods: ['alipay'] },
    ])).toEqual([
      { id: 'a', name: 'A', sort_order: 1, enabled: true, methods: ['alipay'] },
      { id: 'b', name: 'B', sort_order: 2, enabled: true, methods: ['binance'] },
    ])
  })

  it('filters visible methods by tab, matching raw provider keys like alipay_direct', () => {
    const tab = { id: 't', name: 'T', sort_order: 1, enabled: true, methods: ['alipay_direct', 'binance'] }
    expect(filterMethodsForTab(['alipay', 'wxpay', 'binance'], tab)).toEqual(['alipay', 'binance'])
    expect(filterMethodsForTab(['alipay', 'wxpay'], null)).toEqual(['alipay', 'wxpay'])
  })

  it('detects binance methods', () => {
    expect(isBinanceMethod('binance')).toBe(true)
    expect(isBinanceMethod('crypto', 'BinancePay')).toBe(true)
    expect(isBinanceMethod('alipay')).toBe(false)
  })

  it('validates admin drafts', () => {
    expect(validateRechargeTabsDraft([])).toBe('')
    expect(validateRechargeTabsDraft([{ id: 'a', name: ' ', sort_order: 1, enabled: true, methods: ['alipay'] }])).toBe('nameInvalid')
    expect(validateRechargeTabsDraft([{ id: 'a', name: 'A', sort_order: 1, enabled: true, methods: [] }])).toBe('methodsRequired')
    expect(validateRechargeTabsDraft([{ id: 'a', name: 'A', sort_order: 1, enabled: false, methods: [] }])).toBe('noneEnabled')
    expect(validateRechargeTabsDraft([
      { id: 'a', name: 'A', sort_order: 1, enabled: true, methods: ['alipay'] },
      { id: 'a', name: 'B', sort_order: 2, enabled: true, methods: ['wxpay'] },
    ])).toBe('idInvalid')
    expect(nextRechargeTabId([{ id: 'tab2', name: 'x', sort_order: 1, enabled: true, methods: [] }])).toBe('tab3')
  })

  it('keeps disabled tabs for the admin editor', () => {
    expect(normalizeRechargeTabsForAdmin([
      { id: 'b', name: 'B', sort_order: 2, enabled: false, methods: [] },
      { id: 'a', name: 'A', sort_order: 1, enabled: true, methods: ['alipay'] },
    ]).map((t) => t.id)).toEqual(['a', 'b'])
  })
})
