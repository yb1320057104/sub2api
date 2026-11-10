import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { createRouter, createMemoryHistory, type Router } from 'vue-router'
import { flushPromises, mount } from '@vue/test-utils'
import type { PromptAuditConfig, PromptAuditDraft, PromptAuditRuntime, PromptAuditUpdateRequest } from '../types'
import { SCANNER_CATALOG } from '../viewModel'
import PromptAuditView from '../PromptAuditView.vue'
import LocalRulePanel from '../components/LocalRulePanel.vue'
import { defaultAuditRiskPolicy } from '@/types/auditRiskPolicy'

const mocks = vi.hoisted(() => ({
  getConfig: vi.fn(), updateConfig: vi.fn(), probeEndpoint: vi.fn(), getRuntime: vi.fn(), listEvents: vi.fn(),
  getEvent: vi.fn(), deleteEvent: vi.fn(), batchDeleteEvents: vi.fn(), previewDelete: vi.fn(), deleteEventsByFilter: vi.fn(), listGroups: vi.fn(),
  showSuccess: vi.fn(), showError: vi.fn(),
}))

vi.mock('../api', () => ({ default: mocks }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: mocks.showSuccess, showError: mocks.showError }) }))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ locale: { value: 'en' }, t: (key: string, params?: Record<string, unknown>) => key.replace(/\{(\w+)\}/g, (_, token) => String(params?.[token] ?? `{${token}}`)) }) }
})

const baseConfig = (): PromptAuditConfig => ({
  enabled: true, blocking_enabled: false, blocking_latest_turn_only: false, store_pass_events: false, effective_mode: 'async_audit', strategy: 'priority',
  worker_count: 4, queue_capacity: 100, scanners: SCANNER_CATALOG.map((item) => item.id), all_groups: true, group_ids: [],
  endpoints: [{ id: 'guard-1', name: 'Guard One', protocol: 'openai_compatible', base_url: 'http://127.0.0.1:8000', model: 'guard-model', timeout_ms: 3000, input_limit: 4000, enabled: true, has_token: true, token_status: 'configured' }],
  config_version: 7, updated_at: '2026-07-16T00:00:00Z', updated_by: 1, change_summary: '{}',
})
const runtime = (): PromptAuditRuntime => ({
  process_status: 'running', effective_mode: 'async_audit', expected_config_version: 7, active_config_version: 7,
  worker_total: 4, worker_active: 1, queue_capacity: 100,
  queue: { staging: 0, queued: 0, processing: 1, retry: 0, done: 5, failed: 0, active: 1 },
  processed_total: 5, failed_total: 0, enqueued_total: 5, dropped_total: 0, database_status: 'ok', redis_status: 'ok', endpoints: {},
  guard_metrics: { total: 1, allowed: 1, flagged: 0, blocked: 0, unavailable: 0, invalid: 0, timeouts: 0, failovers: 0, bulkhead_full: 0, record_failed: 0 },
})

const AppLayoutStub = { template: '<div><slot /></div>' }
const RuntimeStub = defineComponent({ props: ['runtime', 'loading', 'error'], emits: ['refresh'], template: '<div data-test="runtime">{{ error }}</div>' })
const EndpointStub = defineComponent({
  props: ['endpoints', 'probeResults', 'probingIds'], emits: ['update:endpoints', 'probe'],
  template: '<div data-test="endpoint"><button data-test="inject-secret" @click="$emit(\'update:endpoints\', endpoints.map((e) => ({ ...e, token: \'PROMPT_AUDIT_CANARY_SECRET_DO_NOT_PERSIST\' })))">secret</button><button data-test="probe" @click="$emit(\'probe\', endpoints[0])">probe</button></div>',
})
const PolicyStub = defineComponent({ props: ['draft', 'groups'], emits: ['update:draft'], template: '<div data-test="policy" />' })
const EventsStub = defineComponent({
  props: ['events', 'filters', 'selectedIds', 'loading', 'error', 'total', 'page', 'pageSize'],
  emits: ['filters-change', 'search', 'selection', 'page', 'page-size', 'view', 'delete', 'batch-delete', 'preview-delete'],
  template: '<div data-test="events"><button data-test="preview" @click="$emit(\'preview-delete\')">preview</button><button data-test="change-filter" @click="$emit(\'filters-change\', { ...filters, keyword: \'changed\' })">change</button><button data-test="delete-one" @click="$emit(\'delete\', 5)">delete</button><button data-test="select-batch" @click="$emit(\'selection\', [5, 6])">select</button><button data-test="delete-batch" @click="$emit(\'batch-delete\')">batch</button></div>',
})
const DetailStub = defineComponent({ props: ['show', 'event', 'loading'], emits: ['close'], template: '<div data-test="detail" />' })
const ConfirmStub = defineComponent({ props: ['show', 'title', 'message'], emits: ['confirm', 'cancel'], template: '<div v-if="show" data-test="confirm"><button data-test="confirm-action" @click="$emit(\'confirm\')">confirm</button><button data-test="cancel-action" @click="$emit(\'cancel\')">cancel</button></div>' })
const FilterDeleteStub = defineComponent({
  props: ['show', 'initialFilters', 'preview', 'previewing', 'deleting'],
  emits: ['close', 'preview', 'confirm', 'criteria-change'],
  template: '<div v-if="show" data-test="filter-delete-dialog"><button data-test="dialog-preview" @click="$emit(\'preview\', { ...initialFilters, start_at: \'2026-07-15T00:00\', end_at: \'2026-07-16T00:00\' })">run</button><button data-test="dialog-confirm" @click="$emit(\'confirm\', { ...initialFilters, start_at: \'2026-07-15T00:00\', end_at: \'2026-07-16T00:00\' })">confirm</button><span data-test="dialog-preview-state">{{ preview ? preview.matched_count : \'none\' }}</span></div>',
})

function mountView(props: { embedded?: boolean; eventsOnly?: boolean } = {}, router?: Router) {
  return mount(PromptAuditView, {
    props,
    global: { plugins: router ? [router] : [], stubs: { SecurityAuditNav: true, IPAuditPanel: { template: '<div data-testid="ip-audit-panel" />' }, AppLayout: AppLayoutStub, RuntimeOverview: RuntimeStub, EndpointPool: EndpointStub, PolicyPanel: PolicyStub, PenaltyPolicyPanel: true, EventWorkspace: EventsStub, EventDetailDialog: DetailStub, FilterDeleteDialog: FilterDeleteStub, ConfirmDialog: ConfirmStub } },
  })
}

describe('PromptAuditView', () => {
  beforeEach(() => {
    Object.values(mocks).forEach((mock) => mock.mockReset())
    mocks.getConfig.mockResolvedValue(baseConfig())
    mocks.getRuntime.mockResolvedValue(runtime())
    mocks.listGroups.mockResolvedValue([])
    mocks.listEvents.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20, pages: 0 })
    mocks.updateConfig.mockImplementation(async () => ({ ...baseConfig(), config_version: 8 }))
    mocks.probeEndpoint.mockResolvedValue({ ok: true, status: 'healthy', message: 'ok', latency_ms: 2, http_status: 200, retryable: false, checked_at: '2026-07-16T00:00:00Z', token_applied: true })
    mocks.previewDelete.mockResolvedValue({ matched_count: 2, filter_summary: {}, snapshot_max_id: 10, filter_hash: 'a'.repeat(64), confirmation_token: 'opaque-confirmation', expires_at: '2026-07-16T00:05:00Z' })
    mocks.deleteEventsByFilter.mockResolvedValue({ deleted_events: 2, deleted_jobs: 2 })
    mocks.deleteEvent.mockResolvedValue({ deleted_events: 1, deleted_jobs: 1 })
    mocks.batchDeleteEvents.mockResolvedValue({ deleted_events: 2, deleted_jobs: 2 })
  })

  it('keeps events available without mounting configuration in the audit center', async () => {
    mocks.getConfig.mockRejectedValue(new Error('configuration unavailable'))
    const wrapper = mountView({ embedded: true, eventsOnly: true })
    await flushPromises()
    expect(wrapper.find('[data-test="tab-panel-events"]').isVisible()).toBe(true)
    expect(wrapper.find('[data-test="tab-panel-config"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="tab-config"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="save-config"]').exists()).toBe(false)
    expect(mocks.listEvents).toHaveBeenCalledOnce()
    expect(mocks.getRuntime).not.toHaveBeenCalled()
    expect(mocks.listGroups).not.toHaveBeenCalled()
    expect(mocks.updateConfig).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('opts into scored policy without changing legacy actions, saves zero scores, and explicitly restores legacy mode', async () => {
    const oldRule = { id: 'one', name: 'Existing', kind: 'keyword' as const, pattern: 'custom phrase', category: 'pii', action: 'block' as const, enabled: true, case_sensitive: false }
    const config = { ...baseConfig(), engine: 'local_rules' as const, local_rules: [oldRule] }
    mocks.getConfig.mockResolvedValue(config)
    mocks.updateConfig.mockImplementation(async (payload: PromptAuditUpdateRequest) => ({ ...config, ...payload, config_version: 8 }))
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="tab-config"]').trigger('click')
    expect(wrapper.find('[data-test="prompt-risk-policy-legacy"]').exists()).toBe(true)
    expect(mocks.updateConfig).not.toHaveBeenCalled()
    await wrapper.get('[data-test="prompt-risk-policy-enabled"]').trigger('click')
    expect(wrapper.get<HTMLInputElement>('[data-test="local-rule-score-0"]').element.value).toBe('100')
    await wrapper.get('[data-test="local-rule-score-0"]').setValue(0)
    await wrapper.get('[data-test="prompt-risk-medium-action"]').setValue('charge_allow')
    await wrapper.get('[data-test="save-config"]').trigger('click')
    await flushPromises()
    expect(mocks.updateConfig).toHaveBeenLastCalledWith(expect.objectContaining({ risk_policy: { ...defaultAuditRiskPolicy(), medium_action: 'charge_allow' }, local_rules: [{ ...oldRule, score: 0 }] }))
    await wrapper.get('[data-test="prompt-risk-policy-enabled"]').trigger('click')
    await wrapper.get('[data-test="save-config"]').trigger('click')
    await flushPromises()
    expect(mocks.updateConfig).toHaveBeenLastCalledWith(expect.objectContaining({ risk_policy: null, local_rules: [{ ...oldRule, score: 0 }] }))
    expect(mocks.probeEndpoint).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('rejects invalid risk thresholds before sending configuration', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="tab-config"]').trigger('click')
    await wrapper.get('[data-test="prompt-risk-policy-enabled"]').trigger('click')
    await wrapper.get('[data-test="prompt-risk-high-threshold"]').setValue(40)
    await wrapper.get('[data-test="save-config"]').trigger('click')
    expect(mocks.updateConfig).not.toHaveBeenCalled()
    expect(mocks.showError).toHaveBeenCalledWith('admin.promptAudit.riskPolicy.invalid')
    wrapper.unmount()
  })

  it('opts into scored policy without changing legacy actions, saves zero scores, and explicitly restores legacy mode', async () => {
    const oldRule = { id: 'one', name: 'Existing', kind: 'keyword' as const, pattern: 'custom phrase', category: 'pii', action: 'block' as const, enabled: true, case_sensitive: false }
    const config = { ...baseConfig(), engine: 'local_rules' as const, local_rules: [oldRule] }
    mocks.getConfig.mockResolvedValue(config)
    mocks.updateConfig.mockImplementation(async (payload: PromptAuditUpdateRequest) => ({ ...config, ...payload, config_version: 8 }))
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="tab-config"]').trigger('click')
    expect(wrapper.find('[data-test="prompt-risk-policy-legacy"]').exists()).toBe(true)
    expect(mocks.updateConfig).not.toHaveBeenCalled()
    await wrapper.get('[data-test="prompt-risk-policy-enabled"]').trigger('click')
    expect(wrapper.get<HTMLInputElement>('[data-test="local-rule-score-0"]').element.value).toBe('100')
    await wrapper.get('[data-test="local-rule-score-0"]').setValue(0)
    await wrapper.get('[data-test="prompt-risk-medium-action"]').setValue('charge_allow')
    await wrapper.get('[data-test="save-config"]').trigger('click')
    await flushPromises()
    expect(mocks.updateConfig).toHaveBeenLastCalledWith(expect.objectContaining({ risk_policy: { ...defaultAuditRiskPolicy(), medium_action: 'charge_allow' }, local_rules: [{ ...oldRule, score: 0 }] }))
    await wrapper.get('[data-test="prompt-risk-policy-enabled"]').trigger('click')
    await wrapper.get('[data-test="save-config"]').trigger('click')
    await flushPromises()
    expect(mocks.updateConfig).toHaveBeenLastCalledWith(expect.objectContaining({ risk_policy: null, local_rules: [{ ...oldRule, score: 0 }] }))
    expect(mocks.probeEndpoint).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('rejects invalid risk thresholds before sending configuration', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="tab-config"]').trigger('click')
    await wrapper.get('[data-test="prompt-risk-policy-enabled"]').trigger('click')
    await wrapper.get('[data-test="prompt-risk-high-threshold"]').setValue(40)
    await wrapper.get('[data-test="save-config"]').trigger('click')
    expect(mocks.updateConfig).not.toHaveBeenCalled()
    expect(mocks.showError).toHaveBeenCalledWith('admin.promptAudit.riskPolicy.invalid')
    wrapper.unmount()
  })

  it('saves local auditing with selected groups without any endpoint or probe', async () => {
    const localConfig: PromptAuditConfig = {
      ...baseConfig(), engine: 'local_rules', enabled: false, endpoints: [], group_ids: [], all_groups: false,
      audit_instructions: 'Local policy', local_rules: [{ id: 'one', name: 'Test rule', kind: 'keyword', pattern: 'test pattern', category: 'jailbreak', action: 'warn', enabled: true, case_sensitive: false }],
    }
    mocks.getConfig.mockResolvedValue(localConfig)
    mocks.updateConfig.mockImplementation(async (payload: PromptAuditUpdateRequest) => ({ ...localConfig, ...payload, config_version: 8 }))
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="tab-config"]').trigger('click')
    expect(wrapper.find('[data-test="endpoint"]').exists()).toBe(false)
    const policy = wrapper.getComponent(PolicyStub)
    policy.vm.$emit('update:draft', { ...policy.props('draft'), group_ids: [3] })
    await flushPromises()
    await wrapper.get('[data-test="enabled-toggle"]').trigger('click')
    await wrapper.get('[data-test="save-config"]').trigger('click')
    await flushPromises()
    expect(mocks.updateConfig).toHaveBeenCalledWith(expect.objectContaining({ engine: 'local_rules', enabled: true, all_groups: false, group_ids: [3], endpoints: [], local_rules: localConfig.local_rules }))
    expect(mocks.probeEndpoint).not.toHaveBeenCalled()
    expect(mocks.showError).not.toHaveBeenCalled()
    expect(wrapper.get('[data-test="save-config"]').attributes()).toHaveProperty('disabled')
    wrapper.unmount()
  })

  it('saves and clears the independent prompt audit user allowlist while preserving groups and rules', async () => {
    const localConfig: PromptAuditConfig = { ...baseConfig(), engine: 'local_rules', user_allowlist_ids: [42], group_ids: [3], endpoints: [], audit_instructions: 'custom notes', local_rules: [{ id: 'one', name: 'Custom', kind: 'keyword', pattern: 'custom phrase', category: 'jailbreak', action: 'warn', enabled: true, case_sensitive: false }] }
    mocks.getConfig.mockResolvedValue(localConfig)
    mocks.updateConfig.mockImplementation(async (payload: PromptAuditUpdateRequest) => ({ ...localConfig, ...payload, config_version: 8 }))
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="tab-config"]').trigger('click')
    const policy = wrapper.getComponent(PolicyStub)
    expect(policy.props('draft').user_allowlist_ids).toEqual([42])
    policy.vm.$emit('update:draft', { ...policy.props('draft'), user_allowlist_ids: [42, 7, 7] })
    await flushPromises()
    expect(mocks.updateConfig).not.toHaveBeenCalled()
    await wrapper.get('[data-test="save-config"]').trigger('click')
    await flushPromises()
    expect(mocks.updateConfig).toHaveBeenLastCalledWith(expect.objectContaining({ user_allowlist_ids: [7, 42], group_ids: [3], local_rules: localConfig.local_rules, audit_instructions: 'custom notes' }))
    policy.vm.$emit('update:draft', { ...policy.props('draft'), user_allowlist_ids: [] })
    await flushPromises()
    await wrapper.get('[data-test="save-config"]').trigger('click')
    await flushPromises()
    expect(mocks.updateConfig).toHaveBeenLastCalledWith(expect.objectContaining({ user_allowlist_ids: [], group_ids: [3], local_rules: localConfig.local_rules }))
    expect(mocks.probeEndpoint).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('loads instruction text separately with confirmation while preserving custom rules and audit settings', async () => {
    const customRules: PromptAuditConfig['local_rules'] = [{ id: 'custom', name: 'Custom', kind: 'keyword', pattern: 'custom phrase', category: 'jailbreak', action: 'warn', enabled: false, case_sensitive: true }]
    mocks.getConfig.mockResolvedValue({ ...baseConfig(), engine: 'remote', enabled: false, group_ids: [8], local_rules: customRules, audit_instructions: 'Custom instructions', default_audit_instructions: 'Detailed default instructions' })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="tab-config"]').trigger('click')
    const draft = () => wrapper.getComponent(LocalRulePanel).props('draft') as PromptAuditDraft
    await wrapper.get('[data-test="local-instruction-template"]').trigger('click')
    expect(draft().audit_instructions).toBe('Custom instructions')
    await wrapper.get('[data-test="cancel-action"]').trigger('click')
    expect(draft().audit_instructions).toBe('Custom instructions')
    await wrapper.get('[data-test="local-instruction-template"]').trigger('click')
    await wrapper.get('[data-test="confirm-action"]').trigger('click')
    expect(draft()).toMatchObject({ engine: 'remote', enabled: false, group_ids: [8], local_rules: customRules, audit_instructions: 'Detailed default instructions' })
    expect(mocks.updateConfig).not.toHaveBeenCalled()
    expect(mocks.probeEndpoint).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('loads empty instructions without confirmation and replaces rules only after confirmation', async () => {
    const customRules: PromptAuditConfig['local_rules'] = [{ id: 'custom', name: 'Custom', kind: 'keyword', pattern: 'custom phrase', category: 'jailbreak', action: 'warn', enabled: false, case_sensitive: true }]
    const defaultRules: PromptAuditConfig['local_rules'] = [{ ...customRules[0], id: 'template', pattern: 'default phrase' }]
    mocks.getConfig.mockResolvedValue({ ...baseConfig(), engine: 'local_rules', enabled: false, local_rules: customRules, default_local_rules: defaultRules, audit_instructions: '', default_audit_instructions: 'Detailed default instructions' })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="tab-config"]').trigger('click')
    const draft = () => wrapper.getComponent(LocalRulePanel).props('draft') as PromptAuditDraft
    await wrapper.get('[data-test="local-instruction-template"]').trigger('click')
    expect(wrapper.find('[data-test="confirm"]').exists()).toBe(false)
    expect(draft().audit_instructions).toBe('Detailed default instructions')
    await wrapper.get('[data-test="local-rule-templates"]').trigger('click')
    expect(draft().local_rules).toEqual(customRules)
    await wrapper.get('[data-test="confirm-action"]').trigger('click')
    expect(draft()).toMatchObject({ enabled: false, local_rules: defaultRules, audit_instructions: 'Detailed default instructions' })
    expect(mocks.updateConfig).not.toHaveBeenCalled()
    expect(mocks.probeEndpoint).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('preserves unsaved and stored endpoint secrets when switching local and remote modes', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="tab-config"]').trigger('click')
    await wrapper.get('[data-test="inject-secret"]').trigger('click')
    await wrapper.get('input[value="local_rules"]').setValue()
    expect(wrapper.find('[data-test="endpoint"]').exists()).toBe(false)
    const localDraft = wrapper.getComponent(LocalRulePanel).props('draft') as PromptAuditDraft
    expect(localDraft.endpoints[0]).toMatchObject({ token: 'PROMPT_AUDIT_CANARY_SECRET_DO_NOT_PERSIST', has_token: true, clear_token: false })
    await wrapper.get('input[value="remote"]').setValue()
    expect(wrapper.getComponent(EndpointStub).props('endpoints')[0]).toMatchObject({ token: 'PROMPT_AUDIT_CANARY_SECRET_DO_NOT_PERSIST', has_token: true, clear_token: false })
    expect(mocks.updateConfig).not.toHaveBeenCalled()
    expect(mocks.probeEndpoint).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('keeps a saved disabled remote configuration remote even without endpoints', async () => {
    mocks.getConfig.mockResolvedValue({ ...baseConfig(), engine: 'remote', enabled: false, endpoints: [] })
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.getComponent(LocalRulePanel).props('draft').engine).toBe('remote')
    expect(wrapper.find('[data-test="endpoint"]').exists()).toBe(true)
    expect(mocks.probeEndpoint).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('keeps only prompt events/config in the page and delegates security navigation', async () => {
    const wrapper = mountView(); await flushPromises()
    expect(wrapper.find('[data-test="tab-ip"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="ip-audit-panel"]').exists()).toBe(false)
    expect(wrapper.getComponent({ name: 'SecurityAuditNav' }).attributes('active')).toBe('/admin/prompt-audit/operations')
    expect(wrapper.findAll('[role="tab"]')).toHaveLength(2)
    wrapper.unmount()
  })

  it('keeps configuration deep links on the prompt page without loading IP audit', async () => {
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/admin/prompt-audit', component: PromptAuditView }] })
    await router.push('/admin/prompt-audit?tab=config'); await router.isReady()
    const wrapper = mountView({}, router); await flushPromises()
    expect(wrapper.get('[data-test="tab-config"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.find('[data-testid="ip-audit-panel"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="save-config"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('starts config, runtime, groups, and events loads independently', async () => {
    mocks.getRuntime.mockRejectedValue(new Error('runtime offline'))
    const wrapper = mountView()
    expect(mocks.getConfig).toHaveBeenCalledOnce()
    expect(mocks.getRuntime).toHaveBeenCalledOnce()
    expect(mocks.listGroups).toHaveBeenCalledOnce()
    expect(mocks.listEvents).toHaveBeenCalledOnce()
    await flushPromises()
    expect(wrapper.get('[data-test="runtime"]').text()).toContain('runtime offline')
    expect(wrapper.find('[data-test="endpoint"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="events"]').exists()).toBe(true)
  })

  it('separates configuration and audit events into page tabs', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-test="tab-events"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.get('[data-test="tab-config"]').attributes('aria-selected')).toBe('false')
    expect(wrapper.get('[data-test="tab-panel-events"]').attributes('style') || '').not.toContain('display: none')
    expect(wrapper.get('[data-test="tab-panel-config"]').attributes('style') || '').toContain('display: none')
    expect(wrapper.find('[data-test="save-config"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="events"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="pass-events-disabled-notice"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="tab-events"]').text()).toContain('admin.promptAudit.tabs.events')
    expect(wrapper.get('[data-test="tab-config"]').text()).toContain('admin.promptAudit.tabs.config')

    await wrapper.get('[data-test="tab-config"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="tab-config"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.get('[data-test="tab-panel-config"]').attributes('style') || '').not.toContain('display: none')
    expect(wrapper.get('[data-test="tab-panel-events"]').attributes('style') || '').toContain('display: none')
    expect(wrapper.find('[data-test="save-config"]').exists()).toBe(true)

    await wrapper.get('[data-test="tab-events"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="tab-events"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.find('[data-test="save-config"]').exists()).toBe(false)

    await wrapper.get('[data-test="pass-events-disabled-notice"] button').trigger('click')
    expect(wrapper.get('[data-test="tab-config"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.find('[data-test="save-config"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="tab-panel-config"]').attributes('style') || '').not.toContain('display: none')
  })

  it('requires confirmation for blocking and disables it when audit is turned off', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="tab-config"]').trigger('click')
    await wrapper.get('[data-test="blocking-toggle"]').trigger('click')
    expect(wrapper.find('[data-test="confirm"]').exists()).toBe(true)
    await wrapper.get('[data-test="confirm-action"]').trigger('click')
    expect(wrapper.get('[data-test="blocking-toggle"]').attributes('aria-checked')).toBe('true')
    await wrapper.get('[data-test="blocking-latest-turn-only-toggle"]').trigger('click')
    expect(wrapper.get('[data-test="blocking-latest-turn-only-toggle"]').attributes('aria-checked')).toBe('true')
    await wrapper.get('[data-test="enabled-toggle"]').trigger('click')
    expect(wrapper.get('[data-test="enabled-toggle"]').attributes('aria-checked')).toBe('false')
    expect(wrapper.get('[data-test="blocking-toggle"]').attributes('aria-checked')).toBe('false')
    expect(wrapper.get('[data-test="blocking-toggle"]').attributes()).toHaveProperty('disabled')
    expect(wrapper.get('[data-test="blocking-latest-turn-only-toggle"]').attributes()).toHaveProperty('disabled')
  })

  it('clears plaintext token state after a successful save', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="tab-config"]').trigger('click')
    await wrapper.get('[data-test="inject-secret"]').trigger('click')
    expect(wrapper.text()).toContain('admin.promptAudit.saveBar.dirty')
    await wrapper.get('[data-test="save-config"]').trigger('click')
    await flushPromises()
    expect(mocks.updateConfig).toHaveBeenCalledWith(expect.objectContaining({ endpoints: [expect.objectContaining({ token: 'PROMPT_AUDIT_CANARY_SECRET_DO_NOT_PERSIST' })] }))
    const endpointProps = wrapper.getComponent(EndpointStub).props('endpoints') as Array<{ token: string }>
    expect(endpointProps[0].token).toBe('')
    expect(wrapper.html()).not.toContain('PROMPT_AUDIT_CANARY_SECRET_DO_NOT_PERSIST')
  })

  it('reports real probe progress/results and invalidates filter confirmation when filters change', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="tab-config"]').trigger('click')
    await wrapper.get('[data-test="probe"]').trigger('click')
    await flushPromises()
    expect(mocks.probeEndpoint).toHaveBeenCalledOnce()
    expect((wrapper.getComponent(EndpointStub).props('probeResults') as Record<string, unknown>)).toHaveProperty('guard-1')

    await wrapper.get('[data-test="tab-events"]').trigger('click')
    await wrapper.get('[data-test="preview"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="filter-delete-dialog"]').exists()).toBe(true)
    expect(mocks.previewDelete).not.toHaveBeenCalled()
    await wrapper.get('[data-test="dialog-preview"]').trigger('click')
    await flushPromises()
    expect(mocks.previewDelete).toHaveBeenCalledOnce()
    expect(wrapper.get('[data-test="dialog-preview-state"]').text()).toBe('2')
    await wrapper.get('[data-test="change-filter"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="filter-delete-dialog"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="dialog-preview-state"]').text()).toBe('none')
  })

  it('uses native labeled switches and a responsive fixed save surface', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="tab-config"]').trigger('click')
    const switches = wrapper.findAll('[data-test$="-toggle"][role="switch"]')
    expect(switches).toHaveLength(4)
    expect(switches.every((item) => Boolean(item.attributes('aria-label')))).toBe(true)
    expect(wrapper.html()).toContain('fixed inset-x-0 bottom-0')
    expect(wrapper.html()).toContain('flex-wrap')
  })

  it('executes single, selected-batch, and preview-confirmed filter deletion flows', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-test="delete-one"]').trigger('click')
    await wrapper.get('[data-test="confirm-action"]').trigger('click')
    await flushPromises()
    expect(mocks.deleteEvent).toHaveBeenCalledWith(5)

    await wrapper.get('[data-test="select-batch"]').trigger('click')
    await wrapper.get('[data-test="delete-batch"]').trigger('click')
    await wrapper.get('[data-test="confirm-action"]').trigger('click')
    await flushPromises()
    expect(mocks.batchDeleteEvents).toHaveBeenCalledWith([5, 6])

    await wrapper.get('[data-test="preview"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="dialog-preview"]').trigger('click')
    await flushPromises()
    expect(mocks.previewDelete).toHaveBeenCalledWith(expect.objectContaining({ start_at: '2026-07-15T00:00', end_at: '2026-07-16T00:00' }))
    await wrapper.get('[data-test="dialog-confirm"]').trigger('click')
    await flushPromises()
    expect(mocks.deleteEventsByFilter).toHaveBeenCalledWith(expect.objectContaining({
      start_at: '2026-07-15T00:00',
      end_at: '2026-07-16T00:00',
    }), expect.objectContaining({
      snapshot_max_id: 10,
      confirmation_token: 'opaque-confirmation',
    }))
    expect(wrapper.find('[data-test="filter-delete-dialog"]').exists()).toBe(false)
  })

  it('mints the confirmation token on the fly for one-click filter deletion without a manual preview', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-test="preview"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="filter-delete-dialog"]').exists()).toBe(true)
    expect(mocks.previewDelete).not.toHaveBeenCalled()

    await wrapper.get('[data-test="dialog-confirm"]').trigger('click')
    await flushPromises()
    expect(mocks.previewDelete).toHaveBeenCalledOnce()
    expect(mocks.previewDelete).toHaveBeenCalledWith(expect.objectContaining({ start_at: '2026-07-15T00:00', end_at: '2026-07-16T00:00' }))
    expect(mocks.deleteEventsByFilter).toHaveBeenCalledWith(expect.objectContaining({
      start_at: '2026-07-15T00:00',
      end_at: '2026-07-16T00:00',
    }), expect.objectContaining({
      snapshot_max_id: 10,
      confirmation_token: 'opaque-confirmation',
    }))
    expect(wrapper.find('[data-test="filter-delete-dialog"]').exists()).toBe(false)
  })
})
