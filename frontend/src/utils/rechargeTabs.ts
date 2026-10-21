import type { RechargeTab } from '@/types/payment'
import { normalizeVisibleMethod } from '@/components/payment/paymentFlow'

/** Mirrors backend limits in service/payment_recharge_tabs.go. */
export const MAX_RECHARGE_TABS = 10
export const MAX_RECHARGE_TAB_NAME = 32
const TAB_ID_PATTERN = /^[a-z0-9_-]{1,32}$/
const METHOD_PATTERN = /^[a-z0-9_.-]{1,64}$/

/** Key of the built-in default tab (single 「充值」 tab with every method). */
export const DEFAULT_RECHARGE_TAB_KEY = 'recharge'

/** True when a method should show the Binance icon (type or EasyPay upstreamType mentions binance). */
export function isBinanceMethod(type?: string, upstreamType?: string): boolean {
  return [type, upstreamType].some((value) => (value || '').toLowerCase().includes('binance'))
}

/**
 * Lenient read: keeps enabled tabs with a valid id, a name and at least one
 * method, sorted by sort_order (stable). Empty result = default single tab.
 */
export function normalizeRechargeTabs(raw: unknown): RechargeTab[] {
  if (!Array.isArray(raw)) return []
  const seen = new Set<string>()
  const out: (RechargeTab & { _i: number })[] = []
  raw.forEach((item, index) => {
    if (!item || typeof item !== 'object') return
    const tab = item as Partial<RechargeTab>
    const id = String(tab.id ?? '').trim().toLowerCase()
    const name = String(tab.name ?? '').trim()
    if (!TAB_ID_PATTERN.test(id) || !name || seen.has(id) || tab.enabled === false) return
    const methods = Array.isArray(tab.methods)
      ? [...new Set(tab.methods.map((m) => String(m).trim().toLowerCase()).filter((m) => METHOD_PATTERN.test(m)))]
      : []
    if (!methods.length) return
    seen.add(id)
    out.push({ id, name, sort_order: Number(tab.sort_order) || 0, enabled: true, methods, _i: index })
  })
  out.sort((a, b) => a.sort_order - b.sort_order || a._i - b._i)
  return out.map(({ _i, ...tab }) => tab)
}

/** Whether a visible method key belongs to the tab (tab may bind raw keys such as alipay_direct). */
export function tabIncludesMethod(tab: RechargeTab, visibleKey: string): boolean {
  return tab.methods.some((method) => method === visibleKey || normalizeVisibleMethod(method) === visibleKey)
}

/** Filters visible method keys down to those bound to the tab, keeping input order. */
export function filterMethodsForTab(methodKeys: string[], tab: RechargeTab | null | undefined): string[] {
  if (!tab) return methodKeys
  return methodKeys.filter((key) => tabIncludesMethod(tab, key))
}

/** Tab key used by the payment page switcher for a custom tab. */
export function rechargeTabKey(tab: RechargeTab): string {
  return `recharge:${tab.id}`
}

/** Write-path validation for the admin editor; returns an i18n key suffix or '' when valid. */
export function validateRechargeTabsDraft(tabs: RechargeTab[]): string {
  if (!tabs.length) return ''
  if (tabs.length > MAX_RECHARGE_TABS) return 'tooMany'
  const ids = new Set<string>()
  let enabled = 0
  for (const tab of tabs) {
    const name = tab.name.trim()
    if (!name || [...name].length > MAX_RECHARGE_TAB_NAME) return 'nameInvalid'
    if (!TAB_ID_PATTERN.test(tab.id) || ids.has(tab.id)) return 'idInvalid'
    ids.add(tab.id)
    if (tab.enabled) {
      if (!tab.methods.length) return 'methodsRequired'
      enabled++
    }
  }
  return enabled ? '' : 'noneEnabled'
}

/** Generates a stable unused tab id. */
export function nextRechargeTabId(tabs: RechargeTab[]): string {
  const ids = new Set(tabs.map((tab) => tab.id))
  for (let i = tabs.length + 1; ; i++) {
    const id = `tab${i}`
    if (!ids.has(id)) return id
  }
}

/** Admin read path: keeps disabled tabs too (the editor shows every configured tab), sorted by sort_order. */
export function normalizeRechargeTabsForAdmin(raw: unknown): RechargeTab[] {
  if (!Array.isArray(raw)) return []
  return raw
    .filter((item): item is Partial<RechargeTab> => !!item && typeof item === 'object')
    .map((tab, index) => ({
      id: String(tab.id ?? '').trim().toLowerCase(),
      name: String(tab.name ?? ''),
      sort_order: Number(tab.sort_order) || 0,
      enabled: tab.enabled !== false,
      methods: Array.isArray(tab.methods) ? tab.methods.map((m) => String(m)) : [],
      _i: index,
    }))
    .sort((a, b) => a.sort_order - b.sort_order || a._i - b._i)
    .map(({ _i, ...tab }) => tab)
}
