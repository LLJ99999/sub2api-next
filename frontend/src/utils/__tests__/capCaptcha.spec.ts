import { describe, expect, it } from 'vitest'
import { isCapSiteKey, parseCapSiteKey, readPageCspNonce } from '../capCaptcha'

describe('parseCapSiteKey', () => {
  it('ignores regular Turnstile keys', () => {
    expect(isCapSiteKey('0x4AAAAAAA')).toBe(false)
    expect(parseCapSiteKey('0x4AAAAAAA')).toBeNull()
    expect(parseCapSiteKey('')).toBeNull()
    expect(parseCapSiteKey(undefined)).toBeNull()
  })

  it('defaults to the same-origin /cap base', () => {
    expect(parseCapSiteKey('cap:abc')).toEqual({
      base: '/cap',
      siteKey: 'abc',
      apiEndpoint: '/cap/abc/',
      scriptSrc: '/cap/assets/widget.js',
      wasmUrl: '/cap/assets/cap_wasm_bg.wasm',
      hashwxUrl: '/cap/assets/hashwx.wasm',
    })
  })

  it('accepts an explicit base', () => {
    const cfg = parseCapSiteKey(' cap:https://cap.example.com/|  k1 ')
    expect(cfg?.apiEndpoint).toBe('https://cap.example.com/k1/')
    expect(cfg?.scriptSrc).toBe('https://cap.example.com/assets/widget.js')
    expect(parseCapSiteKey('cap:/captcha/|k2')?.apiEndpoint).toBe('/captcha/k2/')
  })

  it('rejects malformed keys', () => {
    expect(parseCapSiteKey('cap:')).toBeNull()
    expect(parseCapSiteKey('cap:a/b')).toBeNull()
    expect(parseCapSiteKey('cap:javascript:alert(1)|k')).toBeNull()
  })
})

describe('readPageCspNonce', () => {
  it('returns empty without a nonce script', () => {
    expect(readPageCspNonce(document)).toBe('')
  })
})
