import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const batchBilling = vi.hoisted(() => vi.fn())
const batchQuality = vi.hoisted(() => vi.fn())
const showSuccess = vi.hoisted(() => vi.fn())
const showWarning = vi.hoisted(() => vi.fn())

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess, showWarning, showError: vi.fn() }) }))
vi.mock('@/api/admin/accounts', () => ({ accountsAPI: { batchSetUpstreamBillingProbeEnabled: batchBilling } }))
vi.mock('@/api/admin/upstreamOps', () => ({ batchSetUpstreamQualityAccounts: batchQuality }))

import UpstreamProbeBulkDialog from '../UpstreamProbeBulkDialog.vue'

const BaseDialogStub = { props: ['show', 'title'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' }

function mountDialog(kind: 'billing' | 'quality') {
  return mount(UpstreamProbeBulkDialog, {
    props: { show: true, kind, accountIds: [1, 2], accountNames: { 1: 'a', 2: 'b' } },
    global: { stubs: { BaseDialog: BaseDialogStub } },
  })
}

describe('UpstreamProbeBulkDialog', () => {
  beforeEach(() => {
    batchBilling.mockReset()
    batchQuality.mockReset()
    showSuccess.mockReset()
    showWarning.mockReset()
  })

  it('enables billing monitor in bulk and shows duplicate rows', async () => {
    batchBilling.mockResolvedValue([
      { account_id: 1, status: 'enabled' },
      { account_id: 2, status: 'skipped_duplicate', duplicate_of: 1 },
    ])
    const wrapper = mountDialog('billing')
    await wrapper.get('[data-testid="upstream-probe-bulk-enable"]').trigger('click')
    await flushPromises()
    expect(batchBilling).toHaveBeenCalledWith([1, 2], true)
    expect(wrapper.get('[data-testid="upstream-probe-bulk-row-2"]').text()).toContain('statuses.skipped_duplicate')
    expect(showSuccess).toHaveBeenCalled()
    expect(wrapper.emitted('done')).toHaveLength(1)
  })

  it('sends model and interval for quality probes and warns on partial failure', async () => {
    batchQuality.mockResolvedValue([{ account_id: 1, status: 'enabled' }, { account_id: 2, status: 'failed', message: 'x' }])
    const wrapper = mountDialog('quality')
    await wrapper.get('[data-testid="upstream-probe-bulk-model"]').setValue('gpt-5')
    await wrapper.get('[data-testid="upstream-probe-bulk-enable"]').trigger('click')
    await flushPromises()
    expect(batchQuality).toHaveBeenCalledWith({ account_ids: [1, 2], enabled: true, model: 'gpt-5', interval_minutes: 60 })
    expect(showWarning).toHaveBeenCalled()
  })
})
