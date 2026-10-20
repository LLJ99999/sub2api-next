/**
 * Self-hosted Cap (https://capjs.js.org) support for the Turnstile slot.
 *
 * Admin setting turnstile_site_key:
 *   cap:<site_key>              Cap served same-origin under /cap (reverse proxy)
 *   cap:<base_url>|<site_key>   Cap served at <base_url> (e.g. https://cap.example.com)
 *
 * Widget endpoint: <base>/<site_key>/   (Cap Standalone)
 * Widget script:   <base>/assets/widget.js (Cap Standalone asset server, ENABLE_ASSETS_SERVER=true)
 * WASM solvers:    <base>/assets/cap_wasm_bg.wasm, <base>/assets/hashwx.wasm
 */

export const CAP_SITE_KEY_PREFIX = 'cap:'
export const CAP_DEFAULT_BASE = '/cap'

export interface CapSiteConfig {
  base: string
  siteKey: string
  apiEndpoint: string
  scriptSrc: string
  wasmUrl: string
  hashwxUrl: string
}

export function isCapSiteKey(siteKey: string | null | undefined): boolean {
  return typeof siteKey === 'string' && siteKey.trim().startsWith(CAP_SITE_KEY_PREFIX)
}

/** Parse a `cap:` site key. Returns null for regular Turnstile keys or malformed values. */
export function parseCapSiteKey(siteKey: string | null | undefined): CapSiteConfig | null {
  if (!isCapSiteKey(siteKey)) return null
  const rest = (siteKey as string).trim().slice(CAP_SITE_KEY_PREFIX.length)
  let base = CAP_DEFAULT_BASE
  let key = rest
  const sep = rest.lastIndexOf('|')
  if (sep >= 0) {
    base = rest.slice(0, sep).trim() || CAP_DEFAULT_BASE
    key = rest.slice(sep + 1)
  }
  key = key.trim()
  base = base.replace(/\/+$/, '')
  if (!key || /[/?#\s]/.test(key)) return null
  if (!(base.startsWith('/') || /^https?:\/\//i.test(base))) return null
  return {
    base,
    siteKey: key,
    apiEndpoint: `${base}/${encodeURIComponent(key)}/`,
    scriptSrc: `${base}/assets/widget.js`,
    wasmUrl: `${base}/assets/cap_wasm_bg.wasm`,
    hashwxUrl: `${base}/assets/hashwx.wasm`,
  }
}

/** Chinese labels for the Cap widget (data-cap-i18n-* attributes). */
export const CAP_I18N_ZH: Record<string, string> = {
  'data-cap-i18n-initial-state': '点击进行人机验证',
  'data-cap-i18n-verifying-label': '验证中…',
  'data-cap-i18n-solved-label': '验证通过',
  'data-cap-i18n-error-label': '验证出错，请重试',
  'data-cap-i18n-troubleshooting-label': '故障排查',
  'data-cap-i18n-wasm-disabled': '启用 WASM 可显著加快验证速度',
  'data-cap-i18n-verify-aria-label': '点击验证您是真人',
  'data-cap-i18n-verifying-aria-label': '正在验证，请稍候',
  'data-cap-i18n-verified-aria-label': '已通过验证',
  'data-cap-i18n-required-label': '请先完成人机验证',
  'data-cap-i18n-error-aria-label': '发生错误，请重试',
}

/** Page CSP nonce (set on the server-rendered script tags), if any. */
export function readPageCspNonce(doc: Document = document): string {
  const nonce = doc.head?.querySelector<HTMLScriptElement>('script[nonce]')?.nonce ?? ''
  return /^[A-Za-z0-9+/_=-]+$/.test(nonce) ? nonce : ''
}
