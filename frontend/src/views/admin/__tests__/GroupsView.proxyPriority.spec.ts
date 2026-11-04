import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import GroupsView from '../GroupsView.vue'
import GroupProxyPolicyDialog from '@/components/admin/group/GroupProxyPolicyDialog.vue'
import Toggle from '@/components/common/Toggle.vue'

const mocks = vi.hoisted(() => ({ list: vi.fn(), policies: vi.fn(), save: vi.fn(), error: vi.fn(), success: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { groups: { list: mocks.list, getAll: vi.fn().mockResolvedValue([]) }, accounts: { list: vi.fn().mockResolvedValue({ items: [] }) } } }))
vi.mock('@/api/admin/proxyGroups', () => ({ listGroupProxyPolicies: mocks.policies, updateGroupProxyPolicy: mocks.save }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: mocks.error, showSuccess: mocks.success }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isSimpleMode: true }) }))
vi.mock('@/stores/onboarding', () => ({ useOnboardingStore: () => ({ isCurrentStep: () => false }) }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))

const mountView = () => shallowMount(GroupsView, { global: { stubs: {
  AppLayout: { template: '<div><slot/></div>' },
  TablePageLayout: { template: '<div><slot name="filters"/><slot name="table"/><slot name="pagination"/></div>' },
  DataTable: { props: ['data'], template: '<div><div data-test="select-all"><slot name="header-select"/></div><div v-for="row in data" :data-row="row.id"><slot name="cell-select" :row="row"/><slot name="cell-strict_priority" :row="row"/></div></div>' },
  Pagination: { emits: ['update:page'], template: '<button data-test="next-page" @click="$emit(\'update:page\', 2)">Next</button>' }
} } })
let wrapper: ReturnType<typeof mountView>
beforeEach(() => {
  vi.clearAllMocks(); localStorage.clear()
  mocks.list.mockImplementation(async (page: number) => ({ items: page === 1 ? [{ id: 1, name: 'Primary' }] : [{ id: 2, name: 'Second' }], total: 2, page, pages: 2 }))
  mocks.policies.mockResolvedValue([{ group_id: 1, strict_priority: true, proxy_group_id: 7 }])
  mocks.save.mockResolvedValue({ group_count: 1, account_count: 0, binding_count: 0, proxy_count: 0 })
})
afterEach(() => wrapper?.unmount())
describe('business group priority list', () => {
  it('keeps an explicit selection across pages and opens one batch configuration', async () => {
    wrapper = mountView(); await flushPromises(); await wrapper.get('[data-row="1"] input').setValue(true)
    await wrapper.get('[data-test="next-page"]').trigger('click'); await flushPromises(); await wrapper.get('[data-row="2"] input').setValue(true)
    await wrapper.findAll('button').find(button => button.text() === 'admin.proxyGroups.configure')!.trigger('click')
    const dialog = wrapper.getComponent(GroupProxyPolicyDialog)
    expect(dialog.props('show')).toBe(true)
    expect(dialog.props('groupIds')).toEqual([1, 2])
  })
  it('opens configuration before enabling and preserves the selected IP group', async () => {
    mocks.policies.mockResolvedValue([{ group_id: 1, strict_priority: false, proxy_group_id: 7 }])
    wrapper = mountView(); await flushPromises(); wrapper.findAllComponents(Toggle)[0].vm.$emit('update:modelValue', true); await flushPromises()
    expect(mocks.save).not.toHaveBeenCalled()
    expect(wrapper.getComponent(GroupProxyPolicyDialog).props('initialPolicy')).toEqual({ group_id: 1, strict_priority: true, proxy_group_id: 7 })
  })
  it('disables without reassigning proxy bindings and waits for the saved state', async () => {
    wrapper = mountView(); await flushPromises()
    let done!: (value: unknown) => void
    mocks.save.mockImplementation(() => new Promise(resolve => { done = resolve }))
    const toggle = wrapper.findAllComponents(Toggle)[0]
    toggle.vm.$emit('update:modelValue', false); toggle.vm.$emit('update:modelValue', false); await flushPromises()
    expect(mocks.save).toHaveBeenCalledTimes(1)
    expect(mocks.save).toHaveBeenCalledWith({ group_ids: [1], strict_priority: false, assign_proxies: false })
    expect(toggle.props('modelValue')).toBe(true)
    mocks.policies.mockResolvedValue([{ group_id: 1, strict_priority: false, proxy_group_id: 7 }]); done({ group_count: 1 }); await flushPromises()
    expect(toggle.props('modelValue')).toBe(false)
  })
  it('does not allow changing an unknown policy after a load failure', async () => {
    mocks.policies.mockRejectedValue(new Error('unavailable'))
    wrapper = mountView(); await flushPromises(); wrapper.findAllComponents(Toggle)[0].vm.$emit('update:modelValue', true); await flushPromises()
    expect(wrapper.getComponent(GroupProxyPolicyDialog).props('show')).toBe(false)
    expect(mocks.save).not.toHaveBeenCalled()
    expect(mocks.error).toHaveBeenCalledWith('admin.proxyGroups.policiesLoadFailed')
  })
})
