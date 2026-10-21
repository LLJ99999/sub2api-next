import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({
  getAdminNavLayout: vi.fn(async () => ({})),
  updateAdminNavLayout: vi.fn(async (layout: Record<string, unknown>) => layout),
}))
vi.mock('@/api/admin/settings', () => api)
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string, p?: Record<string, unknown>) => (p ? `${key}:${JSON.stringify(p)}` : key) }) }))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showSuccess: vi.fn(), showError: vi.fn() }) }))

import NavLayoutSettings from '../NavLayoutSettings.vue'
import { useAdminNavLayoutStore } from '@/stores/adminNavLayout'
import { buildNavCatalog } from '@/utils/adminNavLayout'

describe('NavLayoutSettings', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    api.updateAdminNavLayout.mockClear()
    useAdminNavLayoutStore().setCatalog(buildNavCatalog([
      { path: '/a', label: 'A', icon: null },
      { path: '/b', label: 'B', icon: null },
      { path: '/c', label: 'C', icon: null },
    ]))
  })

  it('hides an entry, groups two entries and saves the layout', async () => {
    const wrapper = mount(NavLayoutSettings, { global: { stubs: { Icon: true } } })
    await flushPromises()
    await wrapper.get('[data-testid="nav-layout-visible-/c"]').setValue(false)
    await wrapper.get('[data-testid="nav-layout-add-group"]').trigger('click')
    const groupId = wrapper.findAll('[data-testid="nav-layout-location-/a"] option')[1].attributes('value')
    await wrapper.get('[data-testid="nav-layout-location-/a"]').setValue(groupId)
    await wrapper.get('[data-testid="nav-layout-location-/b"]').setValue(groupId)
    await wrapper.get('[data-testid="nav-layout-save"]').trigger('click')
    await flushPromises()
    const saved = api.updateAdminNavLayout.mock.calls[0][0] as { nodes: Array<{ type: string; items?: string[] }>; hidden: string[] }
    expect(saved.hidden).toEqual(['/c'])
    expect(saved.nodes[0]).toMatchObject({ type: 'group', items: ['/a', '/b'] })
    expect(saved.nodes[1]).toEqual({ type: 'item', path: '/c' })
  })

  it('reset to default leaves nothing to save', async () => {
    const wrapper = mount(NavLayoutSettings, { global: { stubs: { Icon: true } } })
    await flushPromises()
    await wrapper.get('[data-testid="nav-layout-visible-/c"]').setValue(false)
    await wrapper.get('[data-testid="nav-layout-reset"]').trigger('click')
    expect(wrapper.get('[data-testid="nav-layout-save"]').attributes('disabled')).toBeDefined()
  })
})
