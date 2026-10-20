import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import TurnstileWidget from '../TurnstileWidget.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

const scriptSelector = 'script[src*="challenges.cloudflare.com/turnstile"]'
let wrapper: VueWrapper | undefined

function installSDK() {
  const render = vi.fn<NonNullable<typeof window.turnstile>['render']>(() => 'widget-1')
  window.turnstile = { render, reset: vi.fn(), remove: vi.fn() }
  return render
}

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  delete window.turnstile
  delete window.onTurnstileLoad
  document.querySelectorAll(scriptSelector).forEach((script) => script.remove())
  vi.restoreAllMocks()
})

describe('TurnstileWidget', () => {
  it('shows a loading placeholder until the SDK initializes the widget', async () => {
    wrapper = mount(TurnstileWidget, { props: { siteKey: 'site-key' } })

    expect(wrapper.get('[role="status"]').text()).toBe('auth.captchaLoading')
    expect(document.querySelector(scriptSelector)).not.toBeNull()
    const container = wrapper.get('.turnstile-container').element

    const render = installSDK()
    window.onTurnstileLoad?.()
    await flushPromises()

    expect(render).toHaveBeenCalledWith(container, expect.objectContaining({ sitekey: 'site-key' }))
    expect(wrapper.find('[role="status"]').exists()).toBe(false)
    expect(wrapper.get('.turnstile-container').element).toBe(container)
  })

  it('initializes an already loaded SDK and still forwards verification', async () => {
    const render = installSDK()
    wrapper = mount(TurnstileWidget, { props: { siteKey: 'site-key' } })
    await flushPromises()

    expect(render).toHaveBeenCalledOnce()
    expect(document.querySelector(scriptSelector)).toBeNull()
    expect(wrapper.find('[role="status"]').exists()).toBe(false)

    render.mock.calls[0]![1].callback('verified-token')
    expect(wrapper.emitted('verify')).toEqual([['verified-token']])
  })

  it('ends loading and reports a script load failure', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    wrapper = mount(TurnstileWidget, { props: { siteKey: 'site-key' } })

    document.querySelector(scriptSelector)!.dispatchEvent(new Event('error'))
    await flushPromises()

    expect(wrapper.find('[role="status"]').exists()).toBe(false)
    expect(wrapper.emitted('error')).toEqual([[]])
  })

  it('does not show a placeholder or load the SDK without a site key', async () => {
    wrapper = mount(TurnstileWidget, { props: { siteKey: '' } })
    await flushPromises()

    expect(wrapper.find('.turnstile-wrapper').exists()).toBe(false)
    expect(wrapper.find('[role="status"]').exists()).toBe(false)
    expect(document.querySelector(scriptSelector)).toBeNull()
  })
})

describe('TurnstileWidget with self-hosted Cap (cap: site key)', () => {
  const capScriptSelector = 'script[data-cap-widget-src]'

  afterEach(() => {
    document.querySelectorAll(capScriptSelector).forEach((script) => script.remove())
    delete window.CAP_CUSTOM_WASM_URL
    delete window.CAP_CUSTOM_HASHWX_URL
  })

  it('loads the Cap widget script from the same-origin /cap base and renders <cap-widget>', async () => {
    wrapper = mount(TurnstileWidget, { props: { siteKey: 'cap:abc123' } })
    await flushPromises()

    // Turnstile is never touched
    expect(document.querySelector(scriptSelector)).toBeNull()
    const script = document.querySelector<HTMLScriptElement>(capScriptSelector)
    expect(script).not.toBeNull()
    expect(script!.getAttribute('src')).toBe('/cap/assets/widget.js')
    expect(window.CAP_CUSTOM_WASM_URL).toBe('/cap/assets/cap_wasm_bg.wasm')
    expect(window.CAP_CUSTOM_HASHWX_URL).toBe('/cap/assets/hashwx.wasm')

    customElements.define('cap-widget', class extends HTMLElement {})
    script!.dispatchEvent(new Event('load'))
    await flushPromises()

    const el = wrapper.get('.turnstile-container').element.querySelector('cap-widget')
    expect(el).not.toBeNull()
    expect(el!.getAttribute('data-cap-api-endpoint')).toBe('/cap/abc123/')
    expect(wrapper.find('[role="status"]').exists()).toBe(false)
  })

  it('supports an explicit base, forwards the solve token and reports errors', async () => {
    wrapper = mount(TurnstileWidget, { props: { siteKey: 'cap:https://cap.example.com/|k9' } })
    await flushPromises()

    const el = wrapper.get('.turnstile-container').element.querySelector('cap-widget')!
    expect(el.getAttribute('data-cap-api-endpoint')).toBe('https://cap.example.com/k9/')
    el.dispatchEvent(new CustomEvent('solve', { detail: { token: 'cap-token-1' } }))
    expect(wrapper.emitted('verify')).toEqual([['cap-token-1']])
    el.dispatchEvent(new CustomEvent('error', { detail: { message: 'boom' } }))
    expect(wrapper.emitted('error')).toEqual([[]])
  })

  it('reset() re-creates the Cap widget for a fresh one-time token', async () => {
    wrapper = mount(TurnstileWidget, { props: { siteKey: 'cap:k1' } })
    await flushPromises()
    const first = wrapper.get('.turnstile-container').element.querySelector('cap-widget')
    ;(wrapper.vm as unknown as { reset: () => void }).reset()
    const second = wrapper.get('.turnstile-container').element.querySelector('cap-widget')
    expect(second).not.toBeNull()
    expect(second).not.toBe(first)
    expect(wrapper.get('.turnstile-container').element.querySelectorAll('cap-widget')).toHaveLength(1)
  })

  it('reports an error for a malformed cap key', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    wrapper = mount(TurnstileWidget, { props: { siteKey: 'cap:' } })
    await flushPromises()
    expect(wrapper.emitted('error')).toEqual([[]])
    expect(wrapper.find('[role="status"]').exists()).toBe(false)
  })
})
