import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import RechargeTabsEditor from '../RechargeTabsEditor.vue'
import type { ProviderInstance, RechargeTab } from '@/types/payment'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key, te: () => false }),
}))

const providers: ProviderInstance[] = [
  { id: 1, provider_key: 'easypay', name: 'EP', config: { customMethods: '[{"type":"binance","upstreamType":"binance","displayName":"Binance Pay"}]' }, supported_types: ['alipay', 'wxpay', 'binance'], enabled: true, payment_mode: 'popup', refund_enabled: false, allow_user_refund: false, limits: '', sort_order: 0 },
  { id: 2, provider_key: 'stripe', name: 'S', config: {}, supported_types: ['card', 'link'], enabled: true, payment_mode: '', refund_enabled: false, allow_user_refund: false, limits: '', sort_order: 1 },
]

function mountEditor(modelValue: RechargeTab[]) {
  const wrapper = mount(RechargeTabsEditor, {
    props: {
      modelValue,
      providers,
      'onUpdate:modelValue': (next: RechargeTab[]) => wrapper.setProps({ modelValue: next }),
    },
    global: { stubs: { Icon: true } },
  })
  return wrapper
}

const last = (wrapper: ReturnType<typeof mountEditor>) => {
  const events = wrapper.emitted('update:modelValue') as RechargeTab[][][]
  return events[events.length - 1][0]
}

describe('RechargeTabsEditor', () => {
  it('starts from the default and seeds the first tab with every known method', async () => {
    const wrapper = mountEditor([])
    expect(wrapper.find('[data-testid="recharge-tabs-default"]').exists()).toBe(true)
    await wrapper.get('[data-testid="recharge-tabs-add"]').trigger('click')
    expect(last(wrapper)).toEqual([{ id: 'recharge', name: 'payment.tabTopUp', sort_order: 1, enabled: true, methods: ['alipay', 'wxpay', 'binance', 'stripe'] }])
    expect(wrapper.text()).toContain('Binance Pay')
  })

  it('renames, binds, reorders and deletes tabs', async () => {
    const wrapper = mountEditor([
      { id: 'recharge', name: '充值', sort_order: 1, enabled: true, methods: ['alipay'] },
    ])
    await wrapper.get('[data-testid="recharge-tabs-add"]').trigger('click')
    await wrapper.get('[data-testid="recharge-tab-name-1"]').setValue('币安支付')
    await wrapper.get('[data-testid="recharge-tab-1-method-binance"]').setValue(true)
    expect(last(wrapper)[1]).toMatchObject({ id: 'tab2', name: '币安支付', methods: ['binance'], sort_order: 2 })

    await wrapper.get('[data-testid="recharge-tab-up-1"]').trigger('click')
    expect(last(wrapper).map((tab) => [tab.id, tab.sort_order])).toEqual([['tab2', 1], ['recharge', 2]])

    await wrapper.get('[data-testid="recharge-tab-delete-0"]').trigger('click')
    expect(last(wrapper).map((tab) => tab.id)).toEqual(['recharge'])
  })

  it('flags an enabled tab without methods', () => {
    const wrapper = mountEditor([{ id: 'a', name: 'A', sort_order: 1, enabled: true, methods: [] }])
    expect(wrapper.get('[data-testid="recharge-tabs-error"]').text()).toContain('methodsRequired')
  })
})
