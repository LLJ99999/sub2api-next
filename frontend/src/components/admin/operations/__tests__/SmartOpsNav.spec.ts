import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { beforeEach, describe, expect, it } from 'vitest'
import SmartOpsNav from '../SmartOpsNav.vue'
import { useAdminNavLayoutStore } from '@/stores/adminNavLayout'

const paths = ['/admin/auto-config', '/admin/account-quality', '/admin/harvest-flow', '/admin/token-guard']

async function mountAt(path: string) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: paths.map(route => ({ path: route, component: { template: '<div />' } }))
  })
  await router.push(path)
  await router.isReady()
  return mount(SmartOpsNav, { global: { plugins: [router] } })
}

const hrefs = (wrapper: Awaited<ReturnType<typeof mountAt>>) => wrapper.findAll('a').map(link => link.attributes('href'))

describe('SmartOpsNav', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    useAdminNavLayoutStore().setResolved([
      { path: '/admin/token-guard', label: 'Token Guard', icon: null },
      { path: 'group:custom-a', label: '巡检', icon: null, expandOnly: true, children: [
        { path: '/admin/auto-config', label: '自动配置', icon: null },
        { path: '/admin/account-quality', label: '质量', icon: null },
        { path: '/admin/harvest-flow', label: '工单', icon: null },
      ] },
    ])
  })

  it('shows the pages of the sidebar group the current page belongs to', async () => {
    const wrapper = await mountAt('/admin/harvest-flow')
    expect(hrefs(wrapper)).toEqual(['/admin/auto-config', '/admin/account-quality', '/admin/harvest-flow'])
    expect(wrapper.find('[aria-current="page"]').text()).toBe('工单')
    expect(wrapper.find('nav').attributes('aria-label')).toBe('巡检')
  })

  it('renders nothing for ungrouped pages', async () => {
    const wrapper = await mountAt('/admin/token-guard')
    expect(wrapper.find('nav').exists()).toBe(false)
  })

  it('follows layout changes without remounting', async () => {
    const wrapper = await mountAt('/admin/auto-config')
    expect(hrefs(wrapper)).toHaveLength(3)
    useAdminNavLayoutStore().setResolved([{ path: '/admin/auto-config', label: '自动配置', icon: null }])
    await nextTick()
    expect(wrapper.find('nav').exists()).toBe(false)
  })
})
