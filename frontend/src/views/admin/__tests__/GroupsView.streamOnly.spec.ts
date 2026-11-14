import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia } from 'pinia'

import type { AdminGroup } from '@/types'
import GroupsView from '@/views/admin/GroupsView.vue'
import NewFeatureBadge from '@/components/common/NewFeatureBadge.vue'
import { NEW_FEATURE_NOTICES } from '@/constants/newFeatureNotices'
import { useNewFeatureNoticeStore } from '@/stores/newFeatureNotices'

const mocks = vi.hoisted(() => ({
  listGroups: vi.fn(),
  createGroup: vi.fn(),
  updateGroup: vi.fn(),
  getModelAllowlistCandidates: vi.fn(),
  getUsageSummary: vi.fn(),
  getCapacitySummary: vi.fn(),
  getLiveCapability: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    groups: {
      list: mocks.listGroups,
      create: mocks.createGroup,
      update: mocks.updateGroup,
      getModelAllowlistCandidates: mocks.getModelAllowlistCandidates,
      getUsageSummary: mocks.getUsageSummary,
      getCapacitySummary: mocks.getCapacitySummary,
      getLiveCapability: mocks.getLiveCapability,
      getAll: vi.fn().mockResolvedValue([]),
      duplicate: vi.fn(),
      delete: vi.fn(),
      updateSortOrder: vi.fn()
    },
    accounts: { list: vi.fn(), getById: vi.fn() }
  }
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: mocks.showSuccess, showError: mocks.showError }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isSimpleMode: false, user: { id: 41 } }) }))
vi.mock('@/stores/onboarding', () => ({
  useOnboardingStore: () => ({ isCurrentStep: vi.fn(() => false), nextStep: vi.fn() })
}))
vi.mock('@/api/admin/proxyGroups', () => ({
  listGroupProxyPolicies: vi.fn().mockResolvedValue([]),
  listProxyGroups: vi.fn().mockResolvedValue([]),
  updateGroupProxyPolicy: vi.fn()
}))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

const baseGroup = {
  id: 42, name: 'Claude Code', description: null, platform: 'anthropic', rate_multiplier: 1, rpm_limit: 0,
  is_exclusive: false, status: 'active', subscription_type: 'standard',
  daily_limit_usd: null, weekly_limit_usd: null, monthly_limit_usd: null,
  allow_image_generation: false, allow_batch_image_generation: false,
  peak_rate_enabled: false, peak_start: '', peak_end: '', peak_rate_multiplier: 1,
  claude_code_only: false, fallback_group_id: null, fallback_group_id_on_invalid_request: null,
  allow_messages_dispatch: false, require_oauth_only: false, require_privacy_set: false,
  force_openai_fast: false, free_openai_fast: false, stream_only: true,
  model_routing: null, model_routing_enabled: false, mcp_xml_inject: true, supported_model_scopes: [],
  account_count: 1, active_account_count: 1, rate_limited_account_count: 0, sort_order: 10,
  created_at: '2026-09-24T00:00:00Z', updated_at: '2026-09-24T00:00:00Z'
} as unknown as AdminGroup

// 同时渲染名称列和操作列，才能看到「仅流式」标记
const DataTableStub = defineComponent({
  props: { data: { type: Array, default: () => [] } },
  template: `<div><div v-for="row in data" :key="row.id" :data-testid="'group-row-' + row.id">
    <slot name="cell-name" :value="row.name" :row="row" /><slot name="cell-actions" :row="row" />
  </div></div>`
})

function mountView(pinia = createPinia()) {
  return mount(GroupsView, {
    global: {
      plugins: [pinia],
      stubs: {
        AppLayout: defineComponent({ template: '<main><slot /></main>' }),
        TablePageLayout: defineComponent({ template: '<section><slot name="filters" /><slot name="table" /><slot name="pagination" /></section>' }),
        DataTable: DataTableStub,
        BaseDialog: defineComponent({ props: { show: Boolean }, template: '<div v-if="show"><slot /><slot name="footer" /></div>' }),
        Pagination: true, ConfirmDialog: true, EmptyState: true, Select: true, PlatformIcon: true, Icon: true,
        GroupCapacityBadge: true, GroupRateMultipliersModal: true, GroupRPMOverridesModal: true, VueDraggable: true
      }
    }
  })
}

const buttonByText = (wrapper: ReturnType<typeof mountView>, text: string) =>
  wrapper.findAll('button').find((button) => button.text() === text)

describe('GroupsView request policy switches', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.spyOn(console, 'error').mockImplementation(() => {})
    vi.clearAllMocks()
    mocks.listGroups.mockResolvedValue({ items: [baseGroup, { ...baseGroup, id: 43, name: 'Normal', stream_only: false }], total: 2, page: 1, page_size: 20, pages: 1 })
    mocks.createGroup.mockResolvedValue(baseGroup)
    mocks.updateGroup.mockResolvedValue(baseGroup)
    mocks.getModelAllowlistCandidates.mockResolvedValue([])
    mocks.getUsageSummary.mockResolvedValue([])
    mocks.getCapacitySummary.mockResolvedValue([])
    mocks.getLiveCapability.mockResolvedValue({ supported: false })
  })
  afterEach(() => vi.restoreAllMocks())

  it('marks stream-only groups in the list', async () => {
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.find('[data-testid="group-row-42"] [data-testid="group-stream-only-badge"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="group-row-43"] [data-testid="group-stream-only-badge"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('creates a group with the switch turned on', async () => {
    const wrapper = mountView()
    await flushPromises()
    await buttonByText(wrapper, 'admin.groups.createGroup')!.trigger('click')
    await flushPromises()

    const toggle = wrapper.get('[data-testid="create-stream-only"]')
    expect(toggle.attributes('aria-checked')).toBe('false')
    await toggle.trigger('click')
    await wrapper.get('#create-group-form input[type="text"]').setValue('Claude Code')
    await wrapper.get('#create-group-form').trigger('submit')
    await flushPromises()

    expect(mocks.createGroup).toHaveBeenCalledWith(expect.objectContaining({ name: 'Claude Code', stream_only: true }))
    wrapper.unmount()
  })

  it('loads the saved value when editing and sends the change', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="group-row-42"]').findAll('button').find((b) => b.text() === 'common.edit')!.trigger('click')
    await flushPromises()

    const toggle = wrapper.get('[data-testid="edit-stream-only"]')
    expect(toggle.attributes('aria-checked')).toBe('true')
    await toggle.trigger('click')
    await wrapper.get('#edit-group-form').trigger('submit')
    await flushPromises()

    expect(mocks.updateGroup).toHaveBeenCalledWith(42, expect.objectContaining({ stream_only: false }))
    wrapper.unmount()
  })

  it('shows Ultrafast only for enabled OpenAI and composite groups', async () => {
    mocks.listGroups.mockResolvedValue({ items: [
      { ...baseGroup, id: 42, platform: 'openai', force_openai_ultrafast: true },
      { ...baseGroup, id: 43, platform: 'composite', force_openai_ultrafast: true },
      { ...baseGroup, id: 44, platform: 'anthropic', force_openai_ultrafast: true },
      { ...baseGroup, id: 45, platform: 'openai' }
    ], total: 4, page: 1, page_size: 20, pages: 1 })
    const wrapper = mountView()
    await flushPromises()
    for (const id of [42, 43]) {
      expect(wrapper.find(`[data-testid="group-row-${id}"] [data-testid="group-openai-ultrafast-badge"]`).exists()).toBe(true)
    }
    for (const id of [44, 45]) {
      expect(wrapper.find(`[data-testid="group-row-${id}"] [data-testid="group-openai-ultrafast-badge"]`).exists()).toBe(false)
    }
    wrapper.unmount()
  })

  it('creates with Ultrafast, excludes Fast, preserves free Fast and resets for the next group', async () => {
    const wrapper = mountView()
    await flushPromises()
    await buttonByText(wrapper, 'admin.groups.createGroup')!.trigger('click')
    wrapper.findComponent('[data-tour="group-form-platform"]').vm.$emit('update:modelValue', 'openai')
    await flushPromises()

    const fast = wrapper.get('[data-testid="create-force-openai-fast"]')
    const ultrafast = wrapper.get('[data-testid="create-force-openai-ultrafast"]')
    expect(ultrafast.attributes('aria-checked')).toBe('false')
    await fast.trigger('click')
    await wrapper.get('[data-testid="create-free-openai-fast"]').trigger('click')
    await ultrafast.trigger('click')
    expect(fast.attributes('aria-checked')).toBe('false')
    expect(ultrafast.attributes('aria-checked')).toBe('true')
    await wrapper.get('#create-group-form input[type="text"]').setValue('Ultrafast')
    await wrapper.get('#create-group-form').trigger('submit')
    await flushPromises()
    expect(mocks.createGroup).toHaveBeenCalledWith(expect.objectContaining({
      platform: 'openai', force_openai_fast: false, force_openai_ultrafast: true, free_openai_fast: true
    }))

    await buttonByText(wrapper, 'admin.groups.createGroup')!.trigger('click')
    wrapper.findComponent('[data-tour="group-form-platform"]').vm.$emit('update:modelValue', 'openai')
    await flushPromises()
    expect(wrapper.get('[data-testid="create-force-openai-ultrafast"]').attributes('aria-checked')).toBe('false')
    wrapper.unmount()
  })

  it('clears the Ultrafast payload when creating on an unsupported platform', async () => {
    const wrapper = mountView()
    await flushPromises()
    await buttonByText(wrapper, 'admin.groups.createGroup')!.trigger('click')
    const platform = wrapper.findComponent('[data-tour="group-form-platform"]')
    platform.vm.$emit('update:modelValue', 'openai')
    await flushPromises()
    await wrapper.get('[data-testid="create-force-openai-ultrafast"]').trigger('click')
    platform.vm.$emit('update:modelValue', 'anthropic')
    await flushPromises()
    expect(wrapper.find('[data-testid="create-force-openai-ultrafast"]').exists()).toBe(false)
    await wrapper.get('#create-group-form input[type="text"]').setValue('Claude')
    await wrapper.get('#create-group-form').trigger('submit')
    await flushPromises()
    expect(mocks.createGroup).toHaveBeenCalledWith(expect.objectContaining({
      platform: 'anthropic', force_openai_fast: false, force_openai_ultrafast: false
    }))
    wrapper.unmount()
  })

  it.each([false, true])('loads and disables a saved Ultrafast value of %s', async (enabled) => {
    const group = { ...baseGroup, platform: 'openai', ...(enabled ? { force_openai_ultrafast: true } : {}) }
    mocks.listGroups.mockResolvedValue({ items: [group], total: 1, page: 1, page_size: 20, pages: 1 })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="group-row-42"]').findAll('button').find((b) => b.text() === 'common.edit')!.trigger('click')
    await flushPromises()
    const toggle = wrapper.get('[data-testid="edit-force-openai-ultrafast"]')
    expect(toggle.attributes('aria-checked')).toBe(String(enabled))
    if (enabled) await toggle.trigger('click')
    await wrapper.get('#edit-group-form').trigger('submit')
    await flushPromises()
    expect(mocks.updateGroup).toHaveBeenCalledWith(42, expect.objectContaining({ force_openai_ultrafast: false }))
    wrapper.unmount()
  })

  it('truthfully displays and preserves an existing combined policy on an unrelated save', async () => {
    mocks.listGroups.mockResolvedValue({ items: [
      { ...baseGroup, platform: 'openai', force_openai_fast: true, force_openai_ultrafast: true }
    ], total: 1, page: 1, page_size: 20, pages: 1 })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="group-row-42"]').findAll('button').find((b) => b.text() === 'common.edit')!.trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="edit-force-openai-fast"]').attributes('aria-checked')).toBe('true')
    expect(wrapper.get('[data-testid="edit-force-openai-ultrafast"]').attributes('aria-checked')).toBe('true')
    expect(wrapper.find('[data-testid="edit-openai-speed-combined-hint"]').exists()).toBe(true)
    await wrapper.get('#edit-group-form').trigger('submit')
    await flushPromises()
    expect(mocks.updateGroup).toHaveBeenCalledWith(42, expect.objectContaining({
      force_openai_fast: true, force_openai_ultrafast: true
    }))
    wrapper.unmount()
  })

  it('switches a saved Ultrafast policy to Fast when Fast is enabled', async () => {
    mocks.listGroups.mockResolvedValue({ items: [
      { ...baseGroup, platform: 'composite', force_openai_fast: false, force_openai_ultrafast: true }
    ], total: 1, page: 1, page_size: 20, pages: 1 })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="group-row-42"]').findAll('button').find((b) => b.text() === 'common.edit')!.trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="edit-force-openai-fast"]').trigger('click')
    expect(wrapper.get('[data-testid="edit-force-openai-ultrafast"]').attributes('aria-checked')).toBe('false')
    await wrapper.get('#edit-group-form').trigger('submit')
    await flushPromises()
    expect(mocks.updateGroup).toHaveBeenCalledWith(42, expect.objectContaining({
      force_openai_fast: true, force_openai_ultrafast: false
    }))
    wrapper.unmount()
  })

  it('shares the new Ultrafast notice across all entries and persists dismissal per administrator', async () => {
    const pinia = createPinia()
    const notices = useNewFeatureNoticeStore(pinia)
    const noticeId = NEW_FEATURE_NOTICES.groupOpenAIUltrafast
    notices.dismiss(41, NEW_FEATURE_NOTICES.protocolFeatureSwitches)
    notices.dismiss(41, NEW_FEATURE_NOTICES.groupAntiDistillation)
    expect(new Set(Object.values(NEW_FEATURE_NOTICES)).size).toBe(Object.values(NEW_FEATURE_NOTICES).length)
    mocks.listGroups.mockResolvedValue({ items: [
      { ...baseGroup, platform: 'openai', force_openai_ultrafast: true }
    ], total: 1, page: 1, page_size: 20, pages: 1 })
    const wrapper = mountView(pinia)
    await flushPromises()
    await buttonByText(wrapper, 'admin.groups.createGroup')!.trigger('click')
    wrapper.findComponent('[data-tour="group-form-platform"]').vm.$emit('update:modelValue', 'openai')
    await wrapper.get('[data-testid="group-row-42"]').findAll('button').find((b) => b.text() === 'common.edit')!.trigger('click')
    await flushPromises()

    const entries = wrapper.findAllComponents(NewFeatureBadge).filter(badge => badge.props('noticeId') === noticeId)
    expect(entries).toHaveLength(3)
    expect(entries.every(badge => badge.find('[data-testid="new-feature-badge"]').exists())).toBe(true)
    await entries[1].get('[data-testid="new-feature-badge"]').trigger('click')
    expect(entries.every(badge => !badge.find('[data-testid="new-feature-badge"]').exists())).toBe(true)
    expect(mocks.createGroup).not.toHaveBeenCalled()
    expect(mocks.updateGroup).not.toHaveBeenCalled()
    wrapper.unmount()

    const reloadedPinia = createPinia()
    const reloaded = mountView(reloadedPinia)
    await flushPromises()
    const reloadedNotice = reloaded.findAllComponents(NewFeatureBadge).find(badge => badge.props('noticeId') === noticeId)!
    expect(reloadedNotice.find('[data-testid="new-feature-badge"]').exists()).toBe(false)
    const reloadedNotices = useNewFeatureNoticeStore(reloadedPinia)
    expect(reloadedNotices.isDismissed(41, NEW_FEATURE_NOTICES.protocolFeatureSwitches)).toBe(true)
    expect(reloadedNotices.isDismissed(41, NEW_FEATURE_NOTICES.groupAntiDistillation)).toBe(true)
    const future = mount(NewFeatureBadge, { props: { noticeId: 'future-ultrafast-notice-test', userId: 41 }, global: { plugins: [reloadedPinia] } })
    const otherAdmin = mount(NewFeatureBadge, { props: { noticeId, userId: 42 }, global: { plugins: [reloadedPinia] } })
    expect(future.find('[data-testid="new-feature-badge"]').exists()).toBe(true)
    expect(otherAdmin.find('[data-testid="new-feature-badge"]').exists()).toBe(true)
    future.unmount()
    otherAdmin.unmount()
    reloaded.unmount()
  })
})
