import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { mount } from '@vue/test-utils'
import EndpointPool from '../components/EndpointPool.vue'
import PolicyPanel from '../components/PolicyPanel.vue'
import LocalRulePanel from '../components/LocalRulePanel.vue'
import UserAllowlistSelector from '@/components/admin/user/UserAllowlistSelector.vue'
import EventWorkspace from '../components/EventWorkspace.vue'
import EventDetailDialog from '../components/EventDetailDialog.vue'
import FilterDeleteDialog from '../components/FilterDeleteDialog.vue'
import type { PromptAuditDraft, PromptAuditEndpointDraft, PromptAuditEvent, PromptEventFilters, PromptLocalRule } from '../types'
import { emptyEventFilters, resolveDeleteRangeFilters, SCANNER_CATALOG } from '../viewModel'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ locale: { value: 'en' }, t: (key: string, params?: Record<string, unknown>) => key.replace(/\{(\w+)\}/g, (_, token) => String(params?.[token] ?? `{${token}}`)) }) }
})

const DialogStub = defineComponent({ props: ['show', 'title'], emits: ['close'], template: '<div v-if="show" data-test="dialog"><slot /><slot name="footer" /></div>' })
const PaginationStub = defineComponent({ props: ['total', 'page', 'pageSize'], emits: ['update:page', 'update:pageSize'], template: '<div data-test="pagination" />' })
const ConfirmStub = defineComponent({ props: ['show', 'title', 'message'], emits: ['confirm', 'cancel'], template: '<div v-if="show" data-test="template-confirm"><button data-test="confirm-templates" @click="$emit(\'confirm\')">confirm</button><button data-test="cancel-templates" @click="$emit(\'cancel\')">cancel</button></div>' })

const endpoint = (): PromptAuditEndpointDraft => ({
  id: 'guard-1', name: 'Guard One', protocol: 'openai_compatible', base_url: 'http://127.0.0.1:8000',
  model: 'guard-model', timeout_ms: 3000, input_limit: 4000, enabled: true,
  has_token: true, token_status: 'configured', token: '', clear_token: false,
})

const localRule = (): PromptLocalRule => ({ id: 'local-1', name: 'Existing rule', enabled: true, kind: 'keyword', pattern: 'existing pattern', category: 'jailbreak', action: 'warn', case_sensitive: false })
const localDraft = (): PromptAuditDraft => ({
  enabled: true, engine: 'local_rules', audit_instructions: 'Existing instructions', local_rules: [localRule()],
  default_audit_instructions: 'Default instructions', default_local_rules: [{ ...localRule(), id: 'default-1', pattern: 'default pattern' }],
  blocking_enabled: false, blocking_latest_turn_only: false, store_pass_events: false, effective_mode: 'async_audit', strategy: 'priority',
  worker_count: 4, queue_capacity: 100, scanners: SCANNER_CATALOG.map((item) => item.id), all_groups: false, group_ids: [],
  endpoints: [endpoint()], config_version: 1, updated_at: '', updated_by: 0, change_summary: '',
})

describe('Prompt Audit components', () => {
  beforeEach(() => vi.restoreAllMocks())

  it('requires confirmation before replacing existing local rules and preserves the separate instructions', async () => {
    const original = localDraft()
    const wrapper = mount(LocalRulePanel, { props: { draft: original }, global: { stubs: { ConfirmDialog: ConfirmStub } } })
    await wrapper.get('[data-test="local-rule-templates"]').trigger('click')
    expect(wrapper.find('[data-test="template-confirm"]').exists()).toBe(true)
    expect(wrapper.emitted('update:draft')).toBeUndefined()
    await wrapper.get('[data-test="cancel-templates"]').trigger('click')
    expect(wrapper.emitted('update:draft')).toBeUndefined()
    await wrapper.get('[data-test="local-rule-templates"]').trigger('click')
    await wrapper.get('[data-test="confirm-templates"]').trigger('click')
    const updated = wrapper.emitted('update:draft')?.at(-1)?.[0] as PromptAuditDraft
    expect(updated.local_rules).toEqual(original.default_local_rules)
    expect(updated.audit_instructions).toBe(original.audit_instructions)
    expect(updated.endpoints).toEqual(original.endpoints)
    expect(original.local_rules[0].pattern).toBe('existing pattern')
    expect(wrapper.find('[data-test="template-confirm"]').exists()).toBe(false)

    await wrapper.setProps({ draft: { ...original, local_rules: [], audit_instructions: 'Keep these instructions' } })
    await wrapper.get('[data-test="local-rule-templates"]').trigger('click')
    expect(wrapper.find('[data-test="template-confirm"]').exists()).toBe(false)
    expect((wrapper.emitted('update:draft')?.at(-1)?.[0] as PromptAuditDraft).audit_instructions).toBe('Keep these instructions')
    wrapper.unmount()
  })

  it('connects the independent prompt user allowlist without replacing other policy fields', async () => {
    const draft = { ...localDraft(), user_allowlist_ids: [42], group_ids: [7] }
    const wrapper = mount(PolicyPanel, { props: { draft, groups: [] }, global: { stubs: { UserAllowlistSelector: true } } })
    const selector = wrapper.getComponent(UserAllowlistSelector)
    expect(selector.props('modelValue')).toEqual([42])
    selector.vm.$emit('update:modelValue', [8, 42])
    expect(wrapper.emitted('update:draft')?.at(-1)?.[0]).toMatchObject({ user_allowlist_ids: [8, 42], group_ids: [7], local_rules: draft.local_rules, endpoints: draft.endpoints })
    selector.vm.$emit('update:modelValue', [])
    expect(wrapper.emitted('update:draft')?.at(-1)?.[0]).toMatchObject({ user_allowlist_ids: [], group_ids: [7] })
    wrapper.unmount()
  })

  it('loads templates into an empty draft without modifying the provided templates', async () => {
    const original = { ...localDraft(), local_rules: [], audit_instructions: '' }
    const wrapper = mount(LocalRulePanel, { props: { draft: original }, global: { stubs: { ConfirmDialog: ConfirmStub } } })
    await wrapper.get('[data-test="local-rule-templates"]').trigger('click')
    expect(wrapper.find('[data-test="template-confirm"]').exists()).toBe(false)
    const updated = wrapper.emitted('update:draft')?.at(-1)?.[0] as PromptAuditDraft
    expect(updated.local_rules).toEqual(original.default_local_rules)
    updated.local_rules[0].pattern = 'changed after emit'
    expect(original.default_local_rules![0].pattern).toBe('default pattern')
    wrapper.unmount()
  })

  it('edits and removes local rules and preserves endpoint credentials across engine switches', async () => {
    let draft = localDraft()
    draft.endpoints[0].token = 'UNSAVED_LOCAL_RULE_TEST_TOKEN'
    const wrapper = mount(LocalRulePanel, { props: { draft }, global: { stubs: { ConfirmDialog: ConfirmStub } } })
    const applyEmitted = async () => {
      draft = wrapper.emitted('update:draft')?.at(-1)?.[0] as PromptAuditDraft
      await wrapper.setProps({ draft })
    }
    await wrapper.get('[aria-label="admin.promptAudit.local.name"]').setValue('Custom regex')
    await applyEmitted()
    await wrapper.get('[aria-label="admin.promptAudit.local.pattern"]').setValue('custom.*pattern')
    await applyEmitted()
    await wrapper.get('[data-test="local-rule-0"] select').setValue('regex')
    await applyEmitted()
    await wrapper.findAll('[data-test="local-rule-0"] select')[2].setValue('block')
    await applyEmitted()
    expect(draft.local_rules[0]).toMatchObject({ name: 'Custom regex', pattern: 'custom.*pattern', kind: 'regex', action: 'block' })
    await wrapper.get('input[value="remote"]').setValue()
    await applyEmitted()
    expect(wrapper.find('[data-test="local-rule-0"]').exists()).toBe(false)
    expect(draft.endpoints[0]).toMatchObject({ token: 'UNSAVED_LOCAL_RULE_TEST_TOKEN', has_token: true, clear_token: false })
    await wrapper.get('input[value="local_rules"]').setValue()
    await applyEmitted()
    expect(draft.local_rules[0].pattern).toBe('custom.*pattern')
    await wrapper.get('[data-test="local-rule-0"] button').trigger('click')
    await applyEmitted()
    expect(draft.local_rules).toEqual([])
    expect(wrapper.text()).toContain('admin.promptAudit.local.empty')
    await wrapper.get('[data-test="local-rule-add"]').trigger('click')
    await applyEmitted()
    expect(draft.local_rules).toHaveLength(1)
    expect(draft.local_rules[0]).toMatchObject({ enabled: true, kind: 'keyword', pattern: '', action: 'warn' })
    wrapper.unmount()
  })

  it('selects only explicit groups and explains that an empty scope skips auditing', async () => {
    const draft = localDraft()
    const wrapper = mount(PolicyPanel, { props: { draft, groups: [
      { id: 1, name: 'Alpha', platform: 'openai', status: 'active' },
      { id: 2, name: 'Beta', platform: 'openai', status: 'active' },
    ] } })
    expect(wrapper.text()).toContain('admin.promptAudit.policy.emptyScope')
    expect(wrapper.findAll('input[type="radio"]')).toHaveLength(0)
    expect(wrapper.findAll('input[type="checkbox"]')).toHaveLength(2)
    await wrapper.findAll('input[type="checkbox"]')[1].setValue(true)
    const updated = wrapper.emitted('update:draft')?.at(-1)?.[0] as PromptAuditDraft
    expect(updated).toMatchObject({ all_groups: false, group_ids: [2], engine: 'local_rules' })
    await wrapper.setProps({ draft: updated })
    expect(wrapper.text()).not.toContain('admin.promptAudit.policy.emptyScope')
    await wrapper.findAll('input[type="checkbox"]')[1].setValue(false)
    const cleared = wrapper.emitted('update:draft')?.at(-1)?.[0] as PromptAuditDraft
    expect(cleared.group_ids).toEqual([])
    wrapper.unmount()
  })

  it('edits a saved endpoint with blank-secret keep, explicit clear, replacement, and probe actions', async () => {
    const wrapper = mount(EndpointPool, {
      props: { endpoints: [endpoint()], probeResults: {}, probingIds: [] },
      global: { stubs: { BaseDialog: DialogStub } },
    })
    expect(wrapper.text()).toContain('admin.promptAudit.pool.configured')
    const edit = wrapper.findAll('button').find((button) => button.text().includes('common.edit'))
    expect(edit).toBeTruthy()
    await edit!.trigger('click')
    const token = wrapper.get<HTMLInputElement>('[aria-label="admin.promptAudit.pool.apiKey"]')
    expect(token.element.value).toBe('')
    expect(token.attributes('placeholder')).toContain('admin.promptAudit.pool.keepSecret')

    await wrapper.get<HTMLInputElement>('[aria-label="admin.promptAudit.pool.clearSecret"]').setValue(true)
    await token.setValue('replacement-canary')
    await wrapper.get('[data-test="save-endpoint"]').trigger('click')
    const updated = wrapper.emitted('update:endpoints')?.at(-1)?.[0] as PromptAuditEndpointDraft[]
    expect(updated[0]).toMatchObject({ token: 'replacement-canary', clear_token: false })

    const probe = wrapper.findAll('button').find((button) => button.text().includes('admin.promptAudit.pool.probe'))
    await probe!.trigger('click')
    expect(wrapper.emitted('probe')?.[0]?.[0]).toMatchObject({ id: 'guard-1' })
  })

  it('surfaces an undecryptable saved credential and prompts for re-entry', async () => {
    const invalidEndpoint = { ...endpoint(), token_status: 'invalid' }
    const wrapper = mount(EndpointPool, {
      props: { endpoints: [invalidEndpoint], probeResults: {}, probingIds: [] },
      global: { stubs: { BaseDialog: DialogStub } },
    })
    expect(wrapper.text()).toContain('admin.promptAudit.pool.invalid')
    expect(wrapper.text()).not.toContain('admin.promptAudit.pool.configured')

    const edit = wrapper.findAll('button').find((button) => button.text().includes('common.edit'))
    await edit!.trigger('click')
    const token = wrapper.get<HTMLInputElement>('[aria-label="admin.promptAudit.pool.apiKey"]')
    expect(token.attributes('placeholder')).toContain('admin.promptAudit.pool.reenterSecret')
  })

  it('supports group search, stale configured groups, nine scanners, and bounded worker inputs', async () => {
    const draft: PromptAuditDraft = {
      engine: 'remote', audit_instructions: '', local_rules: [],
      enabled: true, blocking_enabled: false, blocking_latest_turn_only: false, store_pass_events: false, effective_mode: 'async_audit', strategy: 'priority',
      worker_count: 4, queue_capacity: 100, scanners: SCANNER_CATALOG.map((item) => item.id), all_groups: false, group_ids: [1, 99],
      endpoints: [endpoint()], config_version: 1, updated_at: '', updated_by: 0, change_summary: '',
    }
    const wrapper = mount(PolicyPanel, {
      props: { draft, groups: [{ id: 1, name: 'Alpha', platform: 'openai', status: 'active' }, { id: 2, name: 'Beta', platform: 'claude', status: 'inactive' }] },
    })
    expect(wrapper.text()).toContain('99')
    expect(wrapper.findAll('input[type="checkbox"]').filter((input) => SCANNER_CATALOG.some((scanner) => input.attributes('aria-label') === `admin.promptAudit.scanners.${scanner.id}`))).toHaveLength(9)
    await wrapper.get('[aria-label="admin.promptAudit.policy.searchGroups"]').setValue('Beta')
    expect(wrapper.text()).toContain('Beta')
    expect(wrapper.text()).not.toContain('Alpha')
    await wrapper.get('[aria-label="admin.promptAudit.policy.workerCount"]').setValue('6')
    const emitted = wrapper.emitted('update:draft')?.at(-1)?.[0] as PromptAuditDraft
    expect(emitted.worker_count).toBe(6)
  })

  it('keeps identity fields separate, supports selection, and opens filter deletion from the toolbar', async () => {
    const event: PromptAuditEvent = {
      id: 1, job_id: 1, decision: 'critical', risk_level: 'critical', action: 'Block', categories: ['pii'], matched_scanners: ['pii'], scanner_scores: { pii: 1 }, scanner_evidence: { pii: 'redacted' }, scanner_backend: 'qwen3guard-openai', scanner_version: '1', guard_endpoint_id: 'guard-1', policy_id: 'priority', policy_version: 1, config_version: 1, chunk_total: 1, latency_ms: 10, issue_summaries: [], created_at: '2026-07-16T00:00:00Z',
      snapshot: { request_id: 'req-1', user_id: 1, username: 'alice', user_email: 'alice@example.test', api_key_id: 2, api_key_name: 'alice-key', group_id: 3, group_name: 'Alpha', provider: 'openai', endpoint: '/v1/chat/completions', protocol: 'openai_chat', model: 'gpt-test', prompt_hash: 'a'.repeat(64), redacted_preview: 'redacted preview', full_prompt: 'full prompt text', prompt_length: 10, message_count: 1, stage: 'http' },
    }
    const wrapper = mount(EventWorkspace, {
      props: { events: [event], total: 1, page: 1, pageSize: 20, filters: emptyEventFilters(), selectedIds: [], loading: false, error: '' },
      global: { stubs: { Pagination: PaginationStub } },
    })
    expect(wrapper.text()).toContain('alice')
    expect(wrapper.text()).toContain('alice@example.test')
    expect(wrapper.text()).toContain('alice-key')
    expect(wrapper.text()).toContain('admin.promptAudit.decisions.critical · admin.promptAudit.riskLevels.critical')
    expect(wrapper.text()).toContain('admin.promptAudit.scanners.pii')
    expect(wrapper.get('[data-test="filter-delete"]').attributes()).not.toHaveProperty('disabled')
    await wrapper.get('[data-test="filter-delete"]').trigger('click')
    expect(wrapper.emitted('preview-delete')).toHaveLength(1)
    await wrapper.get('[aria-label="admin.promptAudit.events.selectEvent"]').setValue(true)
    expect(wrapper.emitted('selection')?.at(-1)?.[0]).toEqual([1])
  })

  it('resolves delete range presets to an epoch start and a cutoff end', () => {
    const now = Date.parse('2026-07-17T12:00:00.000Z')
    const sevenDays = resolveDeleteRangeFilters(emptyEventFilters(), '7d', now)
    expect(sevenDays.start_at).toBe('1970-01-01T00:00:00.000Z')
    expect(sevenDays.end_at).toBe('2026-07-10T12:00:00.000Z')
    const all = resolveDeleteRangeFilters(emptyEventFilters(), 'all', now)
    expect(all.start_at).toBe('1970-01-01T00:00:00.000Z')
    expect(all.end_at).toBe('2026-07-17T12:00:00.000Z')
    const customSource = { ...emptyEventFilters(), start_at: '2026-07-01T00:00', end_at: '2026-07-02T00:00' }
    expect(resolveDeleteRangeFilters(customSource, 'custom', now)).toEqual(customSource)
  })

  it('drives filter deletion through presets, custom validation, preview, and confirm', async () => {
    const wrapper = mount(FilterDeleteDialog, {
      props: { show: true, initialFilters: emptyEventFilters(), preview: null, previewing: false, deleting: false },
      global: { stubs: { BaseDialog: DialogStub } },
    })
    expect(wrapper.get<HTMLInputElement>('[data-test="range-preset-7d"]').element.checked).toBe(true)
    expect(wrapper.find('[data-test="custom-range"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="delete-preview-empty"]').exists()).toBeTruthy()
    // A valid preset is enough: confirm is armed immediately (one-click flow)
    // and needs no disabled-reason hint.
    expect(wrapper.get('[data-test="confirm-filter-delete"]').attributes()).not.toHaveProperty('disabled')
    expect(wrapper.find('[data-test="confirm-disabled-reason"]').exists()).toBe(false)
    await wrapper.get('[data-test="confirm-filter-delete"]').trigger('click')
    const directConfirm = wrapper.emitted('confirm')?.at(-1)?.[0] as PromptEventFilters
    expect(directConfirm.start_at).toBe('1970-01-01T00:00:00.000Z')
    expect(Date.now() - new Date(directConfirm.end_at).getTime()).toBeGreaterThanOrEqual(7 * 24 * 60 * 60 * 1000)

    await wrapper.get('[data-test="range-preset-30d"]').setValue()
    expect(wrapper.emitted('criteria-change')?.length).toBeGreaterThan(0)
    await wrapper.get('[data-test="delete-risk"]').setValue('high')
    await wrapper.get('[data-test="run-delete-preview"]').trigger('click')
    const presetPreview = wrapper.emitted('preview')?.at(-1)?.[0] as PromptEventFilters
    expect(presetPreview.risk_level).toBe('high')
    expect(presetPreview.start_at).toBe('1970-01-01T00:00:00.000Z')
    expect(Date.now() - new Date(presetPreview.end_at).getTime()).toBeGreaterThanOrEqual(30 * 24 * 60 * 60 * 1000)

    await wrapper.get('[data-test="range-preset-custom"]').setValue()
    expect(wrapper.find('[data-test="custom-range"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="run-delete-preview"]').attributes()).toHaveProperty('disabled')
    expect(wrapper.get('[data-test="confirm-filter-delete"]').attributes()).toHaveProperty('disabled')
    expect(wrapper.get('[data-test="confirm-disabled-reason"]').text()).toBe('admin.promptAudit.events.filterDeleteConfirmInvalidRange')
    expect(wrapper.get('[data-test="confirm-filter-delete"]').attributes('title')).toBe('admin.promptAudit.events.filterDeleteConfirmInvalidRange')
    await wrapper.get('[data-test="custom-range"] [aria-label="admin.promptAudit.events.startAt"]').setValue('2026-07-01T00:00')
    await wrapper.get('[data-test="custom-range"] [aria-label="admin.promptAudit.events.endAt"]').setValue('2026-07-02T00:00')
    expect(wrapper.get('[data-test="run-delete-preview"]').attributes()).not.toHaveProperty('disabled')
    expect(wrapper.get('[data-test="confirm-filter-delete"]').attributes()).not.toHaveProperty('disabled')
    expect(wrapper.find('[data-test="confirm-disabled-reason"]').exists()).toBe(false)
    await wrapper.get('[data-test="run-delete-preview"]').trigger('click')
    const customPreview = wrapper.emitted('preview')?.at(-1)?.[0] as PromptEventFilters
    expect(customPreview.start_at).toBe('2026-07-01T00:00')
    expect(customPreview.end_at).toBe('2026-07-02T00:00')

    await wrapper.setProps({
      preview: { matched_count: 3, filter_summary: {}, snapshot_max_id: 9, filter_hash: 'b'.repeat(64), confirmation_token: 'tok', expires_at: '2026-07-16T00:05:00Z' },
    })
    expect(wrapper.get('[data-test="delete-preview-result"]').text()).toContain('admin.promptAudit.events.filterDeleteCount')
    expect(wrapper.find('[data-test="confirm-disabled-reason"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="confirm-filter-delete"]').attributes()).not.toHaveProperty('disabled')
    await wrapper.get('[data-test="confirm-filter-delete"]').trigger('click')
    const confirmed = wrapper.emitted('confirm')?.at(-1)?.[0] as PromptEventFilters
    expect(confirmed.start_at).toBe('2026-07-01T00:00')
    expect(confirmed.end_at).toBe('2026-07-02T00:00')
  })

  it('explains that a zero-match preview leaves nothing to delete', async () => {
    const wrapper = mount(FilterDeleteDialog, {
      props: {
        show: true,
        initialFilters: emptyEventFilters(),
        preview: { matched_count: 0, filter_summary: {}, snapshot_max_id: 0, filter_hash: 'c'.repeat(64), confirmation_token: 'tok', expires_at: '2026-07-16T00:05:00Z' },
        previewing: false,
        deleting: false,
      },
      global: { stubs: { BaseDialog: DialogStub } },
    })
    expect(wrapper.get('[data-test="confirm-filter-delete"]').attributes()).toHaveProperty('disabled')
    expect(wrapper.get('[data-test="confirm-disabled-reason"]').text()).toBe('admin.promptAudit.events.filterDeleteConfirmNoMatches')
    await wrapper.setProps({ previewing: true })
    expect(wrapper.find('[data-test="confirm-disabled-reason"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="confirm-filter-delete"]').attributes()).toHaveProperty('disabled')
  })

  it('inherits an explicit list-filter range as the custom preset', async () => {
    const initialFilters = { ...emptyEventFilters(), start_at: '2026-07-01T00:00', end_at: '2026-07-02T00:00', decision: 'critical' }
    const wrapper = mount(FilterDeleteDialog, {
      props: { show: true, initialFilters, preview: null, previewing: false, deleting: false },
      global: { stubs: { BaseDialog: DialogStub } },
    })
    expect(wrapper.get<HTMLInputElement>('[data-test="range-preset-custom"]').element.checked).toBe(true)
    expect(wrapper.get<HTMLInputElement>('[data-test="custom-range"] [aria-label="admin.promptAudit.events.startAt"]').element.value).toBe('2026-07-01T00:00')
    expect(wrapper.get<HTMLSelectElement>('[data-test="delete-decision"]').element.value).toBe('critical')
    expect(wrapper.get('[data-test="run-delete-preview"]').attributes()).not.toHaveProperty('disabled')
  })

  it('shows the full unredacted prompt and structured guard return on the risks tab', async () => {
    const event: PromptAuditEvent = {
      id: 1, job_id: 1, decision: 'critical', risk_level: 'critical', action: 'Block',
      categories: ['sexual_content_or_sexual_acts'], matched_scanners: ['sexual_content_or_sexual_acts'],
      scanner_scores: { sexual_content_or_sexual_acts: 1 },
      scanner_evidence: { sexual_content_or_sexual_acts: 'Sexual Content or Sexual Acts' },
      scanner_backend: 'qwen3guard-openai', scanner_version: 'qwen3guard', guard_endpoint_id: 'guard-1',
      policy_id: 'priority', policy_version: 1, config_version: 1, chunk_total: 1, latency_ms: 12,
      issue_summaries: [{
        category: 'sexual_content_or_sexual_acts', scanner_id: 'sexual_content_or_sexual_acts',
        title: '性内容或性行为', description: 'Sexual content or sexual acts', severity: 'critical',
        severity_label: '严重', action: 'Block', action_label: '阻止',
        code: 'prompt_audit_sexual_content_or_sexual_acts', score: 1,
        evidence: 'Sexual Content or Sexual Acts', evidence_hash: 'abc',
      }],
      created_at: '2026-07-16T00:00:00Z',
      snapshot: {
        request_id: 'req-1', user_id: 1, username: 'alice', user_email: 'alice@example.test',
        api_key_id: 2, api_key_name: 'alice-key', group_id: 3, group_name: 'Alpha', provider: 'openai',
        endpoint: '/v1/chat/completions', protocol: 'openai_chat', model: 'gpt-test',
        prompt_hash: 'a'.repeat(64), redacted_preview: 'redacted prompt body', full_prompt: 'complete unmasked prompt body', prompt_length: 20,
        message_count: 1, stage: 'http',
      },
    }
    const wrapper = mount(EventDetailDialog, {
      props: { show: true, event, loading: false },
      global: { stubs: { BaseDialog: DialogStub } },
    })
    const panel = wrapper.get('[data-test="event-detail-tab-panel"]')
    expect(panel.classes()).toContain('h-[min(62vh,36rem)]')
    expect(panel.classes()).toContain('overflow-y-auto')

    const riskTab = wrapper.findAll('[role="tab"]').find((tab) => tab.text().includes('admin.promptAudit.events.tabs.risks'))
    expect(riskTab).toBeTruthy()
    await riskTab!.trigger('click')
    expect(wrapper.get('[data-test="event-detail-tab-panel"]').classes()).toContain('h-[min(62vh,36rem)]')
    expect(wrapper.get('[data-test="risk-prompt-preview"]').text()).toContain('complete unmasked prompt body')
    expect(wrapper.get('[data-test="risk-prompt-preview"]').text()).not.toContain('redacted prompt body')
    expect(wrapper.get('[data-test="risk-prompt-full"]').classes()).toContain('overflow-auto')
    expect(wrapper.get('[data-test="risk-guard-return"]').text()).toContain('"decision": "admin.promptAudit.decisions.critical"')
    expect(wrapper.get('[data-test="risk-guard-return"]').text()).toContain('admin.promptAudit.scanners.sexual_content_or_sexual_acts')
    expect(wrapper.get('[data-test="risk-issue"]').text()).toContain('admin.promptAudit.scanners.sexual_content_or_sexual_acts')
  })

  it('falls back to the redacted preview for events stored before full prompts were kept', async () => {
    const event: PromptAuditEvent = {
      id: 2, job_id: 2, decision: 'flag', risk_level: 'medium', action: 'Warn',
      categories: ['pii'], matched_scanners: ['pii'], scanner_scores: {}, scanner_evidence: {},
      scanner_backend: 'qwen3guard-openai', scanner_version: '1', guard_endpoint_id: 'guard-1',
      policy_id: 'priority', policy_version: 1, config_version: 1, chunk_total: 1, latency_ms: 5,
      issue_summaries: [], created_at: '2026-07-16T00:00:00Z',
      snapshot: {
        request_id: 'req-2', user_id: 1, username: 'bob', user_email: '', api_key_id: 2,
        api_key_name: 'bob-key', group_id: 3, group_name: 'Alpha', provider: 'openai',
        endpoint: '/v1/chat/completions', protocol: 'openai_chat', model: 'gpt-test',
        prompt_hash: 'b'.repeat(64), redacted_preview: 'legacy redacted preview', full_prompt: '', prompt_length: 20,
        message_count: 1, stage: 'http',
      },
    }
    const wrapper = mount(EventDetailDialog, {
      props: { show: true, event, loading: false },
      global: { stubs: { BaseDialog: DialogStub } },
    })
    const riskTab = wrapper.findAll('[role="tab"]').find((tab) => tab.text().includes('admin.promptAudit.events.tabs.risks'))
    await riskTab!.trigger('click')
    expect(wrapper.get('[data-test="risk-prompt-full"]').text()).toContain('legacy redacted preview')
    expect(wrapper.text()).toContain('admin.promptAudit.riskPolicy.legacyEvent')
    expect(wrapper.find('[data-test="prompt-event-risk-score"]').exists()).toBe(false)
    await wrapper.setProps({ event: { ...event, risk_score: 0, policy_action: 'allow' } })
    expect(wrapper.get('[data-test="prompt-event-risk-score"]').text()).toBe('0')
    expect(wrapper.get('[data-test="prompt-event-policy-action"]').text()).toBe('admin.promptAudit.riskPolicy.actions.allow')
    expect(wrapper.text()).toContain('admin.promptAudit.riskPolicy.recordHint')
    await wrapper.setProps({ event: { ...event, risk_score: 70, policy_action: 'charge_allow' } })
    expect(wrapper.get('[data-test="prompt-event-policy-action"]').text()).toBe('admin.promptAudit.riskPolicy.actions.charge_allow')
    wrapper.unmount()
  })
})
