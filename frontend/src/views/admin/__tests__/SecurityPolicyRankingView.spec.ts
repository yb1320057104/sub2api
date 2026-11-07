import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent } from 'vue'
import SecurityPolicyRankingView from '../SecurityPolicyRankingView.vue'
import AuditCenterView from '../AuditCenterView.vue'
import type { AuditCenterConfig } from '@/api/admin/auditCenter'

const mocks = vi.hoisted(() => ({ getRanking: vi.fn(), getSettings: vi.fn(), getRecords: vi.fn(), preview: vi.fn(), deduct: vi.fn(), centerGet: vi.fn(), centerUpdate: vi.fn(), showError: vi.fn() }))
vi.mock('@/api/admin/securityPolicyRanking', () => ({ securityPolicyRankingAPI: mocks }))
vi.mock('@/api/admin/auditCenter', () => ({ auditCenterAPI: { getConfig: mocks.centerGet, updateConfig: mocks.centerUpdate } }))
vi.mock('@/features/prompt-audit/api', () => ({ default: { listGroups: vi.fn().mockResolvedValue([]) } }))
vi.mock('vue-router', () => ({ useRoute: () => ({ query: {} }) }))
vi.mock('@/api/admin/groups', () => ({ default: { getAllIncludingInactive: vi.fn().mockResolvedValue([]) } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: vi.fn(), showError: mocks.showError }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isAdmin: true }) }))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => `${key}${params ? ` ${JSON.stringify(params)}` : ''}`, te: () => true }) }
})
const DialogStub = defineComponent({ props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' })

describe('audit charge records in the ranking', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.getSettings.mockResolvedValue({ data: { multiplier: '6.7', auto_disable_enabled: false, auto_disable_threshold: 10, reset_on_manual_enable: true, group_ids: [], group_rules: [], user_allowlist_ids: [] } })
    mocks.getRanking.mockResolvedValue({ data: { items: [{ rank: 1, user_id: 8, display_name: 'user', trigger_count: 1, pending_count: 0 }], total: 1, total_triggers: 1, start_date: '2026-11-03', end_date: '2026-11-03', timezone: 'UTC' } })
  })

  it('shows actual automatic charges and groups while observations and disabled group charging hide the multiplier', async () => {
    mocks.getRanking.mockResolvedValue({ data: { items: [{ rank: 1, user_id: 8, trigger_count: 2, pending_count: 0, auto_charged_count: 1, auto_charged_amount: '0.4', trigger_groups: 'Group A', observation_count: 1 }], total: 1, total_triggers: 2 } })
    mocks.getRecords.mockResolvedValue({ data: { items: [
      { usage_id: 0, audit_id: 1, source: 'prompt_observation', policy_action: 'allow', occurred_at: '2026-11-03T00:00:00Z', group_id: 9, group_name: 'Group A', count_status: 'skipped', count_reason: 'observation_only', auto_charge_status: 'skipped', auto_charge_reason: 'observation_only' },
      { usage_id: 0, audit_id: 2, source: 'content_audit', policy_action: 'charge_allow', occurred_at: '2026-11-03T00:00:00Z', group_id: 9, group_name: 'Group A', count_status: 'skipped', count_reason: 'auto_disable_off', auto_charge_status: 'skipped', auto_charge_reason: 'auto_deduct_off', audit_multiplier: '9', actual_cost: '0.2', auto_charge_amount: '1.8' },
    ], total: 2, page: 1, page_size: 20, pages: 1 } })
    const wrapper = mount(SecurityPolicyRankingView, { global: { stubs: { RouterLink: true, AppLayout: { template: '<div><slot /></div>' }, BaseDialog: DialogStub, DateRangePicker: true, Pagination: true, UserAllowlistSelector: true } } })
    await flushPromises()
    expect(wrapper.get('[data-testid="ranking-auto-charge-summary"]').text()).toContain('0.4')
    expect(wrapper.get('[data-testid="ranking-group-summary"]').text()).toBe('Group A')
    await wrapper.get('[data-testid="ranking-records-8"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('securityPolicyRanking.observationHint')
    expect(wrapper.find('[data-testid="ranking-record-charge-formula"]').exists()).toBe(false)
    expect(mocks.deduct).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('shows the linked completed usage and actual charge while keeping audit evidence out of manual deductions', async () => {
    mocks.getRecords.mockResolvedValue({ data: { items: [{ usage_id: 0, audit_id: 42, source: 'combined_audit', linked_usage_id: 77, policy_action: 'charge_allow', risk_score: 60, risk_level: 'high', occurred_at: '2026-11-03T00:00:00Z', group_id: 9, group_name: 'Main', request_id: 'r1', model: 'model', count_status: 'skipped', count_reason: 'auto_disable_off', auto_charge_status: 'deducted', actual_cost: '0.2', audit_multiplier: '2', auto_charge_amount: '0.4' }], total: 1, page: 1, page_size: 20, pages: 1 } })
    const wrapper = mount(SecurityPolicyRankingView, { global: { stubs: { RouterLink: true, AppLayout: { template: '<div><slot /></div>' }, BaseDialog: DialogStub, DateRangePicker: true, Pagination: true, UserAllowlistSelector: true } } })
    await flushPromises()
    expect(wrapper.get('[data-testid="ranking-deduct-8"]').attributes()).toHaveProperty('disabled')
    await wrapper.get('[data-testid="ranking-records-8"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="ranking-linked-usage"]').text()).toContain('77')
    expect(wrapper.get('[data-testid="ranking-policy-action"]').text()).toContain('charge_allow')
    expect(wrapper.get('[data-testid="ranking-policy-action"]').text()).toContain('60 / 100')
    expect(wrapper.get('[data-testid="ranking-records-table"]').text()).toContain('securityPolicyRanking.auditChargeNotManual')
    expect(wrapper.get('[data-testid="ranking-records-table"]').text()).not.toContain('securityPolicyRanking.auditNotDeductible')
    expect(wrapper.get('[data-testid="ranking-record-charge-deducted"]').text()).toContain('0.4')
    expect(mocks.preview).not.toHaveBeenCalled()
    expect(mocks.deduct).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it.each([
    { name: 'new zero-cost type4 minimum', source: 'upstream_policy', status: 'deducted', rule: 'max_cost_multiplier_minimum', minimum: '6.70000000', amount: '6.70000000', formula: 'chargeMinimumFormula', deducted: '6.7' },
    { name: 'insufficient balance does not claim the calculated minimum was paid', source: 'upstream_policy', status: 'insufficient_balance', rule: 'max_cost_multiplier_minimum', minimum: '6.70000000', amount: '6.70000000', formula: 'chargeMinimumFormula', deducted: '0' },
    { name: 'historical zero-cost result does not adopt the current minimum', source: 'upstream_policy', status: 'zero_cost', rule: null, minimum: null, amount: '0.00000000', formula: 'chargeFormula', deducted: '0' },
    { name: 'local zero-cost audit retains its original formula', source: 'content_audit', status: 'zero_cost', rule: 'cost_multiplier', minimum: null, amount: '0.00000000', formula: 'chargeFormula', deducted: '0' },
    { name: 'missing minimum snapshot cannot be inferred', source: 'upstream_policy', status: 'deducted', rule: 'max_cost_multiplier_minimum', minimum: null, amount: '6.70000000', formula: 'chargeFormulaUnknown', deducted: '6.7' },
  ])('shows event evidence: $name', async ({ source, status, rule, minimum, amount, formula, deducted }) => {
    mocks.getSettings.mockResolvedValue({ data: { multiplier: '20', auto_disable_enabled: false, auto_disable_threshold: 10, reset_on_manual_enable: true, group_ids: [], group_rules: [], user_allowlist_ids: [] } })
    mocks.getRecords.mockResolvedValue({ data: { items: [{ usage_id: 91, source, occurred_at: '2026-11-07T00:00:00Z', group_id: 9, count_status: 'skipped', count_reason: 'auto_disable_off', auto_charge_status: status, actual_cost: '0.00000000', audit_multiplier: '2.00000000', auto_charge_amount: amount, auto_charge_source: source === 'upstream_policy' ? 'upstream_type4' : 'audit_block', auto_charge_rule: rule, auto_charge_minimum: minimum }], total: 1, page: 1, page_size: 20, pages: 1 } })
    const wrapper = mount(SecurityPolicyRankingView, { global: { stubs: { RouterLink: true, AppLayout: { template: '<div><slot /></div>' }, BaseDialog: DialogStub, DateRangePicker: true, Pagination: true, UserAllowlistSelector: true } } })
    await flushPromises()
    await wrapper.get('[data-testid="ranking-records-8"]').trigger('click'); await flushPromises()
    const text = wrapper.get('[data-testid="ranking-record-charge-formula"]').text()
    expect(text).toContain(`securityPolicyRanking.${formula}`)
    expect(text).not.toContain('"minimum":"20"')
    if (minimum) expect(text).toContain('"minimum":"6.7"')
    else expect(text).not.toContain('"minimum":')
    expect(wrapper.get('[data-testid="ranking-record-charge-deducted"]').text()).toContain(`"amount":"${deducted}"`)
    expect(mocks.preview).not.toHaveBeenCalled(); expect(mocks.deduct).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})

describe('audit center upstream policy minimum', () => {
  function centerConfig(): AuditCenterConfig {
    return {
      revision: 'revision-minimum', risk_control_enabled: true, differences: [], groups: [], charge_summary: [],
      content: { enabled: false, mode: 'off', engine: 'openai', group_ids: [], user_allowlist_ids: [], blocked_keywords: [], risk_policy: null },
      prompt: { enabled: false, blocking_enabled: false, engine: 'local_rules', group_ids: [], user_allowlist_ids: [], local_rules: [], audit_instructions: '', endpoints: [], scanners: [], risk_policy: null },
      ranking: { multiplier: '6.7', auto_disable_enabled: false, auto_disable_threshold: 10, reset_on_manual_enable: true, group_ids: [3], group_rules: [{ group_id: 3, multiplier: '2', auto_deduct_enabled: true }], user_allowlist_ids: [] },
    } as AuditCenterConfig
  }
  function center() {
    return mount(AuditCenterView, { global: { stubs: { RouterLink: true, AppLayout: { template: '<div><slot /></div>' }, RiskControlView: true, LocalRulePanel: true, AuditRiskPolicyPanel: true, UserAllowlistSelector: true, EndpointPool: true, PolicyPanel: true, SecurityPolicyRankingView: true, PromptAuditView: true } } })
  }
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.centerGet.mockResolvedValue(centerConfig())
    mocks.centerUpdate.mockResolvedValue({ ...centerConfig(), revision: 'saved' })
  })
  it('shows the existing amount and saves it atomically without adding a second setting', async () => {
    const wrapper = center(); await flushPromises()
    expect((wrapper.get('[data-test="audit-upstream-minimum-input"]').element as HTMLInputElement).value).toBe('6.7')
    expect(wrapper.get('[data-test="audit-upstream-minimum"]').text()).toContain('securityPolicyRanking.multiplier')
    expect(mocks.centerUpdate).not.toHaveBeenCalled()
    await wrapper.get('[data-test="audit-upstream-minimum-input"]').setValue('8.123456')
    await wrapper.get('[data-test="audit-save-all"]').trigger('click'); await flushPromises()
    expect(mocks.centerUpdate).toHaveBeenCalledWith({ expected_revision: 'revision-minimum', ranking: { ...centerConfig().ranking, multiplier: '8.123456' } })
    wrapper.unmount()
  })
  it.each(['0', '-1', '1e2', '1000.000001', '0.0000001'])('rejects invalid minimum %s without submitting financial settings', async amount => {
    const wrapper = center(); await flushPromises()
    await wrapper.get('[data-test="audit-upstream-minimum-input"]').setValue(amount)
    await wrapper.get('[data-test="audit-save-all"]').trigger('click'); await flushPromises()
    expect(mocks.centerUpdate).not.toHaveBeenCalled()
    expect(mocks.showError).toHaveBeenCalledWith('securityPolicyRanking.invalidMultiplier')
    wrapper.unmount()
  })
})
