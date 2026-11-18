/// <reference path="../../../../vite-env.d.ts" />
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import KiloAccountIdentity from '../KiloAccountIdentity.vue'
import KiloQuotaCell from '../KiloQuotaCell.vue'
import KiloPreparedTable from '../KiloPreparedTable.vue'
import common from '../../../../i18n/locales/en/common'
import kilo from '../../../../i18n/locales/en/kilo'
import type { AccountUsageInfo, KiloAccountView } from '../../../../types'
import type { KiloPrepared } from '../../../../api/admin/kilo'

const { getUsage } = vi.hoisted(() => ({ getUsage: vi.fn() }))
vi.mock('@/api/admin/accounts', () => ({ accountsAPI: { getUsage } }))

function view(bound = true): KiloAccountView {
  return {
    prepared_id: 11, name: 'Kilo primary', email: 'key@example.test', organization_id: 'kilo-org', has_key: true,
    ...(bound ? { source: { id: 122, name: 'GPT identity', email: 'gpt@example.test', plan_type: 'team', workspace_id: 'gpt-workspace', status: 'active', quota_only: true } } : {})
  }
}
function quota(utilization: number | null): AccountUsageInfo {
  return {
    source: 'active', updated_at: null, five_hour: utilization === null ? null : { utilization, resets_at: null, remaining_seconds: 0 },
    seven_day: null, seven_day_sonnet: null
  }
}
function global() {
  return {
    plugins: [createI18n({ legacy: false, locale: 'en', messages: { en: { ...common, kilo } } })],
    stubs: { UsageProgressBar: { props: ['label', 'utilization'], template: '<p data-testid="quota-window">{{ label }} {{ utilization }}%</p>' } }
  }
}

enableAutoUnmount(afterEach)
beforeEach(() => { getUsage.mockReset() })

describe('Kilo logical account card', () => {
  it('shows key and GPT identity together and emits an explicit source-only management action', async () => {
    const wrapper = mount(KiloAccountIdentity, { props: { view: view() }, global: global() })
    expect(wrapper.get('[data-testid="kilo-account-identity"]').text()).toContain('Kilo primary')
    expect(wrapper.get('[data-testid="kilo-gpt-identity"]').text()).toContain('GPT identity')
    expect(wrapper.text()).toContain('gpt@example.test')
    expect(wrapper.text()).toContain('GPT · Business')
    expect(wrapper.text()).toContain('GPT workspace: gpt-workspace')
    expect(wrapper.text()).toContain(kilo.identity.sourcePurpose)
    await wrapper.get('[data-testid="kilo-manage-source"]').trigger('click')
    expect(wrapper.emitted('manage-source')).toEqual([[122]])
    const premium = view()
    premium.source!.plan_type = 'self_serve_business_prolite'
    premium.source!.workspace_id = '12345678-1234-5678-9012-123456789abc'
    await wrapper.setProps({ view: premium })
    expect(wrapper.text()).toContain('GPT · Business Premium')
    expect(wrapper.text()).not.toContain('self_serve_business_prolite')
    expect(wrapper.get('[data-testid="kilo-workspace"]').text()).toBe('GPT workspace: 12345678…9abc')
    expect(wrapper.get('[data-testid="kilo-workspace"]').attributes('title')).toBe(`GPT workspace: ${premium.source!.workspace_id}`)
    await wrapper.setProps({ view: view(false) })
    expect(wrapper.text()).toContain(kilo.identity.unbound)
    expect(wrapper.find('[data-testid="kilo-manage-source"]').exists()).toBe(false)
  })

  it('renders a bound and a single-key prepared card without moving request identity to the GPT source', async () => {
    getUsage.mockResolvedValue(quota(42))
    const base: KiloPrepared = { id: 11, name: 'Kilo primary', email: 'key@example.test', auth_method: 'api_key', organization_id: '', status: 'bound', account_id: 124, source_account_id: 122, has_credential: true, identity_mode: 'unknown', last_error: '', created_at: '2026-11-15T00:00:00Z', account_view: view() }
    const single: KiloPrepared = { ...base, id: 12, name: 'Kilo single', status: 'ready', account_id: 125, source_account_id: null, account_view: { ...view(false), name: 'Kilo single' } }
    const wrapper = mount(KiloPreparedTable, { props: { items: [base, single], selectedIds: [], loading: false, disabled: false }, global: global() })
    await flushPromises()
    expect(wrapper.findAll('[data-testid="kilo-account-identity"]')).toHaveLength(2)
    expect(wrapper.findAll('[data-testid="kilo-quota"]')).toHaveLength(1)
    expect(wrapper.findAll('li')).toHaveLength(2)
    expect(getUsage.mock.calls).toEqual([[124, 'active', false]])
    await wrapper.get('[data-testid="kilo-manage-source"]').trigger('click')
    expect(wrapper.emitted('manage-source')).toEqual([[122]])
    const routing = wrapper.findAll('button').find(button => button.text() === kilo.routing.action)!
    await routing.trigger('click')
    expect(wrapper.emitted('routing')).toEqual([[base]])
    const pair = wrapper.findAll('button').find(button => button.text() === kilo.prepared.pair)!
    await pair.trigger('click')
    expect(wrapper.emitted('pair')).toEqual([[single]])
  })

  it('separates independent, coupled, unverified and locally retired identities and emits row verification', async () => {
    const base: KiloPrepared = { id: 11, name: 'Independent', email: 'independent@example.test', auth_method: 'api_key', organization_id: '', status: 'ready', account_id: null, source_account_id: null, has_credential: true, identity_mode: 'independent', identity_status: 'active', binding_state: 'empty', reusable: true, last_error: '', created_at: '2026-11-15T00:00:00Z' }
    const items: KiloPrepared[] = [
      base,
      { ...base, id: 12, name: 'Coupled', identity_mode: 'gpt_coupled', identity_source_account_id: 122, source_account_id: 126 },
      { ...base, id: 13, name: 'Unverified', identity_mode: 'unknown' },
      { ...base, id: 14, name: 'Retired', identity_mode: 'gpt_coupled', identity_status: 'retired', binding_state: 'blocked', identity_source_account_id: 122 },
      { ...base, id: 15, name: 'Banned', identity_status: 'banned' },
      { ...base, id: 16, name: 'No reusable session', reusable: false },
      { ...base, id: 17, name: 'Unconfirmed status', identity_status: 'unknown' }
    ]
    const wrapper = mount(KiloPreparedTable, { props: { items, selectedIds: [], loading: false, disabled: false }, global: global() })
    const rows = wrapper.findAll('li')
    expect(rows[0]!.get('[data-testid="kilo-identity-mode"]').text()).toBe(kilo.pool.mode.independent)
    expect(rows[1]!.get('[data-testid="kilo-identity-mode"]').text()).toBe(kilo.pool.mode.gpt_coupled)
    expect(rows[1]!.get('[data-testid="kilo-identity-origin"]').text()).toContain('#122')
    expect(rows[1]!.text()).toContain('Source account #126')
    expect(rows[2]!.get('[data-testid="kilo-identity-mode"]').text()).toBe(kilo.pool.mode.unknown)
    expect(rows[2]!.text()).toContain(kilo.pool.modeHint.unknown)
    expect(rows[3]!.text()).toContain(kilo.pool.identity.retired)
    expect(rows[3]!.get('[data-testid="kilo-identity-retired"]').text()).toBe(kilo.pool.retiredHint)
    expect(rows[3]!.text()).not.toContain(kilo.pool.identity.banned)
    expect(rows[4]!.text()).toContain(kilo.pool.identity.banned)
    expect(wrapper.findAll('[data-testid="kilo-fill-slot"]')).toHaveLength(1)
    expect(rows[0]!.find('[data-testid="kilo-fill-slot"]').exists()).toBe(true)
    expect(rows[3]!.get('input[type="checkbox"]').attributes('disabled')).toBeDefined()
    expect(rows[4]!.get('input[type="checkbox"]').attributes('disabled')).toBeDefined()
    await rows[2]!.get('[data-testid="kilo-refresh-identity"]').trigger('click')
    expect(wrapper.emitted('refreshIdentity')).toEqual([[items[2]]])
    await wrapper.setProps({ identityRefreshId: 13, disabled: true })
    expect(rows[2]!.get('[data-testid="kilo-refresh-identity"]').text()).toBe(kilo.pool.refreshingIdentity)
    expect(wrapper.findAll('[data-testid="kilo-refresh-identity"]').every(button => button.attributes('disabled') !== undefined)).toBe(true)
  })

  it('never queries quota for a single key and distinguishes absent official data from zero', async () => {
    const wrapper = mount(KiloQuotaCell, { props: { gatewayId: 124, view: view(false) }, global: global() })
    await flushPromises()
    expect(getUsage).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain(kilo.quota.unbound)
    expect(wrapper.find('button').exists()).toBe(false)
    getUsage.mockResolvedValue(quota(null))
    await wrapper.setProps({ view: view() })
    await flushPromises()
    expect(getUsage.mock.calls).toEqual([[124, 'active', false]])
    expect(wrapper.text()).toContain(kilo.quota.empty)
    expect(wrapper.find('[data-testid="quota-window"]').exists()).toBe(false)
    getUsage.mockResolvedValue(quota(0))
    await wrapper.get('[data-testid="kilo-refresh-quota"]').trigger('click')
    await flushPromises()
    expect(getUsage.mock.calls[1]).toEqual([124, 'active', true])
    expect(wrapper.get('[data-testid="quota-window"]').text()).toBe('5h 0%')
  })

  it('ignores completions from an old key or an unlinked source and exposes official errors', async () => {
    let completeOld!: (value: AccountUsageInfo) => void
    getUsage.mockImplementationOnce(() => new Promise<AccountUsageInfo>(resolve => { completeOld = resolve }))
    const wrapper = mount(KiloQuotaCell, { props: { gatewayId: 124, view: view() }, global: global() })
    expect(wrapper.text()).toContain(kilo.quota.loading)
    getUsage.mockResolvedValueOnce({ ...quota(null), needs_reauth: true, error: 'Expired OAuth' })
    await wrapper.setProps({ gatewayId: 125 })
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe(kilo.quota.needsReauth)
    completeOld(quota(99))
    await flushPromises()
    expect(wrapper.find('[data-testid="quota-window"]').exists()).toBe(false)
    expect(getUsage.mock.calls.map(call => call[0])).toEqual([124, 125])
    getUsage.mockRejectedValueOnce({ status: 503, message: 'Official quota unavailable' })
    await wrapper.get('[data-testid="kilo-refresh-quota"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('Official quota unavailable')
    let completeUnlinked!: (value: AccountUsageInfo) => void
    getUsage.mockImplementationOnce(() => new Promise<AccountUsageInfo>(resolve => { completeUnlinked = resolve }))
    await wrapper.get('[data-testid="kilo-refresh-quota"]').trigger('click')
    await wrapper.setProps({ view: view(false) })
    completeUnlinked(quota(75))
    await flushPromises()
    expect(wrapper.text()).toContain(kilo.quota.unbound)
    expect(wrapper.find('[data-testid="quota-window"]').exists()).toBe(false)
  })
})
