import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import GroupRPMOverridesModal from '../GroupRPMOverridesModal.vue'
import GroupRateMultipliersModal from '../GroupRateMultipliersModal.vue'
import GroupAntiDistillationSettings from '../GroupAntiDistillationSettings.vue'
import UserAllowlistSelector from '@/components/admin/user/UserAllowlistSelector.vue'
import type { AdminGroup } from '@/types'
const mocks = vi.hoisted(() => ({ list: vi.fn().mockResolvedValue({ items: [] }), getById: vi.fn(), getGroupRPMOverrides: vi.fn().mockResolvedValue([]), getGroupRateMultipliers: vi.fn().mockResolvedValue([]), getAntiDistillation: vi.fn(), saveAntiDistillation: vi.fn(), showSuccess: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { users: mocks, groups: mocks } }))
vi.mock('@/api/admin/users', () => ({ default: mocks }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn(), showSuccess: mocks.showSuccess }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
beforeEach(() => { vi.clearAllMocks(); vi.useFakeTimers() })
afterEach(() => { vi.restoreAllMocks(); vi.useRealTimers() })
enableAutoUnmount(afterEach)
describe.each([GroupRPMOverridesModal, GroupRateMultipliersModal])('group modal lifecycle', (component) => {
  function open() {
    return mount(component, { props: { show: true, group: { id: 1, name: 'Group', platform: 'openai' } as AdminGroup },
      global: { stubs: { BaseDialog: { template: '<div><slot /></div>' }, Icon: true, PlatformIcon: true, Pagination: true } } })
  }
  it('removes its document click listener on unmount', () => {
    const add = vi.spyOn(document, 'addEventListener'); const remove = vi.spyOn(document, 'removeEventListener')
    const w = open()
    const handlers = add.mock.calls.filter(([event]) => event === 'click').map(([, handler]) => handler)
    expect(handlers.length).toBeGreaterThan(0)
    w.unmount()
    for (const handler of handlers) expect(remove).toHaveBeenCalledWith('click', handler)
  })
  it('cancels a queued search when navigating away', async () => {
    const w = open(); await w.get('input[type="text"]').setValue('alice')
    w.unmount(); await vi.advanceTimersByTimeAsync(300); await flushPromises()
    expect(mocks.list).not.toHaveBeenCalled()
  })
})

describe('group anti-distillation user allowlist lifecycle', () => {
  const currentConfig = () => ({ enabled: true, pause_minutes: 5, user_allowlist_ids: [42, 7] })
  beforeEach(() => {
    mocks.getAntiDistillation.mockReset().mockResolvedValue(currentConfig())
    mocks.saveAntiDistillation.mockReset().mockImplementation(async (_id, value) => value)
    mocks.list.mockReset().mockResolvedValue({ items: [], total: 0 })
    mocks.getById.mockReset().mockImplementation(async (id: number) => {
      if (id === 7) return { id, email: 'selected@example.com', status: 'active' }
      throw new Error('not found')
    })
  })
  const open = () => mount(GroupAntiDistillationSettings, { props: { groupId: 1, active: true } })

  it('preserves missing saved IDs, adds searched users, and saves or clears the complete independent list', async () => {
    mocks.list.mockResolvedValue({ items: [{ id: 8, email: 'found@example.com', status: 'active' }], total: 1 })
    const wrapper = open()
    await flushPromises()
    expect(wrapper.get('[data-test="allowlist-selected-42"]').text()).toContain('admin.userAllowlist.unresolved')
    expect(wrapper.get('[data-test="allowlist-selected-7"]').text()).toContain('selected@example.com')
    expect(wrapper.getComponent(UserAllowlistSelector).props()).toMatchObject({ maxUsers: 500, searchDebounceMs: 300, hydrateLimit: 20 })
    await wrapper.get('[data-test="allowlist-search"]').setValue('found')
    await vi.advanceTimersByTimeAsync(299)
    expect(mocks.list).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    await flushPromises()
    expect(mocks.list).toHaveBeenCalledWith(1, 10, { search: 'found' }, { signal: expect.any(AbortSignal) })
    await wrapper.get('[data-test="allowlist-add-8"]').trigger('click')
    await wrapper.get('[data-test="allowlist-remove-42"]').trigger('click')
    await wrapper.get('[data-testid="anti-distillation-save"]').trigger('click')
    await flushPromises()
    expect(mocks.saveAntiDistillation).toHaveBeenLastCalledWith(1, { enabled: true, pause_minutes: 5, user_allowlist_ids: [7, 8] })
    await wrapper.get('[data-test="allowlist-clear"]').trigger('click')
    await wrapper.get('[data-testid="anti-distillation-save"]').trigger('click')
    await flushPromises()
    expect(mocks.saveAntiDistillation).toHaveBeenLastCalledWith(1, { enabled: true, pause_minutes: 5, user_allowlist_ids: [] })
  })

  it('loads a legacy response as an empty list and still sends an explicit empty list when disabled', async () => {
    mocks.getAntiDistillation.mockResolvedValue({ enabled: false, pause_minutes: 5 })
    const wrapper = open()
    await flushPromises()
    expect(wrapper.getComponent(UserAllowlistSelector).props('modelValue')).toEqual([])
    await wrapper.get('[data-testid="anti-distillation-save"]').trigger('click')
    await flushPromises()
    expect(mocks.saveAntiDistillation).toHaveBeenCalledWith(1, { enabled: false, pause_minutes: 5, user_allowlist_ids: [] })
  })

  it('keeps the draft and shows the backend validation error after a failed save', async () => {
    mocks.saveAntiDistillation.mockRejectedValue({ status: 400, message: 'User #42 is no longer available; remove it before saving.' })
    const wrapper = open()
    await flushPromises()
    await wrapper.get('[data-testid="anti-distillation-minutes"]').setValue('9')
    await wrapper.get('[data-testid="anti-distillation-save"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('User #42 is no longer available')
    expect(wrapper.getComponent(UserAllowlistSelector).props('modelValue')).toEqual([42, 7])
    expect((wrapper.get('[data-testid="anti-distillation-minutes"]').element as HTMLInputElement).value).toBe('9')
    expect(mocks.showSuccess).not.toHaveBeenCalled()
  })

  it('does not allow saving or selecting users until the configuration loads successfully', async () => {
    mocks.getAntiDistillation.mockRejectedValueOnce(new Error('unavailable'))
    const wrapper = open()
    await flushPromises()
    expect(wrapper.get('[data-testid="anti-distillation-save"]').attributes('disabled')).toBeDefined()
    expect(wrapper.findComponent(UserAllowlistSelector).exists()).toBe(false)
    await wrapper.get('[data-testid="anti-distillation-save"]').trigger('click')
    expect(mocks.saveAntiDistillation).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="anti-distillation-retry"]').trigger('click')
    await flushPromises()
    expect(wrapper.getComponent(UserAllowlistSelector).props('modelValue')).toEqual([42, 7])
  })

  it('discards an old group load and resets the draft on group changes and close', async () => {
    let resolveOld!: (value: unknown) => void
    mocks.getAntiDistillation.mockImplementationOnce(() => new Promise((resolve) => { resolveOld = resolve }))
    const wrapper = open()
    mocks.getAntiDistillation.mockResolvedValueOnce({ enabled: true, pause_minutes: 6, user_allowlist_ids: [9] })
    await wrapper.setProps({ groupId: 2 })
    await flushPromises()
    resolveOld(currentConfig())
    await flushPromises()
    expect(wrapper.getComponent(UserAllowlistSelector).props('modelValue')).toEqual([9])
    await wrapper.get('[data-test="allowlist-search"]').setValue('pending')
    await wrapper.setProps({ active: false })
    await vi.advanceTimersByTimeAsync(300)
    expect(mocks.list).not.toHaveBeenCalled()
    expect(wrapper.findComponent(UserAllowlistSelector).exists()).toBe(false)
    await wrapper.setProps({ active: true })
    await flushPromises()
    expect(mocks.getAntiDistillation).toHaveBeenLastCalledWith(2)
    expect(wrapper.getComponent(UserAllowlistSelector).props('modelValue')).toEqual([42, 7])
  })

  it('ignores an in-flight search and save from a closed group', async () => {
    let resolveSearch!: (value: unknown) => void
    let resolveSave!: (value: unknown) => void
    mocks.list.mockImplementationOnce(() => new Promise((resolve) => { resolveSearch = resolve }))
    mocks.saveAntiDistillation.mockImplementationOnce(() => new Promise((resolve) => { resolveSave = resolve }))
    const wrapper = open()
    await flushPromises()
    await wrapper.get('[data-test="allowlist-search"]').setValue('old')
    await vi.advanceTimersByTimeAsync(300)
    const signal = mocks.list.mock.calls[0][3].signal as AbortSignal
    await wrapper.get('[data-testid="anti-distillation-save"]').trigger('click')
    expect(wrapper.getComponent(UserAllowlistSelector).props('disabled')).toBe(true)
    expect(signal.aborted).toBe(true)
    await wrapper.setProps({ active: false, groupId: 2 })
    resolveSearch({ items: [{ id: 98, email: 'old@example.com' }], total: 1 })
    resolveSave(currentConfig())
    await flushPromises()
    expect(wrapper.findComponent(UserAllowlistSelector).exists()).toBe(false)
    expect(mocks.showSuccess).not.toHaveBeenCalled()
    await wrapper.setProps({ active: true })
    await flushPromises()
    expect(wrapper.find('[data-test="allowlist-add-98"]').exists()).toBe(false)
  })

  it('isolates overlapping debounced searches and preserves the draft when search fails', async () => {
    let resolveOld!: (value: unknown) => void
    mocks.list.mockImplementationOnce(() => new Promise((resolve) => { resolveOld = resolve }))
    const wrapper = open()
    await flushPromises()
    const input = wrapper.get('[data-test="allowlist-search"]')
    await input.setValue('old')
    await vi.advanceTimersByTimeAsync(300)
    const oldSignal = mocks.list.mock.calls[0][3].signal as AbortSignal
    mocks.list.mockResolvedValueOnce({ items: [{ id: 8, email: 'new@example.com' }], total: 1 })
    await input.setValue('new')
    expect(oldSignal.aborted).toBe(true)
    await vi.advanceTimersByTimeAsync(300)
    await flushPromises()
    resolveOld({ items: [{ id: 99, email: 'old@example.com' }], total: 1 })
    await flushPromises()
    expect(wrapper.find('[data-test="allowlist-add-8"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="allowlist-add-99"]').exists()).toBe(false)
    mocks.list.mockRejectedValueOnce(new Error('unavailable'))
    await input.setValue('failure')
    await vi.advanceTimersByTimeAsync(300)
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('admin.userAllowlist.searchFailed')
    expect(wrapper.getComponent(UserAllowlistSelector).props('modelValue')).toEqual([42, 7])
  })

  it('hydrates at most five selected user details concurrently and cancels the queue on close', async () => {
    const ids = Array.from({ length: 500 }, (_, index) => index + 1)
    mocks.getAntiDistillation.mockResolvedValue({ enabled: true, pause_minutes: 5, user_allowlist_ids: ids })
    const resolvers: Array<() => void> = []
    let active = 0
    let maxActive = 0
    mocks.getById.mockImplementation((id: number) => {
      active++
      maxActive = Math.max(maxActive, active)
      return new Promise((resolve) => { resolvers.push(() => { active--; resolve({ id, email: `user-${id}@example.com` }) }) })
    })
    const wrapper = open()
    await flushPromises()
    expect(mocks.getById).toHaveBeenCalledTimes(5)
    resolvers.shift()!()
    await flushPromises()
    expect(mocks.getById).toHaveBeenCalledTimes(6)
    expect(maxActive).toBe(5)
    await wrapper.setProps({ active: false })
    for (const resolve of resolvers) resolve()
    await flushPromises()
    expect(mocks.getById).toHaveBeenCalledTimes(6)
    expect(wrapper.findComponent(UserAllowlistSelector).exists()).toBe(false)
  })

  it('limits detail hydration and prevents adding a 501st user without dropping saved IDs', async () => {
    const ids = Array.from({ length: 500 }, (_, index) => index + 1)
    mocks.getAntiDistillation.mockResolvedValue({ enabled: true, pause_minutes: 5, user_allowlist_ids: ids })
    mocks.getById.mockImplementation(async (id: number) => ({ id, email: `user-${id}@example.com` }))
    mocks.list.mockResolvedValue({ items: [{ id: 501, email: 'next@example.com' }], total: 1 })
    const wrapper = open()
    await flushPromises()
    expect(mocks.getById).toHaveBeenCalledTimes(20)
    expect(wrapper.findAll('[data-test^="allowlist-selected-"]')).toHaveLength(500)
    expect(wrapper.get('[data-test="allowlist-selected-500"]').text()).toContain('#500')
    await wrapper.get('[data-test="allowlist-search"]').setValue('next')
    await vi.advanceTimersByTimeAsync(300)
    await flushPromises()
    expect(wrapper.get('[data-test="allowlist-add-501"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="anti-distillation-save"]').trigger('click')
    await flushPromises()
    expect(mocks.saveAntiDistillation).toHaveBeenCalledWith(1, expect.objectContaining({ user_allowlist_ids: ids }))
  })

  it('rejects malformed loaded IDs or more than 500 users instead of silently truncating the saved list', async () => {
    mocks.getAntiDistillation.mockResolvedValue({ enabled: true, pause_minutes: 5, user_allowlist_ids: Array.from({ length: 501 }, (_, index) => index + 1) })
    const wrapper = open()
    await flushPromises()
    expect(wrapper.get('[data-testid="anti-distillation-save"]').attributes('disabled')).toBeDefined()
    expect(wrapper.getComponent(UserAllowlistSelector).props('modelValue')).toHaveLength(501)
    await wrapper.get('[data-testid="anti-distillation-save"]').trigger('click')
    expect(mocks.saveAntiDistillation).not.toHaveBeenCalled()
    await wrapper.get('[data-test="allowlist-remove-501"]').trigger('click')
    expect(wrapper.get('[data-testid="anti-distillation-save"]').attributes('disabled')).toBeUndefined()
    wrapper.getComponent(UserAllowlistSelector).vm.$emit('update:modelValue', [0])
    await flushPromises()
    expect(wrapper.get('[data-testid="anti-distillation-save"]').attributes('disabled')).toBeDefined()
  })
})
