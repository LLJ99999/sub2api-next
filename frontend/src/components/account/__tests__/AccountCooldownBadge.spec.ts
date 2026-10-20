import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import AccountCooldownBadge from '../AccountCooldownBadge.vue'
import type { Account, AccountCooldown } from '@/types'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) =>
        params ? `${key}:${JSON.stringify(params)}` : key
    })
  }
})

const HelpTooltipStub = {
  template: '<div><slot name="trigger" /><slot /></div>'
}

function makeAccount(cooldowns: AccountCooldown[] | undefined): Account {
  return {
    id: 7,
    name: 'relay',
    platform: 'openai',
    type: 'apikey',
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    status: 'active',
    error_message: null,
    last_used_at: null,
    expires_at: null,
    auto_pause_on_expired: true,
    created_at: '2026-10-20T00:00:00Z',
    updated_at: '2026-10-20T00:00:00Z',
    schedulable: true,
    rate_limited_at: null,
    rate_limit_reset_at: null,
    overload_until: null,
    temp_unschedulable_until: null,
    temp_unschedulable_reason: null,
    session_window_start: null,
    session_window_end: null,
    session_window_status: null,
    cooldowns
  } as Account
}

const future = (secs: number) => new Date(Date.now() + secs * 1000).toISOString()

describe('AccountCooldownBadge', () => {
  it('renders nothing without active cooldowns', () => {
    const expired: AccountCooldown = {
      kind: 'temp_unschedulable',
      scope: 'account',
      until: new Date(Date.now() - 1000).toISOString(),
      remaining_seconds: 0
    }
    for (const cooldowns of [undefined, [], [expired]]) {
      const wrapper = mount(AccountCooldownBadge, {
        props: { account: makeAccount(cooldowns) },
        global: { stubs: { HelpTooltip: HelpTooltipStub } }
      })
      expect(wrapper.find('[data-testid="account-cooldown-badge"]').exists()).toBe(false)
    }
  })

  it('shows status code, scope, message, breaker level and emits recover', async () => {
    const account = makeAccount([
      {
        kind: 'model_rate_limit',
        scope: 'model',
        model: 'gpt-4o',
        status_code: 503,
        message: 'upstream busy',
        keyword: 'upstream_relay_breaker',
        breaker_level: 2,
        until: future(90),
        remaining_seconds: 90
      },
      { kind: 'rate_limit', scope: 'account', status_code: 429, until: future(30), remaining_seconds: 30 }
    ])
    const wrapper = mount(AccountCooldownBadge, {
      props: { account },
      global: { stubs: { HelpTooltip: HelpTooltipStub } }
    })
    const badge = wrapper.find('[data-testid="account-cooldown-badge"]')
    expect(badge.text()).toContain('admin.accounts.cooldownInfo.badge:{"count":2}')
    const details = wrapper.find('[data-testid="account-cooldown-details"]').text()
    expect(details).toContain('admin.accounts.cooldownInfo.kinds.model_rate_limit')
    expect(details).toContain('admin.accounts.cooldownInfo.scopeModel:{"model":"gpt-4o"}')
    expect(details).toContain('admin.accounts.cooldownInfo.statusCode:{"code":503}')
    expect(details).toContain('upstream busy')
    expect(details).toContain('admin.accounts.cooldownInfo.breakerLevel:{"level":2}')
    expect(details).toContain('admin.accounts.cooldownInfo.scopeAccount')

    await wrapper.find('[data-testid="account-cooldown-recover"]').trigger('click')
    expect(wrapper.emitted('recover')?.[0]).toEqual([account])
  })
})
