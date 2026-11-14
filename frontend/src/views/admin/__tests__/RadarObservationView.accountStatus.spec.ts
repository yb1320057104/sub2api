import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import RadarObservationView from '../RadarObservationView.vue'
import type { Account } from '@/types'
import type { AccountCandyResult } from '@/api/admin/accountCandy'

const mocks = vi.hoisted(() => ({ accounts: vi.fn(), groups: vi.fn(), get: vi.fn(), replace: vi.fn(), tab: 'accounts' }))
vi.mock('@/api/admin/accountCandy', async () => ({ ...await vi.importActual<typeof import('@/api/admin/accountCandy')>('@/api/admin/accountCandy'), accountCandyAPI: { accounts: mocks.accounts } }))
vi.mock('@/api/intelligenceTest', async () => ({ ...await vi.importActual<typeof import('@/api/intelligenceTest')>('@/api/intelligenceTest'), intelligenceTestAPI: { groups: mocks.groups } }))
vi.mock('@/api/client', () => ({ apiClient: { get: mocks.get } }))
vi.mock('@/api/admin/accounts', async () => ({ ...await vi.importActual<typeof import('@/api/admin/accounts')>('@/api/admin/accounts'), getBatchUsage: vi.fn() }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn() }) }))
vi.mock('vue-router', () => ({ useRoute: () => ({ query: { tab: mocks.tab } }), useRouter: () => ({ replace: mocks.replace }) }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@vueuse/core', async importOriginal => ({ ...await importOriginal<typeof import('@vueuse/core')>(), useIntervalFn: vi.fn() }))
const wrappers: ReturnType<typeof shallowMount>[] = []
beforeEach(() => { vi.clearAllMocks(); mocks.tab = 'accounts'; mocks.groups.mockResolvedValue([]); mocks.get.mockResolvedValue({ data: [] }) })
afterEach(() => wrappers.splice(0).forEach(wrapper => wrapper.unmount()))

async function open(account: Partial<Account>, run: Partial<AccountCandyResult> = {}) {
  mocks.accounts.mockResolvedValue({ total: 1, items: [{ account: { id: 11, name: 'Account', platform: 'openai', type: 'oauth', status: 'active', schedulable: true, ...account }, verdict: 'pass', run: { id: 1, status: 'completed', passed: true, model_results: [], ...run } }] })
  const wrapper = shallowMount(RadarObservationView, { global: { stubs: { AppLayout: { template: '<main><slot /></main>' }, RouterLink: true } } })
  wrappers.push(wrapper); await flushPromises(); return wrapper
}

describe('radar account health', () => {
  it('opens the unified authorization inspection without mounting a second scheduler', async () => {
    mocks.tab = 'authorization'
    const wrapper = await open({})
    expect(mocks.replace).toHaveBeenCalledWith({ path: '/admin/smart-ops', query: { tool: 'credentials', tab: 'inspection' }, hash: '' })
    expect(mocks.accounts).not.toHaveBeenCalled()
    expect(wrapper.find('authorization-schedule-stub').exists()).toBe(false)
  })
  it('opens unified notifications without mounting another alert configuration', async () => {
    mocks.tab = 'emails'
    const wrapper = await open({})
    expect(mocks.replace).toHaveBeenCalledWith({ path: '/admin/smart-ops', query: { tool: 'notifications', notification: 'overview' }, hash: '' })
    expect(mocks.accounts).not.toHaveBeenCalled()
    expect(wrapper.find('radar-email-alerts-stub').exists()).toBe(false)
  })
  it('shows actual error and scheduling pause next to the previous passing result', async () => {
    const wrapper = await open({ status: 'error', schedulable: false, error_message: 'OAuth credential invalidated' })
    const status = wrapper.get('[data-testid="radar-account-status"]')
    expect(status.text()).toContain('admin.accounts.status.error')
    expect(status.text()).toContain('admin.accounts.status.paused')
    expect(status.text()).toContain('OAuth credential invalidated')
    expect(wrapper.text()).toContain('accountCandy.passed')
  })

  it('does not infer stopped account state from a failed request message', async () => {
    const wrapper = await open({}, { status: 'failed', error_message: 'HTTP 401; credentials changed' })
    expect(wrapper.find('[data-testid="radar-account-status"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('HTTP 401; credentials changed')
  })

  it('keeps recovered completed answers passing and displays recovery details', async () => {
    const wrapper = await open({}, { error_message: '已重新请求1次；最近原因：响应中断', move_error: '目标组已改变' })
    expect(wrapper.text()).toContain('accountCandy.passed')
    expect(wrapper.text()).toContain('已重新请求1次')
    expect(wrapper.text()).toContain('目标组已改变')
    expect(wrapper.find('[data-testid="radar-account-status"]').exists()).toBe(false)
  })
})
