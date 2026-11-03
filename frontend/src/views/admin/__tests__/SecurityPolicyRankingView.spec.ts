import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent } from 'vue'
import SecurityPolicyRankingView from '../SecurityPolicyRankingView.vue'

const mocks = vi.hoisted(() => ({ getRanking: vi.fn(), getSettings: vi.fn(), getRecords: vi.fn(), preview: vi.fn(), deduct: vi.fn() }))
vi.mock('@/api/admin/securityPolicyRanking', () => ({ securityPolicyRankingAPI: mocks }))
vi.mock('@/api/admin/groups', () => ({ default: { getAllIncludingInactive: vi.fn().mockResolvedValue([]) } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: vi.fn(), showError: vi.fn() }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isAdmin: true }) }))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => `${key}${params ? ` ${JSON.stringify(params)}` : ''}` }) }
})
const DialogStub = defineComponent({ props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' })

describe('audit charge records in the ranking', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.getSettings.mockResolvedValue({ data: { multiplier: '6.7', auto_disable_enabled: false, auto_disable_threshold: 10, reset_on_manual_enable: true, group_ids: [], group_rules: [], user_allowlist_ids: [] } })
    mocks.getRanking.mockResolvedValue({ data: { items: [{ rank: 1, user_id: 8, display_name: 'user', trigger_count: 1, pending_count: 0 }], total: 1, total_triggers: 1, start_date: '2026-11-03', end_date: '2026-11-03', timezone: 'UTC' } })
  })

  it('shows the linked completed usage and actual charge while keeping audit evidence out of manual deductions', async () => {
    mocks.getRecords.mockResolvedValue({ data: { items: [{ usage_id: 0, audit_id: 42, source: 'combined_audit', linked_usage_id: 77, policy_action: 'charge_allow', risk_score: 60, risk_level: 'high', occurred_at: '2026-11-03T00:00:00Z', group_id: 9, group_name: 'Main', request_id: 'r1', model: 'model', count_status: 'skipped', count_reason: 'auto_disable_off', auto_charge_status: 'deducted', actual_cost: '0.2', audit_multiplier: '2', auto_charge_amount: '0.4' }], total: 1, page: 1, page_size: 20, pages: 1 } })
    const wrapper = mount(SecurityPolicyRankingView, { global: { stubs: { AppLayout: { template: '<div><slot /></div>' }, BaseDialog: DialogStub, DateRangePicker: true, Pagination: true, UserAllowlistSelector: true } } })
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
})
