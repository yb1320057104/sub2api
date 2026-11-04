import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ProxyGroupsDialog from '../ProxyGroupsDialog.vue'
import ProxyGroupMembersDialog from '../ProxyGroupMembersDialog.vue'
import GroupProxyPolicyDialog from '../../group/GroupProxyPolicyDialog.vue'
import AccountAssignProxiesDialog from '../../account/AccountAssignProxiesDialog.vue'

const api = vi.hoisted(() => ({ listProxyGroups: vi.fn(), createProxyGroup: vi.fn(), updateProxyGroup: vi.fn(), deleteProxyGroup: vi.fn(), assignProxyGroupMembers: vi.fn(), assignAccountProxyGroup: vi.fn(), updateGroupProxyPolicy: vi.fn(), assignProxies: vi.fn(), showSuccess: vi.fn() }))
vi.mock('@/api/admin/proxyGroups', async () => ({ ...await vi.importActual<typeof import('@/api/admin/proxyGroups')>('@/api/admin/proxyGroups'), ...api }))
vi.mock('@/api/admin/accounts', () => ({ assignProxies: api.assignProxies }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: api.showSuccess }) }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))

const group = (overrides = {}) => ({ id: 7, name: 'Static IPs', description: '', proxy_ids: [1, 2], proxy_count: 2, active_proxy_count: 2, ...overrides })
const global = { stubs: {
  BaseDialog: { props: ['show'], template: '<div v-if="show"><slot/><slot name="footer"/></div>' },
  Select: { props: ['modelValue', 'options', 'disabled'], emits: ['update:modelValue'], template: `<select :disabled="disabled" :value="modelValue ?? ''" @change="$emit('update:modelValue', Number($event.target.value))"><option value="">Select</option><option v-for="option in options" :value="option.value">{{ option.label }}</option></select>` }
} }
const wrappers: Array<ReturnType<typeof mount>> = []
function keep<T extends ReturnType<typeof mount>>(wrapper: T): T { wrappers.push(wrapper); return wrapper }
beforeEach(() => { vi.clearAllMocks(); api.listProxyGroups.mockResolvedValue([group()]); api.assignAccountProxyGroup.mockResolvedValue({ account_count: 2, proxy_count: 2, binding_count: 4 }); api.updateGroupProxyPolicy.mockResolvedValue({ group_count: 2, account_count: 3, proxy_count: 2, binding_count: 6 }); api.assignProxyGroupMembers.mockResolvedValue({ proxy_count: 1 }) })
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()) })

describe('IP group account assignment', () => {
  const open = async (ids = [11, 12]) => { const wrapper = keep(mount(AccountAssignProxiesDialog, { props: { show: true, accountIds: ids }, global })); await wrapper.get('input[value="group"]').setValue(true); await flushPromises(); return wrapper }

  it('assigns every group proxy using one frozen account selection and blocks duplicate submissions', async () => {
    const wrapper = await open([11, 11, 12])
    await wrapper.get('select').setValue('7')
    await wrapper.get('[data-testid="assign-proxy-concurrency"]').setValue(10)
    await wrapper.setProps({ accountIds: [99] })
    let resolve!: (value: unknown) => void
    api.assignAccountProxyGroup.mockImplementation(() => new Promise(done => { resolve = done }))
    await wrapper.get('form').trigger('submit')
    await wrapper.get('form').trigger('submit')
    expect(api.assignAccountProxyGroup).toHaveBeenCalledTimes(1)
    expect(api.assignAccountProxyGroup).toHaveBeenCalledWith({ account_ids: [11, 12], proxy_group_id: 7, proxy_concurrency: 10 })
    expect(api.assignProxies).not.toHaveBeenCalled()
    resolve({ account_count: 2, proxy_count: 2, binding_count: 4 }); await flushPromises()
    expect(wrapper.emitted('assigned')).toHaveLength(1)
  })

  it.each([{ proxy_count: 0, active_proxy_count: 0 }, { proxy_count: 2, active_proxy_count: 1 }, { proxy_count: 101, active_proxy_count: 101 }])('rejects an unusable group %j', async overrides => {
    api.listProxyGroups.mockResolvedValue([group(overrides)])
    const wrapper = await open(); await wrapper.get('select').setValue('7'); await wrapper.get('form').trigger('submit')
    expect(api.assignAccountProxyGroup).not.toHaveBeenCalled()
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
  })

  it.each([0, 1.5, 10001])('rejects invalid global concurrency %s', async concurrency => {
    const wrapper = await open(); await wrapper.get('select').setValue('7'); await wrapper.get('[data-testid="assign-proxy-concurrency"]').setValue(concurrency); await wrapper.get('form').trigger('submit')
    expect(api.assignAccountProxyGroup).not.toHaveBeenCalled()
  })

  it('preserves selected group and concurrency after an atomic rejection', async () => {
    const wrapper = await open(); await wrapper.get('select').setValue('7'); await wrapper.get('[data-testid="assign-proxy-concurrency"]').setValue(10)
    api.assignAccountProxyGroup.mockRejectedValue(new Error('capacity'))
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(wrapper.emitted('close')).toBeUndefined()
    expect(wrapper.get<HTMLSelectElement>('select').element.value).toBe('7')
    expect(wrapper.get<HTMLInputElement>('[data-testid="assign-proxy-concurrency"]').element.value).toBe('10')
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
  })

  it('preserves the existing available-proxy allocation path', async () => {
    const wrapper = keep(mount(AccountAssignProxiesDialog, { props: { show: true, accountIds: [11] }, global }))
    api.assignProxies.mockResolvedValue({ account_count: 1, proxy_count: 1, binding_count: 1 })
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(api.assignProxies).toHaveBeenCalledWith({ account_ids: [11], proxies_per_account: 1, max_accounts_per_proxy: 1, proxy_concurrency: 3 })
    expect(api.listProxyGroups).not.toHaveBeenCalled()
  })
})

describe('business group priority policy', () => {
  it('shows a structured capacity rejection and keeps the configuration draft', async () => {
    const wrapper = keep(mount(GroupProxyPolicyDialog, { props: { show: true, groupIds: [4] }, global }))
    await flushPromises(); await wrapper.get('select').setValue('7')
    api.updateGroupProxyPolicy.mockRejectedValue({ status: 400, reason: 'PROXY_ACCOUNT_CAPACITY', message: '代理 #2 将绑定 3 个账号，超过设置的 2 个；本次未保存' })
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('超过设置的 2 个')
    expect(wrapper.get<HTMLSelectElement>('select').element.value).toBe('7')
    expect(wrapper.emitted('close')).toBeUndefined()
  })

  it('atomically enables priority and assigns the same IP group to all selected business groups', async () => {
    const wrapper = keep(mount(GroupProxyPolicyDialog, { props: { show: true, groupIds: [4, 5] }, global }))
    await flushPromises(); await wrapper.get('select').setValue('7'); await wrapper.setProps({ groupIds: [99] })
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(api.updateGroupProxyPolicy).toHaveBeenCalledWith({ group_ids: [4, 5], strict_priority: true, assign_proxies: true, proxy_group_id: 7, proxy_concurrency: 10 })
  })

  it('disables without requiring an IP group or rewriting bindings', async () => {
    api.listProxyGroups.mockResolvedValue([])
    const wrapper = keep(mount(GroupProxyPolicyDialog, { props: { show: true, groupIds: [4], initialPolicy: { group_id: 4, strict_priority: true, proxy_group_id: 7 } }, global }))
    await flushPromises(); await wrapper.get('[role="switch"]').trigger('click'); await wrapper.get('[data-testid="policy-assign-proxies"]').setValue(false)
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(api.updateGroupProxyPolicy).toHaveBeenCalledWith({ group_ids: [4], strict_priority: false, assign_proxies: false })
  })

  it('requires a valid group for enabling and blocks duplicate saves', async () => {
    const wrapper = keep(mount(GroupProxyPolicyDialog, { props: { show: true, groupIds: [4] }, global }))
    await flushPromises(); await wrapper.get('form').trigger('submit'); expect(api.updateGroupProxyPolicy).not.toHaveBeenCalled()
    await wrapper.get('select').setValue('7')
    api.updateGroupProxyPolicy.mockReturnValue(new Promise(() => {}))
    await wrapper.get('form').trigger('submit'); await wrapper.get('form').trigger('submit')
    expect(api.updateGroupProxyPolicy).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[role="switch"]').attributes('disabled')).toBeDefined()
  })
})

describe('proxy membership and group management', () => {
  it('uses a frozen selection and null for removing membership', async () => {
    const wrapper = keep(mount(ProxyGroupMembersDialog, { props: { show: true, proxyIds: [1, 1] }, global }))
    await flushPromises(); await wrapper.setProps({ proxyIds: [2] }); await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(api.assignProxyGroupMembers).toHaveBeenCalledWith([1], null)
    expect(wrapper.emitted('assigned')).toEqual([[[1]]])
  })

  it('creates a trimmed name and keeps form input after a server rejection', async () => {
    const wrapper = keep(mount(ProxyGroupsDialog, { props: { show: true }, global })); await flushPromises()
    await wrapper.get('[data-testid="proxy-group-name"]').setValue('  Static B  ')
    api.createProxyGroup.mockRejectedValue(new Error('duplicate'))
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(api.createProxyGroup).toHaveBeenCalledWith({ name: 'Static B', description: '' })
    expect(wrapper.get<HTMLInputElement>('[data-testid="proxy-group-name"]').element.value).toBe('  Static B  ')
    expect(wrapper.emitted('changed')).toBeUndefined()
  })

  it('requires confirmation before deleting and reloads only after successful deletion', async () => {
    const wrapper = keep(mount(ProxyGroupsDialog, { props: { show: true }, global })); await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'common.delete')!.trigger('click')
    expect(api.deleteProxyGroup).not.toHaveBeenCalled()
    await wrapper.findAll('button').find(button => button.text() === 'common.confirm')!.trigger('click'); await flushPromises()
    expect(api.deleteProxyGroup).toHaveBeenCalledWith(7)
    expect(wrapper.emitted('changed')).toHaveLength(1)
    expect(api.listProxyGroups).toHaveBeenCalledTimes(2)
  })
})
