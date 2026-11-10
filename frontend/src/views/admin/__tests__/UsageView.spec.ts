import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, reactive, ref } from 'vue'

import UsageView from '../UsageView.vue'
import AuditCenterView from '../AuditCenterView.vue'
import UserAllowlistSelector from '@/components/admin/user/UserAllowlistSelector.vue'
import type { AuditCenterConfig, AuditCenterUpdate } from '@/api/admin/auditCenter'
import type { SecurityPolicyRankingSettings } from '@/types/securityPolicyRanking'
import AdminDailyUsage from '@/components/admin/usage/AdminDailyUsage.vue'
import SecurityPolicyRankingView from '../SecurityPolicyRankingView.vue'
import zhRanking from '@/i18n/locales/zh/securityPolicyRanking'

const auditCenterMocks = vi.hoisted(() => ({ getConfig: vi.fn(), updateConfig: vi.fn(), listGroups: vi.fn(), showError: vi.fn(), showSuccess: vi.fn() }))
vi.mock('@/api/admin/auditCenter', () => ({ auditCenterAPI: auditCenterMocks }))
vi.mock('@/features/prompt-audit/api', () => ({ default: auditCenterMocks }))

const rankingMocks = vi.hoisted(() => ({
  getRanking: vi.fn(), getPublicRanking: vi.fn(), getSettings: vi.fn(),
  saveSettings: vi.fn(), preview: vi.fn(), deduct: vi.fn(),
  getRecords: vi.fn(), getGroups: vi.fn(),
  auth: { isAdmin: true, user: { id: 41 } },
}))
vi.mock('@/api/admin/securityPolicyRanking', () => ({ securityPolicyRankingAPI: rankingMocks }))
const rankingAuth = reactive(rankingMocks.auth)
vi.mock('@/stores/auth', () => ({ useAuthStore: () => rankingAuth }))
vi.mock('@/api/admin/groups', () => ({ default: { getAllIncludingInactive: rankingMocks.getGroups } }))
import DateRangePicker from '@/components/common/DateRangePicker.vue'
import UsageStatsCards from '@/components/admin/usage/UsageStatsCards.vue'
import UsageCleanupDialog from '@/components/admin/usage/UsageCleanupDialog.vue'
import { usagePerMinute } from '@/utils/usageRate'
import { formatDateLocalInput as formatLocalDate } from '@/utils/format'

const { list, exportList, getStats, getSnapshotV2, getById, getModelStats, listErrorLogs, routeQuery, aoaToSheet, sheetAddAoa, saveAs, xlsxWrite, listCleanupTasks, createCleanupTask, showWarning } = vi.hoisted(() => {
  vi.stubGlobal('localStorage', {
    getItem: vi.fn(() => null),
    setItem: vi.fn(),
    removeItem: vi.fn(),
  })

  return {
    listCleanupTasks: vi.fn(),
    createCleanupTask: vi.fn(),
    showWarning: vi.fn(),
    list: vi.fn(),
		exportList: vi.fn(),
    getStats: vi.fn(),
    getSnapshotV2: vi.fn(),
    getById: vi.fn(),
    getModelStats: vi.fn(),
    listErrorLogs: vi.fn(),
    routeQuery: {} as Record<string, string>,
		aoaToSheet: vi.fn(() => ({})),
		sheetAddAoa: vi.fn(),
		saveAs: vi.fn(),
		xlsxWrite: vi.fn(() => new Uint8Array([1, 2, 3])),
  }
})

const own = vi.hoisted(() => ({
  list: vi.fn(), getStats: vi.fn(), getModelStats: vi.fn(), getSnapshotV2: vi.fn(), listErrors: vi.fn(),
  settings: { allow_user_view_error_requests: true },
}))
vi.mock('@/api/observerUsage', () => ({ observerUsageAPI: own }))

const messages: Record<string, string> = {
  'admin.dashboard.timeRange': 'Time Range',
  'admin.dashboard.day': 'Day',
  'admin.dashboard.hour': 'Hour',
  'admin.usage.failedToLoadUser': 'Failed to load user',
	'admin.usage.requestId': 'Request ID',
	'admin.usage.upstreamRequestId': 'Upstream ID',
	'latencyObservability.reasoning_first_ms': 'First reasoning event',
	'latencyObservability.visible_text_first_ms': 'First visible text',
	'latencyObservability.response_byte_first_ms': 'First response byte',
	'usage.requestedModel': 'Requested model',
	'usage.sentUpstreamModel': 'Sent upstream model',
	'usage.upstreamResponseModel': 'Upstream response model',
	'usage.upstreamModelMismatch': 'Upstream model mismatch',
	'common.yes': 'Yes',
	'common.no': 'No',
  'securityPolicyRanking.previewFormula': '{count} triggers × {multiplier} = {amount}',
  'securityPolicyRanking.countSummary': 'counted {counted} / skipped {skipped} / unknown {unknown}',
  'securityPolicyRanking.chargeFormula': 'Calculated {cost} × {multiplier} = {amount}',
  'securityPolicyRanking.chargeDeducted': 'Actually deducted {amount}',
  'securityPolicyRanking.groupMultiplier': zhRanking.securityPolicyRanking.groupMultiplier,
}

vi.mock('@/api/admin', () => ({
  adminAPI: {
    usage: {
      list,
      getStats,
    },
    dashboard: {
      getSnapshotV2,
      getModelStats,
    },
    users: {
      getById,
    },
  },
}))

vi.mock('@/api/admin/usage', () => ({
  adminUsageAPI: {
    listCleanupTasks, createCleanupTask,
		list: exportList,
  },
}))

vi.mock('file-saver', () => ({ saveAs }))

vi.mock('xlsx', () => ({
	utils: {
		aoa_to_sheet: aoaToSheet,
		sheet_add_aoa: sheetAddAoa,
		book_new: vi.fn(() => ({})),
		book_append_sheet: vi.fn(),
	},
	write: xlsxWrite,
}))

vi.mock('@/api/admin/ops', () => ({
  listErrorLogs,
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    cachedPublicSettings: own.settings,
    showError: auditCenterMocks.showError,
    showWarning,
    showSuccess: auditCenterMocks.showSuccess,
    showInfo: vi.fn(),
  }),
}))

vi.mock('@/utils/format', async () => ({
  ...await vi.importActual<typeof import('@/utils/format')>('@/utils/format'),
  formatReasoningEffort: (value: string | null | undefined) => value ?? '-',
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => (messages[key] ?? key).replace(/\{(\w+)\}/g, (match, field: string) => String(params?.[field] ?? match)),
      locale: ref('en'),
      te: () => false,
    }),
  }
})

vi.mock('vue-router', () => ({
  useRoute: () => ({
    query: routeQuery
  })
}))

const AppLayoutStub = { template: '<div><slot /></div>' }
const UsageFiltersStub = defineComponent({
  emits: ['cleanup', 'update:modelValue', 'change', 'export'],
  setup(_, { expose }) {
    const userKeyword = ref('')
    let userSearchRevision = 0
    const setUserKeyword = (email: string) => {
      userSearchRevision += 1
      userKeyword.value = email
    }
    expose({
      getUserSearchRevision: () => userSearchRevision,
      setUserKeyword,
      simulateUserInput: setUserKeyword,
    })
    return { userKeyword }
  },
  template: '<div><button data-test="cleanup-open" @click="$emit(\'cleanup\')">cleanup</button><span data-test="user-filter-label">{{ userKeyword }}</span><slot name="after-reset" /></div>',
})
const UsageTableStub = {
  props: ['columns'],
  emits: ['userClick'],
  template: '<div data-test="usage-table"><button class="user-click" @click="$emit(\'userClick\', 2)">user</button></div>',
}
const UsageConnectionsStub = { props: ['filters', 'refreshKey'], template: '<div data-test="connection-tab" />' }
const LatencyObservabilityPanelStub = {
  props: {
    startDate: String,
    endDate: String,
    model: String,
    userId: Number,
    apiKeyId: Number,
    accountId: Number,
    groupId: Number,
    refreshToken: Number,
    flat: Boolean,
    hideTitle: Boolean,
  },
  template: '<div data-test="latency-tab" />',
}
const UserTokenRankingStub = {
  emits: ['select-user'],
  template: '<div data-test="ranking"><button class="pick-user" @click="$emit(\'select-user\', 5, \'rank@test.com\')">pick</button></div>',
}
const ModelDistributionChartStub = {
  props: ['metric'],
  emits: ['update:metric'],
  template: `
    <div data-test="model-chart">
      <span class="metric">{{ metric }}</span>
      <button class="switch-metric" @click="$emit('update:metric', 'actual_cost')">switch</button>
    </div>
  `,
}
const GroupDistributionChartStub = {
  props: ['metric'],
  emits: ['update:metric'],
  template: `
    <div data-test="group-chart">
      <span class="metric">{{ metric }}</span>
      <button class="switch-metric" @click="$emit('update:metric', 'actual_cost')">switch</button>
    </div>
  `,
}

const mountRouteFilteredUsageView = (realDatePicker = false, realCleanupDialog = false) => mount(UsageView, {
  global: { stubs: {
    AppLayout: AppLayoutStub, UsageStatsCards: true, UsageFilters: UsageFiltersStub,
    UsageTable: true, UsageExportProgress: true, UsageCleanupDialog: !realCleanupDialog,
    BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' },
    ConfirmDialog: { props: ['show'], emits: ['confirm'], template: '<button v-if="show" data-test="cleanup-confirm" @click="$emit(\'confirm\')">confirm</button>' },
    UserBalanceHistoryModal: true, Pagination: true, Select: true,
    DateRangePicker: !realDatePicker, Icon: true, TokenUsageTrend: true,
    ModelDistributionChart: true, GroupDistributionChart: true,
    EndpointDistributionChart: true, UserTokenRanking: true,
    LatencyObservabilityPanel: LatencyObservabilityPanelStub,
  } },
})

describe('admin UsageView route filters', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    Object.keys(routeQuery).forEach((key) => delete routeQuery[key])
    list.mockReset().mockResolvedValue({ items: [], total: 0, pages: 0 })
    getStats.mockReset().mockResolvedValue({
      total_requests: 0,
      total_input_tokens: 0,
      total_output_tokens: 0,
      total_cache_tokens: 0,
      total_tokens: 0,
      total_cost: 0,
      total_actual_cost: 0,
      average_duration_ms: 0,
    })
    getSnapshotV2.mockReset().mockResolvedValue({ trend: [], models: [], groups: [] })
    getModelStats.mockReset().mockResolvedValue({ models: [] })
    getById.mockReset()
  })

  afterEach(() => {
    Object.keys(routeQuery).forEach((key) => delete routeQuery[key])
    vi.useRealTimers()
  })

  it('mounts the latency panel only after selecting its tab and passes supported filters', async () => {
    const wrapper = mountRouteFilteredUsageView()
    await flushPromises()
    expect(wrapper.find('[data-test="latency-tab"]').exists()).toBe(false)

    const vm = wrapper.vm as any
    vm.filters.model = 'gpt-5.6-sol'
    vm.filters.user_id = 42
    vm.filters.api_key_id = 8
    vm.filters.account_id = 7
    vm.filters.group_id = 3
    await wrapper.vm.$nextTick()

    const latencyTab = wrapper.findAll('[data-testid="usage-detail-tab"]').find(tab => tab.text() === 'latencyObservability.title')!
    await latencyTab.trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="latency-tab"]').exists()).toBe(true)
    expect(wrapper.findComponent(LatencyObservabilityPanelStub).props()).toMatchObject({
      startDate: expect.any(String),
      endDate: expect.any(String),
      model: 'gpt-5.6-sol',
      userId: 42,
      apiKeyId: 8,
      accountId: 7,
      groupId: 3,
      flat: true,
      hideTitle: true,
    })
    wrapper.unmount()
  })

  it('gives the RPM/TPM card the range its stats were loaded for, not a newer pending range', async () => {
    const wrapper = mountRouteFilteredUsageView()
    await flushPromises()
    const cards = () => wrapper.findComponent(UsageStatsCards)
    const vm = wrapper.vm as any
    expect(cards().props('showRate')).toBe(true)
    expect(cards().props('rateWindow')).toMatchObject({ start: vm.filters.start_date, end: vm.filters.end_date, now: expect.any(Number) })

    let resolveStats!: (value: unknown) => void
    getStats.mockReturnValueOnce(new Promise((resolve) => { resolveStats = resolve }))
    const previousWindow = cards().props('rateWindow')
    vm.onDateRangeChange({ startDate: '2026-11-01', endDate: '2026-11-07', preset: null })
    await flushPromises()
    expect(getStats).toHaveBeenLastCalledWith(expect.objectContaining({ start_date: '2026-11-01', end_date: '2026-11-07' }))
    expect(cards().props('rateWindow')).toEqual(previousWindow)

    resolveStats({ total_requests: 10_080, total_tokens: 0, total_cost: 0, total_actual_cost: 0, average_duration_ms: 0 })
    await flushPromises()
    expect(cards().props('rateWindow')).toMatchObject({ start: '2026-11-01', end: '2026-11-07' })
    wrapper.unmount()
  })

  it.each([
    { startDate: '', endDate: '2026-11-07' },
    { startDate: '2026-11-01', endDate: '' },
    { startDate: '', endDate: '' },
  ])('pins the admin today-so-far default for partial dates: %j', async (dates) => {
    const now = new Date(2026, 10, 10, 6, 30)
    vi.setSystemTime(now)
    const wrapper = mountRouteFilteredUsageView(true)
    await flushPromises()
    const picker = wrapper.findComponent(DateRangePicker)
    await picker.get('.date-picker-trigger').trigger('click')
    const inputs = picker.findAll('.date-picker-input')
    await inputs[0].setValue(dates.startDate)
    await inputs[1].setValue(dates.endDate)
    await picker.get('.date-picker-apply').trigger('click')
    await flushPromises()
    const start = new Date(2026, 10, 10).toISOString()
    const end = now.toISOString()
    expect(getStats).toHaveBeenLastCalledWith(expect.objectContaining({ start_date: start, end_date: end }))
    const window = wrapper.findComponent(UsageStatsCards).props('rateWindow')
    expect(window).toEqual({ start, end, now: now.getTime() })
    expect(usagePerMinute(390, window)).toBe(1)
    wrapper.unmount()
  })

  it('shows the routed user while applying user_id to usage requests', async () => {
    routeQuery.user_id = '42'
    getById.mockResolvedValue({ id: 42, email: 'route-user@test.com' })

    const wrapper = mountRouteFilteredUsageView()
    await flushPromises()

    expect(getById).toHaveBeenCalledWith(42, true)
    expect(list).toHaveBeenCalledWith(expect.objectContaining({ user_id: 42 }), expect.anything())
    expect(wrapper.find('[data-test="user-filter-label"]').text()).toBe('route-user@test.com')
  })

  it('does not apply a stale routed user label after user_id changes', async () => {
    routeQuery.user_id = '42'
    let resolveLookup!: (user: { id: number; email: string }) => void
    getById.mockReturnValue(new Promise((resolve) => { resolveLookup = resolve }))

    const wrapper = mountRouteFilteredUsageView()
    await wrapper.vm.$nextTick()
    ;(wrapper.vm as any).filters.user_id = 84
    ;(wrapper.findComponent(UsageFiltersStub).vm as any).setUserKeyword('current-user@test.com')

    resolveLookup({ id: 42, email: 'stale-user@test.com' })
    await flushPromises()

    expect(wrapper.find('[data-test="user-filter-label"]').text()).toBe('current-user@test.com')
  })

  it('does not overwrite newer user input when the routed user lookup succeeds', async () => {
    routeQuery.user_id = '42'
    let resolveLookup!: (user: { id: number; email: string }) => void
    getById.mockReturnValue(new Promise((resolve) => { resolveLookup = resolve }))

    const wrapper = mountRouteFilteredUsageView()
    await wrapper.vm.$nextTick()
    ;(wrapper.findComponent(UsageFiltersStub).vm as any).simulateUserInput('new-search@test.com')

    resolveLookup({ id: 42, email: 'route-user@test.com' })
    await flushPromises()

    expect((wrapper.vm as any).filters.user_id).toBe(42)
    expect(wrapper.find('[data-test="user-filter-label"]').text()).toBe('new-search@test.com')
  })

  it('does not overwrite newer user input when the routed user lookup fails', async () => {
    routeQuery.user_id = '42'
    let rejectLookup!: (error: Error) => void
    getById.mockReturnValue(new Promise((_, reject) => { rejectLookup = reject }))

    const wrapper = mountRouteFilteredUsageView()
    await wrapper.vm.$nextTick()
    ;(wrapper.findComponent(UsageFiltersStub).vm as any).simulateUserInput('new-search@test.com')

    rejectLookup(new Error('lookup failed'))
    await flushPromises()

    expect((wrapper.vm as any).filters.user_id).toBe(42)
    expect(wrapper.find('[data-test="user-filter-label"]').text()).toBe('new-search@test.com')
  })

  it('shows the routed user ID when its label lookup fails', async () => {
    routeQuery.user_id = '42'
    getById.mockRejectedValue(new Error('lookup failed'))

    const wrapper = mountRouteFilteredUsageView()
    await flushPromises()

    expect(list).toHaveBeenCalledWith(expect.objectContaining({ user_id: 42 }), expect.anything())
    expect(wrapper.find('[data-test="user-filter-label"]').text()).toBe('42')
  })
})

describe('admin UsageView native compaction filter', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    list.mockReset().mockResolvedValue({ items: [], total: 0, pages: 0 })
    getStats.mockReset().mockResolvedValue({
      total_requests: 0,
      total_input_tokens: 0,
      total_output_tokens: 0,
      total_cache_tokens: 0,
      total_tokens: 0,
      total_cost: 0,
      total_actual_cost: 0,
      average_duration_ms: 0,
    })
    getSnapshotV2.mockReset().mockResolvedValue({ trend: [], models: [], groups: [] })
    getModelStats.mockReset().mockResolvedValue({ models: [] })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('propagates the filter to list/stats/model/snapshot requests and clears it on reset', async () => {
    const wrapper = mountRouteFilteredUsageView()
    vi.advanceTimersByTime(120)
    await flushPromises()

    list.mockClear()
    getStats.mockClear()
    getModelStats.mockClear()
    getSnapshotV2.mockClear()

    ;(wrapper.vm as any).filters.native_compaction_v2 = true
    ;(wrapper.vm as any).applyFilters()
    await flushPromises()

    expect((wrapper.vm as any).breakdownFilters.native_compaction_v2).toBe(true)
    expect(list).toHaveBeenCalledWith(
      expect.objectContaining({ native_compaction_v2: true }),
      expect.anything()
    )
    expect(getStats).toHaveBeenCalledWith(expect.objectContaining({ native_compaction_v2: true }))
    expect(getModelStats).toHaveBeenCalledWith(expect.objectContaining({ native_compaction_v2: true }))
    expect(getSnapshotV2).toHaveBeenCalledWith(expect.objectContaining({ native_compaction_v2: true }))

    list.mockClear()
    getStats.mockClear()
    getModelStats.mockClear()
    getSnapshotV2.mockClear()

    ;(wrapper.vm as any).resetFilters()
    await flushPromises()

    expect((wrapper.vm as any).filters.native_compaction_v2).toBeNull()
    expect((wrapper.vm as any).breakdownFilters).not.toHaveProperty('native_compaction_v2')
    expect(list).toHaveBeenCalledWith(
      expect.objectContaining({ native_compaction_v2: null }),
      expect.anything()
    )
    expect(getStats).toHaveBeenCalledWith(expect.objectContaining({ native_compaction_v2: null }))
    expect(getModelStats).toHaveBeenCalledWith(expect.objectContaining({ native_compaction_v2: null }))
    expect(getSnapshotV2).toHaveBeenCalledWith(expect.objectContaining({ native_compaction_v2: null }))
  })
})

describe('admin UsageView distribution metric toggles', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    list.mockReset()
    getStats.mockReset()
    getSnapshotV2.mockReset()
    getById.mockReset()
    getModelStats.mockReset()

    list.mockResolvedValue({
      items: [],
      total: 0,
      pages: 0,
    })
    getStats.mockResolvedValue({
      total_requests: 0,
      total_input_tokens: 0,
      total_output_tokens: 0,
      total_cache_tokens: 0,
      total_tokens: 0,
      total_cost: 0,
      total_actual_cost: 0,
      average_duration_ms: 0,
    })
    getSnapshotV2.mockResolvedValue({
      trend: [],
      models: [],
      groups: [],
    })
    getModelStats.mockResolvedValue({ models: [] })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('keeps previous model stats visible during refresh until new data arrives', async () => {
    // 首次加载返回 A
    getModelStats.mockResolvedValueOnce({ models: [{ model: 'A', total_tokens: 10 }] })

    const wrapper = mount(UsageView, {
      global: { stubs: {
        AppLayout: AppLayoutStub, UsageStatsCards: true, UsageFilters: UsageFiltersStub,
        UsageTable: true, UsageExportProgress: true, UsageCleanupDialog: true,
        UserBalanceHistoryModal: true, AuditLogModal: true, Pagination: true, Select: true,
        DateRangePicker: true, Icon: true, TokenUsageTrend: true,
        ModelDistributionChart: ModelDistributionChartStub, GroupDistributionChart: GroupDistributionChartStub,
        EndpointDistributionChart: true, UserTokenRanking: true,
      } },
    })
    vi.advanceTimersByTime(120)
    await flushPromises()
    expect((wrapper.vm as any).requestedModelStats).toEqual([{ model: 'A', total_tokens: 10 }])

    // 刷新:让第二次 getModelStats 处于 pending,断言旧数据 A 仍在(不被清空成 [])
    let resolveSecond: (v: any) => void = () => {}
    getModelStats.mockReturnValueOnce(new Promise((res) => { resolveSecond = res }))
    ;(wrapper.vm as any).refreshData()
    await flushPromises()
    expect((wrapper.vm as any).requestedModelStats).toEqual([{ model: 'A', total_tokens: 10 }])

    // 新数据到达后替换为 B
    resolveSecond({ models: [{ model: 'B', total_tokens: 20 }] })
    await flushPromises()
    expect((wrapper.vm as any).requestedModelStats).toEqual([{ model: 'B', total_tokens: 20 }])
  })

  it('keeps model and group metric toggles independent without refetching chart data', async () => {
    const wrapper = mount(UsageView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub,
          UsageStatsCards: true,
          UsageFilters: UsageFiltersStub,
          UsageTable: true,
          UsageExportProgress: true,
          UsageCleanupDialog: true,
          UserBalanceHistoryModal: true,
          Pagination: true,
          Select: true,
          DateRangePicker: true,
          Icon: true,
          TokenUsageTrend: true,
          ModelDistributionChart: ModelDistributionChartStub,
          GroupDistributionChart: GroupDistributionChartStub,
          UserTokenRanking: true,
        },
      },
    })

    vi.advanceTimersByTime(120)
    await flushPromises()

    expect(getSnapshotV2).toHaveBeenCalledTimes(1)
    const now = new Date(Date.now() - 120) // The applied snapshot was captured before advancing timers.
    const yesterday = new Date(now.getTime() - 24 * 60 * 60 * 1000)
    expect(getSnapshotV2).toHaveBeenCalledWith(expect.objectContaining({
      start_date: yesterday.toISOString(),
      end_date: now.toISOString(),
      granularity: 'hour'
    }))

    const modelChart = wrapper.find('[data-test="model-chart"]')
    const groupChart = wrapper.find('[data-test="group-chart"]')

    expect(modelChart.find('.metric').text()).toBe('tokens')
    expect(groupChart.find('.metric').text()).toBe('tokens')

    await modelChart.find('.switch-metric').trigger('click')
    await flushPromises()

    expect(modelChart.find('.metric').text()).toBe('actual_cost')
    expect(groupChart.find('.metric').text()).toBe('tokens')
    expect(getSnapshotV2).toHaveBeenCalledTimes(1)

    await groupChart.find('.switch-metric').trigger('click')
    await flushPromises()

    expect(modelChart.find('.metric').text()).toBe('actual_cost')
    expect(groupChart.find('.metric').text()).toBe('actual_cost')
    expect(getSnapshotV2).toHaveBeenCalledTimes(1)
  })
})

describe('admin UsageView request ID column visibility', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.mocked(localStorage.getItem).mockReset().mockReturnValue(null)
    vi.mocked(localStorage.setItem).mockReset()
    list.mockReset().mockResolvedValue({ items: [], total: 0, pages: 0 })
    getStats.mockReset().mockResolvedValue({
      total_requests: 0, total_input_tokens: 0, total_output_tokens: 0,
      total_cache_tokens: 0, total_tokens: 0, total_cost: 0, total_actual_cost: 0, average_duration_ms: 0,
    })
    getSnapshotV2.mockReset().mockResolvedValue({ trend: [], models: [], groups: [] })
    getModelStats.mockReset().mockResolvedValue({ models: [] })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('keeps request ID hidden by default and allows enabling it from column settings', async () => {
    const wrapper = mount(UsageView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub,
          UsageStatsCards: true,
          UsageFilters: UsageFiltersStub,
          UsageTable: UsageTableStub,
          UsageExportProgress: true,
          UsageCleanupDialog: true,
          UserBalanceHistoryModal: true,
          AuditLogModal: true,
          Pagination: true,
          Select: true,
          DateRangePicker: true,
          Icon: true,
          TokenUsageTrend: true,
          ModelDistributionChart: true,
          GroupDistributionChart: true,
          EndpointDistributionChart: true,
          UserTokenRanking: true,
        },
      },
    })
    await wrapper.vm.$nextTick()

    const usageTable = wrapper.findComponent(UsageTableStub)
    const usageColumnKeys = usageTable.props('columns').map((column: { key: string }) => column.key)
    const latencyColumnIndex = usageColumnKeys.indexOf('latency')
    expect(usageColumnKeys[latencyColumnIndex + 1]).toBe('created_at')
    expect(usageColumnKeys).not.toEqual(expect.arrayContaining([
      'content_reasoning_first_ms', 'content_visible_text_first_ms', 'content_response_byte_first_ms',
    ]))
    expect(usageTable.props('columns')).not.toEqual(
      expect.arrayContaining([expect.objectContaining({ key: 'request_id' })]),
    )

    await wrapper.get('button[title="admin.users.columnSettings"]').trigger('click')
    const requestIdToggle = wrapper.findAll('button').find((button) => button.text() === 'Request ID')
    expect(requestIdToggle).toBeDefined()
    await requestIdToggle!.trigger('click')

    expect(usageTable.props('columns')).toEqual(
      expect.arrayContaining([expect.objectContaining({ key: 'request_id', label: 'Request ID' })]),
    )
    expect(localStorage.setItem).toHaveBeenCalledWith(
      'usage-hidden-columns-version',
      'upstream-request-id-hidden-by-default',
    )

    expect(wrapper.findAll('button').some(button =>
      ['First reasoning event', 'First visible text', 'First response byte'].includes(button.text()),
    )).toBe(false)
  })

  it('keeps upstream ID hidden by default and allows enabling it from column settings', async () => {
    const wrapper = mount(UsageView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub,
          UsageStatsCards: true,
          UsageFilters: UsageFiltersStub,
          UsageTable: UsageTableStub,
          UsageExportProgress: true,
          UsageCleanupDialog: true,
          UserBalanceHistoryModal: true,
          AuditLogModal: true,
          Pagination: true,
          Select: true,
          DateRangePicker: true,
          Icon: true,
          TokenUsageTrend: true,
          ModelDistributionChart: true,
          GroupDistributionChart: true,
          EndpointDistributionChart: true,
          UserTokenRanking: true,
        },
      },
    })
    await wrapper.vm.$nextTick()

    const usageTable = wrapper.findComponent(UsageTableStub)
    expect(usageTable.props('columns')).not.toEqual(
      expect.arrayContaining([expect.objectContaining({ key: 'upstream_request_id' })]),
    )

    await wrapper.get('button[title="admin.users.columnSettings"]').trigger('click')
    const upstreamToggle = wrapper.findAll('button').find((button) => button.text() === 'Upstream ID')
    expect(upstreamToggle).toBeDefined()
    await upstreamToggle!.trigger('click')

    expect(usageTable.props('columns')).toEqual(
      expect.arrayContaining([expect.objectContaining({ key: 'upstream_request_id', label: 'Upstream ID' })]),
    )
  })
})

describe('admin UsageView handleUserClick', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    list.mockReset()
    getStats.mockReset()
    getSnapshotV2.mockReset()
    getById.mockReset()

    list.mockResolvedValue({ items: [], total: 0, pages: 0 })
    getStats.mockResolvedValue({
      total_requests: 0, total_input_tokens: 0, total_output_tokens: 0,
      total_cache_tokens: 0, total_tokens: 0, total_cost: 0, total_actual_cost: 0, average_duration_ms: 0,
    })
    getSnapshotV2.mockResolvedValue({ trend: [], models: [], groups: [] })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('opens user via include_deleted when clicking a usage row user', async () => {
    getById.mockResolvedValue({ id: 2, email: 'd@test.com', deleted_at: '2026-05-28T00:00:00Z' })

    const wrapper = mount(UsageView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub,
          UsageStatsCards: true,
          UsageFilters: UsageFiltersStub,
          UsageTable: UsageTableStub,
          UsageExportProgress: true,
          UsageCleanupDialog: true,
          UserBalanceHistoryModal: true,
          AuditLogModal: true,
          Pagination: true,
          Select: true,
          DateRangePicker: true,
          Icon: true,
          TokenUsageTrend: true,
          ModelDistributionChart: true,
          GroupDistributionChart: true,
          EndpointDistributionChart: true,
          UserTokenRanking: true,
        },
      },
    })

    vi.advanceTimersByTime(120)
    await flushPromises()

    await wrapper.find('[data-test="usage-table"] .user-click').trigger('click')
    await flushPromises()

    expect(getById).toHaveBeenCalledWith(2, true)
  })
})

describe('admin UsageView errors tab filter forwarding', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    list.mockReset()
    getStats.mockReset()
    getSnapshotV2.mockReset()
    getModelStats.mockReset()
    listErrorLogs.mockReset()

    list.mockResolvedValue({ items: [], total: 0, pages: 0 })
    getStats.mockResolvedValue({
      total_requests: 0, total_input_tokens: 0, total_output_tokens: 0,
      total_cache_tokens: 0, total_tokens: 0, total_cost: 0, total_actual_cost: 0, average_duration_ms: 0,
    })
    getSnapshotV2.mockResolvedValue({ trend: [], models: [], groups: [] })
    getModelStats.mockResolvedValue({ models: [] })
    listErrorLogs.mockResolvedValue({ items: [], total: 0, pages: 0 })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('forwards model/account_id/group_id to listErrorLogs on the errors tab', async () => {
    const wrapper = mount(UsageView, {
      global: { stubs: {
        AppLayout: AppLayoutStub, UsageStatsCards: true, UsageFilters: UsageFiltersStub,
        UsageTable: true, UsageExportProgress: true, UsageCleanupDialog: true,
        UserBalanceHistoryModal: true, AuditLogModal: true, Pagination: true, Select: true,
        DateRangePicker: true, Icon: true, TokenUsageTrend: true,
        ModelDistributionChart: true, GroupDistributionChart: true, EndpointDistributionChart: true,
        UserTokenRanking: true, OpsErrorLogTable: true, OpsErrorDetailModal: true,
      } },
    })
    vi.advanceTimersByTime(120)
    await flushPromises()

    // 模拟用户在过滤器里选择了模型/账户/分组
    const vm = wrapper.vm as any
    vm.filters.model = 'gpt-5.3-codex'
    vm.filters.account_id = 7
    vm.filters.group_id = 3
    await flushPromises()

    // 切换到「错误请求」标签（第二个 tab 按钮）触发 loadAdminErrors
    const tabs = wrapper.findAll('[data-testid="usage-detail-tab"]')
    await tabs[1].trigger('click')
    await flushPromises()

    expect(listErrorLogs).toHaveBeenCalledWith(expect.objectContaining({
      view: 'all',
      model: 'gpt-5.3-codex',
      account_id: 7,
      group_id: 3,
    }))
  })
})

describe('admin UsageView ranking tab', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    list.mockReset()
    getStats.mockReset()
    getSnapshotV2.mockReset()
    getModelStats.mockReset()

    list.mockResolvedValue({ items: [], total: 0, pages: 0 })
    getStats.mockResolvedValue({
      total_requests: 0, total_input_tokens: 0, total_output_tokens: 0,
      total_cache_tokens: 0, total_tokens: 0, total_cost: 0, total_actual_cost: 0, average_duration_ms: 0,
    })
    getSnapshotV2.mockResolvedValue({ trend: [], models: [], groups: [] })
    getModelStats.mockResolvedValue({ models: [] })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('mounts ranking lazily and drill-down sets user filter then jumps back to usage tab', async () => {
    const wrapper = mount(UsageView, {
      global: { stubs: {
        AppLayout: AppLayoutStub, UsageStatsCards: true, UsageFilters: UsageFiltersStub,
        UsageTable: true, UsageExportProgress: true, UsageCleanupDialog: true,
        UserBalanceHistoryModal: true, Pagination: true, Select: true,
        DateRangePicker: true, Icon: true, TokenUsageTrend: true,
        ModelDistributionChart: true, GroupDistributionChart: true, EndpointDistributionChart: true,
        UserTokenRanking: UserTokenRankingStub,
        UsageConnections: UsageConnectionsStub, OpsErrorLogTable: true, OpsErrorDetailModal: true,
        LatencyObservabilityPanel: LatencyObservabilityPanelStub,
      } },
    })
    vi.advanceTimersByTime(120)
    await flushPromises()

    // 懒挂载:切到排行 tab 前不渲染
    expect(wrapper.find('[data-test="ranking"]').exists()).toBe(false)

    const tabs = wrapper.findAll('[data-testid="usage-detail-tab"]')
    expect(tabs).toHaveLength(6)
    expect(wrapper.find('[data-test="connection-tab"]').exists()).toBe(false)
    await tabs[2].trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="connection-tab"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="ranking"]').exists()).toBe(false)
    await tabs[3].trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="ranking"]').exists()).toBe(true)

    // 下钻:设置 user_id、切回用量明细 tab 并按新筛选重新拉取列表
    list.mockClear()
    await wrapper.find('[data-test="ranking"] .pick-user').trigger('click')
    await flushPromises()

    expect((wrapper.vm as any).activeTab).toBe('usage')
    expect((wrapper.vm as any).filters.user_id).toBe(5)
    expect(list).toHaveBeenCalledWith(expect.objectContaining({ user_id: 5 }), expect.anything())
  })
})

describe('admin UsageView model audit export', () => {
	beforeEach(() => {
		vi.useFakeTimers()
		list.mockReset().mockResolvedValue({ items: [], total: 0, pages: 0 })
		exportList.mockReset().mockResolvedValue({
			items: [{
				id: 1,
				created_at: '2026-08-04T00:00:00Z',
				model: 'gpt-5.6-sol',
				upstream_model: 'gpt-5.5',
				upstream_response_model: 'gpt-5.4',
				upstream_model_mismatch: true,
				request_type: 'sync',
				input_tokens: 1,
				output_tokens: 1,
				cache_read_tokens: 0,
				cache_creation_tokens: 0,
				duration_ms: 10,
			}],
			total: 1,
			pages: 1,
		})
		getStats.mockReset().mockResolvedValue({
			total_requests: 0, total_input_tokens: 0, total_output_tokens: 0,
			total_cache_tokens: 0, total_tokens: 0, total_cost: 0, total_actual_cost: 0, average_duration_ms: 0,
		})
		getSnapshotV2.mockReset().mockResolvedValue({ trend: [], models: [], groups: [] })
		getModelStats.mockReset().mockResolvedValue({ models: [] })
		aoaToSheet.mockClear()
		sheetAddAoa.mockClear()
		saveAs.mockClear()
		xlsxWrite.mockClear()
	})

	afterEach(() => {
		vi.useRealTimers()
	})

	it('exports requested, sent, response, and mismatch as separate admin columns', async () => {
		const wrapper = mountRouteFilteredUsageView()
		vi.advanceTimersByTime(120)
		await flushPromises()
		;(wrapper.vm as any).filters.native_compaction_v2 = true

		await (wrapper.vm as any).exportToExcel()
		await flushPromises()

		expect(exportList).toHaveBeenCalledWith(
			expect.objectContaining({ native_compaction_v2: true }),
			expect.anything()
		)

		const headers = aoaToSheet.mock.calls[0][0][0]
		expect(headers.slice(4, 8)).toEqual([
			'Requested model',
			'Sent upstream model',
			'Upstream response model',
			'Upstream model mismatch',
		])
		const row = sheetAddAoa.mock.calls[0][1][0]
		expect(row.slice(4, 8)).toEqual(['gpt-5.6-sol', 'gpt-5.5', 'gpt-5.4', 'Yes'])
		const cacheRateIndex = headers.indexOf('usage.latencyCacheRate')
		expect(cacheRateIndex).toBe(headers.indexOf('admin.usage.cacheCreationTokens') + 1)
		expect(row[cacheRateIndex]).toBe('0%')
		expect(saveAs).toHaveBeenCalledTimes(1)
	})
})

describe('PKSecurityPolicyRanking', () => {
  const mountedViews: Array<{ unmount: () => void }> = []
  let visibility: ReturnType<typeof vi.spyOn>
  const settingsData = () => ({ multiplier: '6.700000', auto_disable_enabled: false, auto_disable_threshold: 10, reset_on_manual_enable: true, group_ids: [] as number[], settings_revision: 1 })
  const rankingData = (count = 4) => ({
    items: [{ rank: 1, user_id: 7, username: 'Example user', email: 'private@example.com', balance: '100.00000000', status: 'active', auto_disable_count: count, trigger_count: count, counted_count: 1, skipped_count: 1, unknown_count: count - 2, deducted_count: 1, pending_count: count - 1, pending_amount: '20.10000000' }],
    total: 1, total_triggers: count, total_pending: count - 1, total_counted: 1, total_skipped: 1, total_unknown: count - 2, multiplier: '6.700000',
    start_date: '2026-10-03', end_date: '2026-10-03', timezone: 'UTC', generated_at: '2026-10-03T12:00:00Z',
  })
  const recordsData = () => ({
    start_date: '2026-10-03', end_date: '2026-10-03', timezone: 'UTC', total: 3, page: 1, page_size: 20, pages: 1,
    items: [
      { usage_id: 101, occurred_at: '2026-10-03T12:00:00Z', group_id: 2, group_name: 'Unselected group', request_id: 'req-101', model: 'test-model', count_status: 'skipped', count_reason: 'group_not_enabled', settings_revision: 1, evaluated_at: '2026-10-03T12:00:00Z' },
      { usage_id: 102, occurred_at: '2026-10-03T11:00:00Z', group_id: 1, group_name: 'Selected group', request_id: 'req-102', model: 'test-model', count_status: 'counted', count_reason: '', settings_revision: 1, evaluated_at: '2026-10-03T11:00:00Z' },
      { usage_id: 103, occurred_at: '2026-10-03T10:00:00Z', group_id: null, group_name: '', request_id: '', model: '', count_status: 'unknown', count_reason: 'historical_unknown', settings_revision: null, evaluated_at: null },
    ],
  })
  const previewData = () => ({
    id: 'frozen-preview-7', user_id: 7, username: 'Example user', email: 'private@example.com',
    start_date: '2026-10-03', end_date: '2026-10-03', timezone: 'UTC', trigger_count: 3,
    multiplier: '6.700000', amount: '20.10000000', balance: '100.00000000',
    expires_at: '2026-10-03T12:10:00Z', status: 'pending',
  })
  const mountRanking = () => {
    const wrapper = mount(SecurityPolicyRankingView, {
      global: { stubs: {
        AppLayout: AppLayoutStub, DateRangePicker: true, Pagination: true, UserAllowlistSelector: true,
        RouterLink: { template: '<a><slot /></a>' },
        BaseDialog: { props: ['show'], template: '<section v-if="show"><slot /><slot name="footer" /></section>' },
      } },
    })
    mountedViews.push(wrapper)
    return wrapper
  }
  const deferred = <T,>() => {
    let resolve!: (value: T) => void
    const promise = new Promise<T>(done => { resolve = done })
    return { promise, resolve }
  }

  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date(2026, 9, 3, 12))
    visibility = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
    rankingAuth.isAdmin = true
    rankingMocks.getRanking.mockReset().mockResolvedValue({ data: rankingData() })
    rankingMocks.getPublicRanking.mockReset().mockResolvedValue({ data: { ...rankingData(), items: [{ rank: 1, display_name: 'Pub***', trigger_count: 4 }] } })
    rankingMocks.getSettings.mockReset().mockResolvedValue({ data: settingsData() })
    rankingMocks.getGroups.mockReset().mockResolvedValue([{ id: 1, name: 'Selected group', platform: 'openai', status: 'active' }, { id: 2, name: 'Unselected group', platform: 'openai', status: 'inactive' }])
    rankingMocks.getRecords.mockReset().mockResolvedValue({ data: recordsData() })
    rankingMocks.saveSettings.mockReset().mockImplementation(async value => ({ data: value }))
    rankingMocks.preview.mockReset().mockResolvedValue({ data: previewData() })
    rankingMocks.deduct.mockReset().mockResolvedValue({ data: { ...previewData(), status: 'completed', balance_after: '79.90000000' } })
  })
  afterEach(() => {
    mountedViews.splice(0).forEach(wrapper => wrapper.unmount())
    vi.restoreAllMocks()
    vi.useRealTimers()
  })

  it('defaults to today with an explicit timezone, saved settings and serialized five-second refresh', async () => {
    const wrapper = mountRanking()
    await flushPromises()
    expect(rankingMocks.getRanking).toHaveBeenCalledWith(expect.objectContaining({ start_date: '2026-10-03', end_date: '2026-10-03', timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC', page: 1, page_size: 50 }), expect.any(AbortSignal))
    expect(wrapper.get('[data-testid="ranking-manual-deduction-info"]').text()).toContain('securityPolicyRanking.savedMultiplier')
    expect(rankingMocks.getSettings).toHaveBeenCalledOnce()
    expect(wrapper.find('form').exists()).toBe(false)
    const pending = deferred<{ data: ReturnType<typeof rankingData> }>()
    rankingMocks.getRanking.mockImplementationOnce(() => pending.promise)
    vi.advanceTimersByTime(5000)
    await flushPromises()
    vi.advanceTimersByTime(30000)
    await flushPromises()
    expect(rankingMocks.getRanking).toHaveBeenCalledTimes(2)
    pending.resolve({ data: rankingData(6) })
    await flushPromises()
    expect(wrapper.find('[data-testid="ranking-user-7"]').text()).toContain('6')
    await wrapper.find('[data-testid="ranking-auto-refresh"]').trigger('click')
    vi.advanceTimersByTime(30000)
    await flushPromises()
    expect(rankingMocks.getRanking).toHaveBeenCalledTimes(2)
    await wrapper.find('[data-testid="ranking-refresh-seconds"]').setValue('2')
    await wrapper.find('[data-testid="ranking-auto-refresh"]').trigger('click')
    vi.advanceTimersByTime(2000)
    await flushPromises()
    expect(rankingMocks.getRanking).toHaveBeenCalledTimes(3)
    await wrapper.find('[data-testid="ranking-refresh-seconds"]').setValue('0')
    vi.advanceTimersByTime(30000)
    await flushPromises()
    expect(rankingMocks.getRanking).toHaveBeenCalledTimes(3)
  })

  it('rejects stale date responses and requests only the newest range after cancellation', async () => {
    const first = deferred<{ data: ReturnType<typeof rankingData> }>()
    rankingMocks.getRanking.mockImplementationOnce(() => first.promise)
    const wrapper = mountRanking()
    await flushPromises()
    const firstSignal = rankingMocks.getRanking.mock.calls[0][1] as AbortSignal
    wrapper.findComponent({ name: 'DateRangePicker' }).vm.$emit('change', { startDate: '2026-10-01', endDate: '2026-10-02', preset: null })
    await flushPromises()
    expect(firstSignal.aborted).toBe(true)
    expect(rankingMocks.getRanking).toHaveBeenCalledTimes(1)
    first.resolve({ data: { ...rankingData(), items: [{ ...rankingData().items[0], username: 'STALE' }] } })
    await flushPromises()
    expect(rankingMocks.getRanking).toHaveBeenCalledTimes(2)
    expect(rankingMocks.getRanking.mock.calls[1][0]).toMatchObject({ start_date: '2026-10-01', end_date: '2026-10-02' })
    expect(wrapper.text()).not.toContain('STALE')
  })

  it('follows today across midnight, cancels hidden requests, and stops polling on unmount', async () => {
    const wrapper = mountRanking()
    await flushPromises()
    vi.setSystemTime(new Date(2026, 9, 4, 0, 1))
    vi.advanceTimersByTime(5000)
    await flushPromises()
    expect(rankingMocks.getRanking.mock.calls[1][0]).toMatchObject({ start_date: '2026-10-04', end_date: '2026-10-04' })
    const pending = deferred<{ data: ReturnType<typeof rankingData> }>()
    rankingMocks.getRanking.mockImplementationOnce(() => pending.promise)
    vi.advanceTimersByTime(5000)
    await flushPromises()
    const signal = rankingMocks.getRanking.mock.calls[2][1] as AbortSignal
    visibility.mockReturnValue('hidden')
    document.dispatchEvent(new Event('visibilitychange'))
    expect(signal.aborted).toBe(true)
    pending.resolve({ data: rankingData() })
    await flushPromises()
    vi.advanceTimersByTime(30000)
    expect(rankingMocks.getRanking).toHaveBeenCalledTimes(3)
    visibility.mockReturnValue('visible')
    document.dispatchEvent(new Event('visibilitychange'))
    await flushPromises()
    expect(rankingMocks.getRanking).toHaveBeenCalledTimes(4)
    wrapper.unmount()
    mountedViews.splice(mountedViews.indexOf(wrapper), 1)
    vi.advanceTimersByTime(30000)
    expect(rankingMocks.getRanking).toHaveBeenCalledTimes(4)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('shows only the safe ranking to ordinary users and never calls admin APIs', async () => {
    rankingAuth.isAdmin = false
    // Even an unexpected extra field must not become a user-visible column.
    rankingMocks.getPublicRanking.mockResolvedValue({ data: { ...rankingData(), items: [{ ...rankingData().items[0], display_name: 'Pub***' }] } })
    const wrapper = mountRanking()
    await flushPromises()
    expect(wrapper.text()).toContain('Pub***')
    expect(wrapper.text()).not.toContain('Example user')
    expect(wrapper.text()).not.toContain('private@example.com')
    expect(wrapper.text()).not.toContain('100.00000000')
    expect(wrapper.find('[data-testid="ranking-settings"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="ranking-deduct-7"]').exists()).toBe(false)
    expect(rankingMocks.getSettings).not.toHaveBeenCalled()
    expect(rankingMocks.getRanking).not.toHaveBeenCalled()
    expect(rankingMocks.preview).not.toHaveBeenCalled()
    expect(rankingMocks.deduct).not.toHaveBeenCalled()
    expect(rankingMocks.getPublicRanking).toHaveBeenCalledTimes(1)
    expect(rankingMocks.getGroups).not.toHaveBeenCalled()
    expect(rankingMocks.getRecords).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="ranking-records-7"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="ranking-count-summary"]').exists()).toBe(false)
  })

  it('keeps manual deduction totals independent from the current automatic detection scope', async () => {
    const wrapper = mountRanking()
    await flushPromises()
    await wrapper.get('[data-testid="ranking-deduct-7"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="ranking-preview-formula"]').text()).toBe('3 triggers × 6.7 = 20.1')
    expect(rankingMocks.preview).toHaveBeenLastCalledWith(7, expect.objectContaining({ start_date: '2026-10-03', end_date: '2026-10-03' }), expect.any(AbortSignal))
  })

  it('distinguishes exact calculated amounts, actual automatic deductions, skipped exemptions and unknown history', async () => {
    const statuses = ['deducted', 'zero_cost', 'insufficient_balance', 'skipped', 'previously_charged', 'historical_unknown', 'failed']
    const items = statuses.map((status, index) => ({
      ...recordsData().items[0], usage_id: 501 + index, auto_charge_status: status,
      auto_charge_reason: status === 'skipped' ? 'user_exempt' : status === 'failed' ? 'charge_failed' : '', count_reason: status === 'skipped' ? 'user_exempt' : 'group_not_enabled',
      actual_cost: status === 'historical_unknown' ? null : status === 'zero_cost' ? '0.00000000' : '0.12345678',
      audit_multiplier: status === 'historical_unknown' ? null : '0.290001',
      auto_charge_amount: status === 'historical_unknown' ? null : status === 'zero_cost' ? '0.00000000' : '0.03580259',
    }))
    rankingMocks.getRecords.mockResolvedValue({ data: { ...recordsData(), total: items.length, items } })
    const wrapper = mountRanking()
    await flushPromises()
    await wrapper.get('[data-testid="ranking-records-7"]').trigger('click')
    await flushPromises()
    for (const [index, status] of statuses.entries()) {
      const row = wrapper.get(`[data-testid="ranking-record-${501 + index}"]`)
      expect(row.text()).toContain(`securityPolicyRanking.chargeStatuses.${status}`)
      expect(row.get('[data-testid="ranking-record-charge-deducted"]').text()).toBe(`Actually deducted ${status === 'deducted' ? '0.03580259' : status === 'historical_unknown' ? '—' : '0'}`)
    }
    expect(wrapper.get('[data-testid="ranking-record-501"] [data-testid="ranking-record-charge-formula"]').text()).toBe('Calculated 0.12345678 × 0.290001 = 0.03580259')
    expect(wrapper.get('[data-testid="ranking-record-502"] [data-testid="ranking-record-charge-formula"]').text()).toBe('Calculated 0 × 0.290001 = 0')
    expect(wrapper.get('[data-testid="ranking-record-504"]').text()).toContain('securityPolicyRanking.recordExemptSkipped')
    expect(wrapper.get('[data-testid="ranking-record-504"]').text()).toContain('securityPolicyRanking.chargeReasons.user_exempt')
    expect(wrapper.find('[data-testid="ranking-record-506"] [data-testid="ranking-record-charge-formula"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="ranking-record-507"]').text()).toContain('securityPolicyRanking.chargeReasons.charge_failed')
  })

  it('renders each persisted counting decision without interpreting unknown history as skipped', async () => {
    const wrapper = mountRanking()
    await flushPromises()
    await wrapper.get('[data-testid="ranking-records-7"]').trigger('click')
    await flushPromises()
    expect(rankingMocks.getRecords).toHaveBeenCalledWith(7, expect.objectContaining({ start_date: '2026-10-03', end_date: '2026-10-03', page: 1, page_size: 20 }), expect.any(AbortSignal))
    expect(wrapper.get('[data-testid="ranking-record-101"]').text()).toContain('securityPolicyRanking.recordGroupSkipped')
    expect(wrapper.get('[data-testid="ranking-record-101"]').text()).toContain('Unselected group')
    expect(wrapper.get('[data-testid="ranking-record-102"]').text()).toContain('securityPolicyRanking.recordCounted')
    expect(wrapper.get('[data-testid="ranking-record-103"]').text()).toContain('securityPolicyRanking.recordUnknown')
    expect(wrapper.get('[data-testid="ranking-record-103"]').text()).not.toContain('securityPolicyRanking.recordGroupSkipped')
    expect(wrapper.get('[data-testid="ranking-records-dialog"]').text()).toContain('securityPolicyRanking.recordEvidenceHint')
    expect(rankingMocks.preview).not.toHaveBeenCalled()
    expect(rankingMocks.deduct).not.toHaveBeenCalled()
  })

  it('shows separate audit sources and IDs without making audit blocks deductible', async () => {
    const ranked = rankingData()
    rankingMocks.getRanking.mockResolvedValue({ data: { ...ranked, total_audit_blocks: 3, total_pending: 0, items: [{ ...ranked.items[0], audit_block_count: 3, pending_count: 0, pending_amount: '0' }] } })
    const legacy = recordsData()
    const audits = ['content_audit', 'prompt_audit', 'combined_audit'].map((source, index) => ({
      ...legacy.items[0], usage_id: 0, audit_id: 401 + index, record_key: `audit:${401 + index}`, source,
      request_id: `audit-request-${index}`, occurred_at: '2026-10-03T12:00:00Z',
    }))
    rankingMocks.getRecords.mockResolvedValue({ data: { ...legacy, total: 4, items: [legacy.items[0], ...audits] } })
    const wrapper = mountRanking()
    await flushPromises()
    expect(wrapper.get('[data-testid="ranking-audit-total"]').text()).toContain('securityPolicyRanking.auditBlockCount')
    expect(wrapper.get('[data-testid="ranking-deduct-7"]').attributes()).toHaveProperty('disabled')
    await wrapper.get('[data-testid="ranking-records-7"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="ranking-record-101"]').text()).toContain('securityPolicyRanking.recordSources.upstream_policy')
    expect(wrapper.get('[data-testid="ranking-record-101"]').text()).toContain('securityPolicyRanking.usageRecordId')
    for (const audit of audits) {
      const row = wrapper.get(`[data-testid="ranking-record-audit-${audit.audit_id}"]`)
      expect(row.text()).toContain(`securityPolicyRanking.recordSources.${audit.source}`)
      expect(row.text()).toContain('securityPolicyRanking.auditRecordId')
      expect(row.text()).toContain('securityPolicyRanking.auditNotDeductible')
      expect(row.text()).not.toContain('securityPolicyRanking.usageRecordId')
    }
    expect(wrapper.find('[data-testid="ranking-record-0"]').exists()).toBe(false)
    expect(rankingMocks.preview).not.toHaveBeenCalled()
    expect(rankingMocks.deduct).not.toHaveBeenCalled()
  })

  it('rejects record responses after closing or changing the date window', async () => {
    const pending = deferred<{ data: ReturnType<typeof recordsData> }>()
    rankingMocks.getRecords.mockImplementationOnce(() => pending.promise)
    const wrapper = mountRanking()
    await flushPromises()
    await wrapper.get('[data-testid="ranking-records-7"]').trigger('click')
    const signal = rankingMocks.getRecords.mock.calls[0][2] as AbortSignal
    await wrapper.get('[data-testid="ranking-records-close"]').trigger('click')
    expect(signal.aborted).toBe(true)
    pending.resolve({ data: recordsData() })
    await flushPromises()
    expect(wrapper.find('[data-testid="ranking-records-dialog"]').exists()).toBe(false)

    const next = deferred<{ data: ReturnType<typeof recordsData> }>()
    rankingMocks.getRecords.mockImplementationOnce(() => next.promise)
    await wrapper.get('[data-testid="ranking-records-7"]').trigger('click')
    const nextSignal = rankingMocks.getRecords.mock.calls[1][2] as AbortSignal
    wrapper.findComponent({ name: 'DateRangePicker' }).vm.$emit('change', { startDate: '2026-10-01', endDate: '2026-10-02', preset: null })
    await flushPromises()
    expect(nextSignal.aborted).toBe(true)
    next.resolve({ data: recordsData() })
    await flushPromises()
    expect(wrapper.find('[data-testid="ranking-records-dialog"]').exists()).toBe(false)
  })

  it('switches record users and pages without allowing old responses to overwrite the active dialog', async () => {
    rankingMocks.getRanking.mockResolvedValue({ data: { ...rankingData(), items: [rankingData().items[0], { ...rankingData().items[0], user_id: 8, username: 'Second user' }] } })
    const first = deferred<{ data: ReturnType<typeof recordsData> }>()
    rankingMocks.getRecords.mockImplementationOnce(() => first.promise)
    const wrapper = mountRanking()
    await flushPromises()
    await wrapper.get('[data-testid="ranking-records-7"]').trigger('click')
    const firstSignal = rankingMocks.getRecords.mock.calls[0][2] as AbortSignal
    rankingMocks.getRecords.mockResolvedValueOnce({ data: { ...recordsData(), total: 21, pages: 2, items: [{ ...recordsData().items[0], group_name: 'Second user group' }] } })
    await wrapper.get('[data-testid="ranking-records-8"]').trigger('click')
    await flushPromises()
    expect(firstSignal.aborted).toBe(true)
    first.resolve({ data: { ...recordsData(), items: [{ ...recordsData().items[0], group_name: 'STALE RECORD GROUP' }] } })
    await flushPromises()
    expect(wrapper.get('[data-testid="ranking-records-dialog"]').text()).toContain('Second user group')
    expect(wrapper.text()).not.toContain('STALE RECORD GROUP')
    rankingMocks.getRecords.mockResolvedValueOnce({ data: { ...recordsData(), page: 2, total: 21, pages: 2, items: [{ ...recordsData().items[1], usage_id: 201 }] } })
    wrapper.get('[data-testid="ranking-records-dialog"]').findComponent({ name: 'Pagination' }).vm.$emit('update:page', 2)
    await flushPromises()
    expect(rankingMocks.getRecords).toHaveBeenLastCalledWith(8, expect.objectContaining({ page: 2, page_size: 20 }), expect.any(AbortSignal))
    expect(wrapper.find('[data-testid="ranking-record-201"]').exists()).toBe(true)
  })

  it('clears administrator record data on role changes and ignores the outstanding response', async () => {
    const pending = deferred<{ data: ReturnType<typeof recordsData> }>()
    rankingMocks.getRecords.mockImplementationOnce(() => pending.promise)
    const wrapper = mountRanking()
    await flushPromises()
    await wrapper.get('[data-testid="ranking-records-7"]').trigger('click')
    const signal = rankingMocks.getRecords.mock.calls[0][2] as AbortSignal
    rankingAuth.isAdmin = false
    await flushPromises()
    expect(signal.aborted).toBe(true)
    pending.resolve({ data: recordsData() })
    await flushPromises()
    expect(wrapper.find('[data-testid="ranking-records-dialog"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('Unselected group')
    expect(wrapper.find('[data-testid="ranking-settings"]').exists()).toBe(false)
    expect(rankingMocks.getPublicRanking).toHaveBeenCalledOnce()
  })

  it('bounds pagination independently of global table options and keeps custom dates fixed across midnight', async () => {
    const wrapper = mountRanking()
    await flushPromises()
    expect(wrapper.find('[data-testid="ranking-page-size"]').findAll('option').map(option => option.element.value)).toEqual(['10', '20', '50', '100'])
    wrapper.findComponent({ name: 'Pagination' }).vm.$emit('update:pageSize', 500)
    await flushPromises()
    expect(rankingMocks.getRanking.mock.calls.at(-1)?.[0].page_size).toBe(100)
    wrapper.findComponent({ name: 'DateRangePicker' }).vm.$emit('change', { startDate: '2026-10-01', endDate: '2026-10-02', preset: null })
    await flushPromises()
    vi.setSystemTime(new Date(2026, 9, 4, 0, 1))
    vi.advanceTimersByTime(5000)
    await flushPromises()
    expect(rankingMocks.getRanking.mock.calls.at(-1)?.[0]).toMatchObject({ start_date: '2026-10-01', end_date: '2026-10-02' })
    await wrapper.find('[data-testid="ranking-auto-refresh"]').trigger('click')
    const calls = rankingMocks.getRanking.mock.calls.length
    visibility.mockReturnValue('hidden')
    document.dispatchEvent(new Event('visibilitychange'))
    visibility.mockReturnValue('visible')
    document.dispatchEvent(new Event('visibilitychange'))
    await flushPromises()
    vi.advanceTimersByTime(30000)
    expect(rankingMocks.getRanking).toHaveBeenCalledTimes(calls)
  })

  it('confirms a server snapshot and reuses its ID after a network failure without recalculating from live rows', async () => {
    const wrapper = mountRanking()
    await flushPromises()
    await wrapper.find('[data-testid="ranking-deduct-7"]').trigger('click')
    await flushPromises()
    expect(rankingMocks.deduct).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="ranking-preview-formula"]').text()).toBe('3 triggers × 6.7 = 20.1')
    expect(wrapper.find('[data-testid="ranking-deduction-preview"]').text()).toContain('private@example.com')
    rankingMocks.getRanking.mockResolvedValue({ data: rankingData(10) })
    vi.advanceTimersByTime(5000)
    await flushPromises()
    expect(wrapper.find('[data-testid="ranking-preview-formula"]').text()).toBe('3 triggers × 6.7 = 20.1')
    rankingMocks.deduct.mockRejectedValueOnce(new Error('Connection lost'))
    await wrapper.find('[data-testid="ranking-confirm-deduct"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Connection lost')
    expect(wrapper.find('[data-testid="ranking-deduction-preview"]').exists()).toBe(true)
    await wrapper.find('[data-testid="ranking-confirm-deduct"]').trigger('click')
    await flushPromises()
    expect(rankingMocks.deduct.mock.calls).toEqual([[7, 'frozen-preview-7'], [7, 'frozen-preview-7']])
    expect(rankingMocks.preview).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[data-testid="ranking-deduction-preview"]').exists()).toBe(false)
  })

  it('disables settled/deleted rows and prevents overlapping deduction clicks', async () => {
    rankingMocks.getRanking.mockResolvedValue({ data: { ...rankingData(), items: [
      { ...rankingData().items[0], pending_count: 0 },
      { ...rankingData().items[0], user_id: 8, deleted: true },
      { ...rankingData().items[0], user_id: 9 },
    ] } })
    const wrapper = mountRanking()
    await flushPromises()
    expect(wrapper.find('[data-testid="ranking-deduct-7"]').attributes('disabled')).toBeDefined()
    expect(wrapper.find('[data-testid="ranking-deduct-8"]').attributes('disabled')).toBeDefined()
    const pending = deferred<{ data: ReturnType<typeof previewData> }>()
    rankingMocks.preview.mockImplementationOnce(() => pending.promise)
    await wrapper.find('[data-testid="ranking-deduct-9"]').trigger('click')
    await wrapper.find('[data-testid="ranking-deduct-9"]').trigger('click')
    expect(rankingMocks.preview).toHaveBeenCalledTimes(1)
    pending.resolve({ data: { ...previewData(), user_id: 9 } })
    await flushPromises()
    const deduction = deferred<{ data: ReturnType<typeof previewData> }>()
    rankingMocks.deduct.mockImplementationOnce(() => deduction.promise)
    await wrapper.find('[data-testid="ranking-confirm-deduct"]').trigger('click')
    await wrapper.find('[data-testid="ranking-confirm-deduct"]').trigger('click')
    expect(rankingMocks.deduct).toHaveBeenCalledTimes(1)
    deduction.resolve({ data: previewData() })
    await flushPromises()
  })
})

describe('PKBinance daily usage tab', () => {
  const mountedViews: Array<{ unmount: () => void }> = []
  let visibility: ReturnType<typeof vi.spyOn>

  const mountDailyUsage = () => {
    const wrapper = mount(AdminDailyUsage, {
      global: { stubs: {
        AppLayout: AppLayoutStub, UsageStatsCards: true, UsageFilters: UsageFiltersStub,
        UsageTable: UsageTableStub, UsageExportProgress: true, UsageCleanupDialog: true,
        UserBalanceHistoryModal: true, Pagination: true, Select: true,
        DateRangePicker: true, Icon: true, TokenUsageTrend: true,
        ModelDistributionChart: true, GroupDistributionChart: true,
        EndpointDistributionChart: true, UserTokenRanking: true,
        OpsErrorLogTable: true, OpsErrorDetailModal: true,
      } },
    })
    mountedViews.push(wrapper)
    return wrapper
  }

  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date(2026, 9, 1, 12))
    visibility = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
    Object.keys(routeQuery).forEach((key) => delete routeQuery[key])
    list.mockReset().mockResolvedValue({ items: [], total: 0, pages: 0 })
    getStats.mockReset().mockResolvedValue({ total_requests: 0 })
    getSnapshotV2.mockReset().mockResolvedValue({ trend: [], groups: [] })
    getModelStats.mockReset().mockResolvedValue({ models: [] })
    listErrorLogs.mockReset().mockResolvedValue({ items: [], total: 0 })
    getById.mockReset()
  })

  afterEach(() => {
    mountedViews.splice(0).forEach((wrapper) => wrapper.unmount())
    Object.keys(routeQuery).forEach((key) => delete routeQuery[key])
    vi.restoreAllMocks()
    vi.useRealTimers()
  })

  it('defaults to today and all users without inheriting recharge filters or nesting AppLayout', async () => {
    routeQuery.user_id = '42'
    routeQuery.start_date = '2020-01-01'
    routeQuery.end_date = '2020-02-01'
    const wrapper = mountDailyUsage()
    vi.advanceTimersByTime(120)
    await flushPromises()

    const today = formatLocalDate(new Date())
    for (const request of [list, getStats, getModelStats, getSnapshotV2]) {
      const params = request.mock.calls[0][0]
      expect(params.start_date).toBe(today)
      expect(params.end_date).toBe(today)
      expect(params.user_id).toBeUndefined()
      expect(params).not.toHaveProperty('recharge_type')
      expect(params).not.toHaveProperty('payment_source')
    }
    expect(getById).not.toHaveBeenCalled()
    expect(wrapper.findComponent(AppLayoutStub).exists()).toBe(false)
    expect(wrapper.find('[data-test="usage-table"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="daily-usage-auto-refresh"]').element).toHaveProperty('checked', true)
    expect(wrapper.find('[data-testid="daily-usage-refresh-seconds"]').element).toHaveProperty('value', '5')
    expect(wrapper.text()).toContain('binanceRecharge.dailyUsage.description')
  })

  it('refreshes every five seconds by default and supports switching off and changing the interval', async () => {
    const wrapper = mountDailyUsage()
    vi.advanceTimersByTime(120)
    await flushPromises()
    expect(list).toHaveBeenCalledTimes(1)
    vi.advanceTimersByTime(4879)
    await flushPromises()
    expect(list).toHaveBeenCalledTimes(1)
    vi.advanceTimersByTime(1)
    await flushPromises()
    expect(list).toHaveBeenCalledTimes(2)

    await wrapper.find('[data-testid="daily-usage-auto-refresh"]').setValue(false)
    vi.advanceTimersByTime(20000)
    await flushPromises()
    expect(list).toHaveBeenCalledTimes(2)
    await wrapper.find('[data-testid="daily-usage-refresh-seconds"]').setValue('3')
    await wrapper.find('[data-testid="daily-usage-auto-refresh"]').setValue(true)
    vi.advanceTimersByTime(2999)
    await flushPromises()
    expect(list).toHaveBeenCalledTimes(2)
    vi.advanceTimersByTime(1)
    await flushPromises()
    expect(list).toHaveBeenCalledTimes(3)

    await wrapper.find('[data-testid="daily-usage-refresh-seconds"]').setValue('0')
    vi.advanceTimersByTime(20000)
    await flushPromises()
    expect(list).toHaveBeenCalledTimes(3)
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
  })

  it('waits for a slow refresh to finish instead of accumulating polling requests', async () => {
    mountDailyUsage()
    vi.advanceTimersByTime(120)
    await flushPromises()
    let resolveStats!: (value: { total_requests: number }) => void
    getStats.mockImplementationOnce(() => new Promise((resolve) => { resolveStats = resolve }))
    vi.advanceTimersByTime(4880)
    await flushPromises()
    expect(list).toHaveBeenCalledTimes(2)
    expect(getStats).toHaveBeenCalledTimes(2)
    vi.advanceTimersByTime(30000)
    await flushPromises()
    expect(list).toHaveBeenCalledTimes(2)
    expect(getStats).toHaveBeenCalledTimes(2)
    resolveStats({ total_requests: 1 })
    await flushPromises()
    vi.advanceTimersByTime(5000)
    await flushPromises()
    expect(list).toHaveBeenCalledTimes(3)
  })

  it('cancels requests and polling while hidden, resumes once visible and clears timers on unmount', async () => {
    const wrapper = mountDailyUsage()
    vi.advanceTimersByTime(120)
    await flushPromises()
    const signals = [list, getStats, getModelStats, getSnapshotV2].map((request) => request.mock.calls[0][1].signal as AbortSignal)
    visibility.mockReturnValue('hidden')
    document.dispatchEvent(new Event('visibilitychange'))
    expect(signals.every((signal) => signal.aborted)).toBe(true)
    vi.advanceTimersByTime(30000)
    await flushPromises()
    expect(list).toHaveBeenCalledTimes(1)

    visibility.mockReturnValue('visible')
    document.dispatchEvent(new Event('visibilitychange'))
    await flushPromises()
    expect(list).toHaveBeenCalledTimes(2)
    const latestSignal = list.mock.calls[1][1].signal as AbortSignal
    wrapper.unmount()
    mountedViews.splice(mountedViews.indexOf(wrapper), 1)
    expect(latestSignal.aborted).toBe(true)
    vi.advanceTimersByTime(30000)
    await flushPromises()
    expect(list).toHaveBeenCalledTimes(2)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('does not refresh on becoming visible when automatic refresh is off', async () => {
    const wrapper = mountDailyUsage()
    vi.advanceTimersByTime(120)
    await flushPromises()
    await wrapper.find('[data-testid="daily-usage-auto-refresh"]').setValue(false)
    visibility.mockReturnValue('hidden')
    document.dispatchEvent(new Event('visibilitychange'))
    visibility.mockReturnValue('visible')
    document.dispatchEvent(new Event('visibilitychange'))
    await flushPromises()
    vi.advanceTimersByTime(20000)
    await flushPromises()
    expect(list).toHaveBeenCalledTimes(1)
  })

  it('preserves the standalone usage page default date range and does not auto-refresh it', async () => {
    const today = new Date()
    const wrapper = mountRouteFilteredUsageView()
    mountedViews.push(wrapper)
    vi.advanceTimersByTime(120)
    await flushPromises()
    expect(list.mock.calls[0][0].start_date).toBe(new Date(today.getTime() - 86400000).toISOString())
    expect(list.mock.calls[0][0].end_date).toBe(today.toISOString())
    vi.advanceTimersByTime(30000)
    await flushPromises()
    expect(list).toHaveBeenCalledTimes(1)
    expect(wrapper.findComponent(AppLayoutStub).exists()).toBe(true)
  })
})


describe('observer own usage view', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.clearAllMocks()
    Object.keys(routeQuery).forEach(key => delete routeQuery[key])
    own.settings.allow_user_view_error_requests = true
    own.list.mockResolvedValue({ items: [], total: 0 })
    own.getStats.mockResolvedValue({ total_requests: 2, total_account_cost: 3 })
    own.getModelStats.mockResolvedValue({ models: [] })
    own.getSnapshotV2.mockResolvedValue({ trend: [], groups: [] })
    own.listErrors.mockResolvedValue({ items: [], total: 0 })
  })
  afterEach(() => { vi.useRealTimers() })
  const mountObserver = (realDatePickerAndCards = false) => mount(UsageView, {
    props: { observerMode: true },
    global: { stubs: {
      AppLayout: AppLayoutStub, UsageStatsCards: !realDatePickerAndCards, UsageFilters: UsageFiltersStub,
      UsageTable: UsageTableStub, UsageExportProgress: true, UsageCleanupDialog: true,
      UserBalanceHistoryModal: true, Pagination: true, Select: true, DateRangePicker: !realDatePickerAndCards,
      Icon: true, TokenUsageTrend: true, ModelDistributionChart: true, GroupDistributionChart: true,
      EndpointDistributionChart: true, UserTokenRanking: true, OpsErrorLogTable: true, OpsErrorDetailModal: true,
      LatencyObservabilityPanel: LatencyObservabilityPanelStub,
    } },
  })
  it.each([
    { startDate: '', endDate: '2026-11-05', start: '2026-11-03', end: '2026-11-05', minutes: 4320 },
    { startDate: '2026-11-04', endDate: '', start: '2026-11-04', end: '2026-11-10', minutes: 9030 },
    { startDate: '', endDate: '', start: '2026-11-03', end: '2026-11-10', minutes: 10470 },
  ])('uses the observer stats range after clearing picker dates: %j', async ({ startDate, endDate, start, end, minutes }) => {
    const now = new Date(2026, 10, 10, 6, 30)
    vi.setSystemTime(now)
    const wrapper = mountObserver(true)
    await flushPromises()
    const picker = wrapper.findComponent(DateRangePicker)
    await picker.get('.date-picker-trigger').trigger('click')
    const inputs = picker.findAll('.date-picker-input')
    await inputs[0].setValue(startDate)
    await inputs[1].setValue(endDate)
    own.getStats.mockResolvedValueOnce({ total_requests: minutes, total_tokens: minutes * 100 })
    await picker.get('.date-picker-apply').trigger('click')
    await flushPromises()
    expect(own.getStats).toHaveBeenLastCalledWith(expect.objectContaining({ start_date: start, end_date: end }))
    expect(wrapper.findComponent(UsageStatsCards).props('rateWindow')).toEqual({ start, end, now: now.getTime() })
    expect(wrapper.get('[data-testid="usage-rpm"]').text()).toBe('1.00')
    expect(wrapper.get('[data-testid="usage-tpm"]').text()).toBe('100.0')
    expect(getStats).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('keeps old stats on their window while pending or failed and discards out-of-order responses', async () => {
    const now = new Date(2026, 10, 10, 6, 30)
    vi.setSystemTime(now)
    const wrapper = mountObserver()
    await flushPromises()
    const cards = () => wrapper.findComponent(UsageStatsCards)
    const originalStats = { ...cards().props('stats')! }
    const originalWindow = { ...cards().props('rateWindow')! }
    let resolveA!: (stats: unknown) => void
    let resolveB!: (stats: unknown) => void
    own.getStats.mockReturnValueOnce(new Promise(resolve => { resolveA = resolve }))
    own.getStats.mockReturnValueOnce(new Promise(resolve => { resolveB = resolve }))

    ;(wrapper.vm as any).onDateRangeChange({ startDate: '', endDate: '2026-11-05', preset: null })
    await flushPromises()
    expect(own.getStats).toHaveBeenLastCalledWith(expect.objectContaining({ start_date: '2026-11-03', end_date: '2026-11-05' }))
    expect(cards().props('stats')).toEqual(originalStats)
    expect(cards().props('rateWindow')).toEqual(originalWindow)

    const newerNow = new Date(2026, 10, 11, 7)
    vi.setSystemTime(newerNow)
    ;(wrapper.vm as any).onDateRangeChange({ startDate: '2026-11-04', endDate: '', preset: null })
    await flushPromises()
    expect(own.getStats).toHaveBeenLastCalledWith(expect.objectContaining({ start_date: '2026-11-04', end_date: '2026-11-11' }))
    expect(cards().props('stats')).toEqual(originalStats)
    expect(cards().props('rateWindow')).toEqual(originalWindow)

    const newerStats = { total_requests: 21000, total_tokens: 42000 }
    const newerWindow = { start: '2026-11-04', end: '2026-11-11', now: newerNow.getTime() }
    vi.setSystemTime(new Date(2026, 10, 12, 8))
    resolveB(newerStats)
    await flushPromises()
    expect(cards().props('stats')).toEqual(newerStats)
    expect(cards().props('rateWindow')).toEqual(newerWindow)
    expect(usagePerMinute(cards().props('stats')!.total_requests, cards().props('rateWindow'))).toBe(2)

    resolveA({ total_requests: 4320, total_tokens: 432000 })
    await flushPromises()
    expect(cards().props('stats')).toEqual(newerStats)
    expect(cards().props('rateWindow')).toEqual(newerWindow)

    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {})
    try {
      own.getStats.mockRejectedValueOnce(new Error('fixture stats unavailable'))
      ;(wrapper.vm as any).onDateRangeChange({ startDate: '', endDate: '', preset: null })
      await flushPromises()
      expect(consoleError).toHaveBeenCalled()
      expect(cards().props('stats')).toEqual(newerStats)
      expect(cards().props('rateWindow')).toEqual(newerWindow)
    } finally {
      consoleError.mockRestore()
      wrapper.unmount()
    }
  })

  it('uses own APIs for filters, charts and export, without admin actions', async () => {
    routeQuery.user_id = '999'
    const wrapper = mountObserver()
    await vi.advanceTimersByTimeAsync(150)
    await flushPromises()
    expect(own.list).toHaveBeenCalledWith(expect.objectContaining({ user_id: undefined }), expect.anything())
    expect(own.getStats).toHaveBeenCalled()
    expect(own.getModelStats).toHaveBeenCalled()
    expect(own.getSnapshotV2).toHaveBeenCalled()
    expect(wrapper.find('usage-cleanup-dialog-stub').exists()).toBe(false)
    expect(wrapper.find('user-balance-history-modal-stub').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('usage.tabs.ranking')
    expect(wrapper.text()).not.toContain('connections.title')
    expect(wrapper.text()).not.toContain('latencyObservability.title')
    for (const name of ['model-distribution-chart-stub', 'group-distribution-chart-stub', 'endpoint-distribution-chart-stub']) {
      expect(wrapper.findComponent(name).props('enableBreakdown')).toBe(false)
    }
    await wrapper.find('.user-click').trigger('click')
    const filter = wrapper.findComponent(UsageFiltersStub)
    filter.vm.$emit('update:modelValue', { account_id: 7, group_id: 8, api_key_id: 9, model: 'gpt', start_date: '2026-09-01', end_date: '2026-09-02' })
    filter.vm.$emit('change')
    await flushPromises()
    expect(own.list).toHaveBeenLastCalledWith(expect.objectContaining({ account_id: 7, group_id: 8, api_key_id: 9 }), expect.anything())
    filter.vm.$emit('cleanup')
    filter.vm.$emit('export')
    await flushPromises()
    expect(own.list).toHaveBeenLastCalledWith(expect.objectContaining({ page_size: 100, exact_total: true, account_id: 7 }), expect.anything())
    expect(saveAs).toHaveBeenCalled()
    const errorTab = wrapper.findAll('[data-testid="usage-detail-tab"]').find(tab => tab.text() === 'usage.tabs.errors')!
    await errorTab.trigger('click')
    await flushPromises()
    expect(own.listErrors).toHaveBeenCalledWith(expect.objectContaining({ start_date: '2026-09-01', account_id: 7, group_id: 8 }))
    for (const fn of [list, exportList, getStats, getModelStats, getSnapshotV2, getById, listErrorLogs]) expect(fn).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  it('honors the user error visibility switch', async () => {
    own.settings.allow_user_view_error_requests = false
    const wrapper = mountObserver()
    await vi.advanceTimersByTimeAsync(150)
    await flushPromises()
    expect(wrapper.findAll('[data-testid="usage-detail-tab"]')).toHaveLength(1)
    expect(own.listErrors).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})

describe('admin UsageView rolling 24 hour requests', () => {
  let wrapper: ReturnType<typeof mountRouteFilteredUsageView> | undefined
  const now = new Date('2026-10-01T00:01:02.123+09:00')

  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(now)
    Object.keys(routeQuery).forEach(key => delete routeQuery[key])
    list.mockReset().mockResolvedValue({ items: [], total: 0, pages: 0 })
    exportList.mockReset().mockResolvedValue({ items: [], total: 0, pages: 0 })
    getStats.mockReset().mockResolvedValue({ total_requests: 2, total_cost: 12, total_actual_cost: 3, total_account_cost: 5 })
    getModelStats.mockReset().mockResolvedValue({ models: [] })
    getSnapshotV2.mockReset().mockResolvedValue({ trend: [], groups: [] })
    listErrorLogs.mockReset().mockResolvedValue({ items: [], total: 0 })
  })
  afterEach(() => {
    wrapper?.unmount()
    wrapper = undefined
    Object.keys(routeQuery).forEach(key => delete routeQuery[key])
    vi.useRealTimers()
  })

  const expectRequests = (end: Date) => {
    const range = { start_date: new Date(end.getTime() - 86_400_000).toISOString(), end_date: end.toISOString() }
    for (const request of [list, getStats, getModelStats, getSnapshotV2]) {
      expect(request.mock.lastCall?.[0]).toMatchObject(range)
      const params = request.mock.lastCall![0]
      expect(Date.parse(params.end_date) - Date.parse(params.start_date)).toBe(86_400_000)
    }
    expect(getSnapshotV2.mock.lastCall?.[0].granularity).toBe('hour')
    return range
  }

  it.each([
    ['rolling', undefined, undefined],
    ['routed exact', '2026-09-30T00:00:00Z', '2026-10-01T00:00:00Z'],
    ['mixed', '2026-09-30', '2026-10-01T00:00:00Z'],
  ])('blocks %s cleanup until calendar dates are explicitly selected', async (_, start, end) => {
    if (start) routeQuery.start_date = start
    if (end) routeQuery.end_date = end
    listCleanupTasks.mockReset().mockResolvedValue({ items: [], total: 0 })
    createCleanupTask.mockReset()
    showWarning.mockClear()
    wrapper = mountRouteFilteredUsageView(true, true)
    await flushPromises()
    await wrapper.get('[data-test="cleanup-open"]').trigger('click')
    await flushPromises()
    expect(wrapper.findAll('button').some(b => b.text() === 'admin.usage.cleanup.submit')).toBe(false)
    expect(wrapper.find('[data-test="cleanup-confirm"]').exists()).toBe(false)
    expect(listCleanupTasks).not.toHaveBeenCalled()
    expect(createCleanupTask).not.toHaveBeenCalled()
    expect(showWarning).toHaveBeenCalledWith('admin.usage.cleanup.calendarRangeRequired')
  })

  it('submits unchanged natural dates after an explicit picker selection', async () => {
    listCleanupTasks.mockReset().mockResolvedValue({ items: [], total: 0 })
    createCleanupTask.mockReset().mockResolvedValue({ id: 1 })
    wrapper = mountRouteFilteredUsageView(true, true)
    await flushPromises()
    await wrapper.get('[data-test="cleanup-open"]').trigger('click')
    const picker = wrapper.findComponent(DateRangePicker)
    await picker.get('.date-picker-trigger').trigger('click')
    await picker.findAll('.date-picker-preset').find(b => b.text() === 'dates.today')!.trigger('click')
    await picker.get('.date-picker-apply').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="cleanup-open"]').trigger('click')
    await flushPromises()
    const dialog = wrapper.findComponent(UsageCleanupDialog)
    const today = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`
    expect(dialog.props()).toMatchObject({ show: true, startDate: today, endDate: today })
    await dialog.findAll('button').find(b => b.text() === 'admin.usage.cleanup.submit')!.trigger('click')
    await wrapper.get('[data-test="cleanup-confirm"]').trigger('click')
    await flushPromises()
    expect(createCleanupTask).toHaveBeenCalledOnce()
    expect(createCleanupTask).toHaveBeenCalledWith({ start_date: today, end_date: today, timezone: Intl.DateTimeFormat().resolvedOptions().timeZone })
  })

  it('uses the same precise cross-midnight window for list, cards and charts on initial load', async () => {
    wrapper = mountRouteFilteredUsageView(true)
    vi.advanceTimersByTime(120)
    await flushPromises()
    expectRequests(now)
    expect(wrapper.findComponent(DateRangePicker).get('.date-picker-trigger').text()).toContain('dates.last24Hours')
    expect(wrapper.findComponent(UsageStatsCards).props('stats')).toMatchObject({
      total_actual_cost: 3, total_cost: 12, total_account_cost: 5
    })
  })

  it('refreshes the rolling window once and keeps pagination/export on that applied snapshot', async () => {
    wrapper = mountRouteFilteredUsageView()
    await flushPromises()
    const refreshedAt = new Date('2026-10-02T00:00:00.456+09:00')
    vi.setSystemTime(refreshedAt)
    ;(wrapper.vm as any).refreshData()
    await flushPromises()
    const range = expectRequests(refreshedAt)
    vi.setSystemTime(new Date('2026-10-02T00:05:00.789+09:00'))
    ;(wrapper.vm as any).handlePageChange(2)
    await flushPromises()
    expect(list.mock.lastCall?.[0]).toMatchObject({ ...range, page: 2 })
    await (wrapper.vm as any).exportToExcel()
    expect(exportList.mock.lastCall?.[0]).toMatchObject(range)
    expect((wrapper.vm as any).connectionFilters).toMatchObject({ start_time: range.start_date, end_time: range.end_date })
    ;(wrapper.vm as any).switchTab('errors')
    await flushPromises()
    expect(listErrorLogs.mock.lastCall?.[0]).toMatchObject({ start_time: range.start_date, end_time: range.end_date })
  })

  it('applies the actual picker selection after midnight and resets back to rolling time', async () => {
    routeQuery.start_date = '2026-09-20'
    routeQuery.end_date = '2026-09-25'
    wrapper = mountRouteFilteredUsageView(true)
    await flushPromises()
    const picker = wrapper.findComponent(DateRangePicker)
    await picker.get('.date-picker-trigger').trigger('click')
    await picker.findAll('.date-picker-preset').find(b => b.text() === 'dates.last24Hours')!.trigger('click')
    await picker.get('.date-picker-apply').trigger('click')
    await flushPromises()
    expectRequests(now)
    const resetAt = new Date('2026-10-02T00:00:00.789+09:00')
    vi.setSystemTime(resetAt)
    ;(wrapper.vm as any).resetFilters()
    await flushPromises()
    expectRequests(resetAt)
  })

  it('preserves manual/routed calendar dates on refresh', async () => {
    routeQuery.start_date = '2026-09-30'
    routeQuery.end_date = '2026-10-01'
    wrapper = mountRouteFilteredUsageView(true)
    await flushPromises()
    ;(wrapper.vm as any).refreshData()
    await flushPromises()
    for (const request of [list, getStats, getModelStats, getSnapshotV2]) {
      expect(request.mock.lastCall?.[0]).toMatchObject({ start_date: '2026-09-30', end_date: '2026-10-01' })
    }
    expect(wrapper.findComponent(DateRangePicker).get('.date-picker-trigger').text()).not.toContain('dates.last24Hours')
  })
})


const auditMountedViews: ReturnType<typeof mount>[] = []
const auditSettingsData = (): SecurityPolicyRankingSettings => ({ multiplier: '6.700000', auto_disable_enabled: false, auto_disable_threshold: 10, reset_on_manual_enable: true, group_ids: [], user_allowlist_ids: [], group_rules: [], settings_revision: 1 })
function configData(ranking: SecurityPolicyRankingSettings = auditSettingsData()): AuditCenterConfig {
  return {
    revision: 'saved-revision', risk_control_enabled: true, differences: [], groups: [], charge_summary: [],
    content: { enabled: true, mode: 'monitor_only', group_ids: [...ranking.group_ids], user_allowlist_ids: [...ranking.user_allowlist_ids ?? []], risk_policy: null, blocked_keywords: [], thresholds: {} },
    prompt: { enabled: false, blocking_enabled: false, blocking_latest_turn_only: false, store_pass_events: false, strategy: 'priority', worker_count: 4, queue_capacity: 100, scanners: [], group_ids: [...ranking.group_ids], user_allowlist_ids: [...ranking.user_allowlist_ids ?? []], endpoints: [], config_version: 1, engine: 'local_rules', local_rules: [], risk_policy: null },
    ranking,
  } as AuditCenterConfig
}

const ToggleStub = { props: ['modelValue'], emits: ['update:modelValue'], template: '<input type="checkbox" :checked="modelValue" @change="$emit(\'update:modelValue\', $event.target.checked)" />' }
async function mountCenter(ranking = auditSettingsData()) {
  let saved = configData(ranking)
  auditCenterMocks.getConfig.mockResolvedValue(saved)
  auditCenterMocks.updateConfig.mockImplementation(async (payload: AuditCenterUpdate) => {
    saved = structuredClone(saved)
    if (payload.ranking) saved.ranking = structuredClone(payload.ranking)
    if (payload.shared) {
      for (const source of ['content', 'prompt', 'ranking'] as const) Object.assign(saved[source], payload.shared)
    }
    return saved
  })
  const wrapper = mount(AuditCenterView, { global: { stubs: {
    AppLayout: { template: '<div><slot /></div>' }, Toggle: ToggleStub, UserAllowlistSelector: true,
    RiskControlView: true, PromptAuditView: true, LocalRulePanel: true, EndpointPool: true, PolicyPanel: true, AuditRiskPolicyPanel: true,
  } } })
  auditMountedViews.push(wrapper)
  await flushPromises()
  return wrapper
}
async function save(wrapper: ReturnType<typeof mount>) {
  await wrapper.get('[data-test="audit-save-all"]').trigger('click')
  await flushPromises()
}
// These settings moved out of the ranking table into the shared Audit Center.
// Keep the previous validation, scope, precision and explicit-removal contracts here.
describe('AuditCenter shared ranking settings', () => {
beforeEach(() => {
  vi.clearAllMocks()
  for (const key of Object.keys(routeQuery)) delete routeQuery[key]
  auditCenterMocks.listGroups.mockResolvedValue([
    { id: 1, name: 'Selected group', platform: 'openai', status: 'active' },
    { id: 2, name: 'Unselected group', platform: 'openai', status: 'inactive' },
  ])
})

  afterEach(() => { auditMountedViews.splice(0).forEach(wrapper => wrapper.unmount()) })

  it('loads saved automatic disable options and does not overwrite unrelated policies', async () => {
    const wrapper = await mountCenter()
    expect(wrapper.get('[data-test="audit-upstream-minimum-input"]').element).toHaveProperty('value', '6.700000')
    expect(wrapper.get('[data-test="audit-auto-disable"]').element).toHaveProperty('checked', false)
    expect(wrapper.get('[data-test="audit-reset-on-enable"]').element).toHaveProperty('checked', true)
    expect(auditCenterMocks.updateConfig).not.toHaveBeenCalled()
  })

  it('saves exact multiplier and automatic disable options, rejecting invalid input', async () => {
    const wrapper = await mountCenter()
    await wrapper.get('[data-test="audit-upstream-minimum-input"]').setValue('1e2')
    await save(wrapper)
    expect(auditCenterMocks.updateConfig).not.toHaveBeenCalled()
    expect(auditCenterMocks.showError).toHaveBeenCalledWith('securityPolicyRanking.invalidMultiplier')
    await wrapper.get('[data-test="audit-upstream-minimum-input"]').setValue('0.290001')
    await wrapper.get('[data-test="audit-auto-disable"]').setValue(true)
    await wrapper.get('[data-test="audit-disable-threshold"]').setValue('1000000000')
    await wrapper.get('[data-test="audit-reset-on-enable"]').setValue(false)
    await save(wrapper)
    expect(auditCenterMocks.updateConfig).toHaveBeenCalledWith({ expected_revision: 'saved-revision', ranking: { ...auditSettingsData(), multiplier: '0.290001', auto_disable_enabled: true, auto_disable_threshold: 1000000000, reset_on_manual_enable: false } })
  })

  it.each([0, 1.5, 1000000001])('rejects invalid automatic disable threshold %s', async threshold => {
    const wrapper = await mountCenter()
    await wrapper.get('[data-test="audit-disable-threshold"]').setValue(threshold)
    await save(wrapper)
    expect(auditCenterMocks.updateConfig).not.toHaveBeenCalled()
    expect(auditCenterMocks.showError).toHaveBeenCalledWith('securityPolicyRanking.invalidThreshold')
  })

  it('saves only selected detection groups and clears the scope explicitly without replacing ranking settings', async () => {
    const wrapper = await mountCenter()
    await wrapper.get('[data-test="audit-shared-group-1"]').setValue(true)
    await save(wrapper)
    expect(auditCenterMocks.updateConfig).toHaveBeenLastCalledWith({ expected_revision: 'saved-revision', shared: { group_ids: [1] } })
    await wrapper.get('[data-test="audit-shared-group-1"]').setValue(false)
    await save(wrapper)
    expect(auditCenterMocks.updateConfig).toHaveBeenLastCalledWith({ expected_revision: 'saved-revision', shared: { group_ids: [] } })
  })

  it('preserves saved inactive and missing groups until explicitly removed and can search by group ID', async () => {
    const wrapper = await mountCenter({ ...auditSettingsData(), group_ids: [2, 99] })
    expect(wrapper.get('[data-test="audit-shared-group-2"]').attributes()).not.toHaveProperty('disabled')
    expect(wrapper.get('[data-test="audit-missing-group-99"]').element).toHaveProperty('checked', true)
    const search = wrapper.get('input[placeholder="admin.auditCenter.searchGroups"]')
    await search.setValue('1')
    expect(wrapper.find('[data-test="audit-shared-group-1"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="audit-shared-group-2"]').exists()).toBe(false)
    await search.setValue('')
    await wrapper.get('[data-test="audit-shared-group-2"]').setValue(false)
    await wrapper.get('[data-test="audit-missing-group-99"]').setValue(false)
    await save(wrapper)
    expect(auditCenterMocks.updateConfig).toHaveBeenLastCalledWith({ expected_revision: 'saved-revision', shared: { group_ids: [] } })
  })

  it('preserves group rules outside detection scope and saves auto deductions independently from automatic disabling', async () => {
    const rule = { group_id: 1, multiplier: '0.290001', auto_deduct_enabled: false }
    const wrapper = await mountCenter({ ...auditSettingsData(), group_ids: [1], group_rules: [rule], user_allowlist_ids: [7, 9] })
    expect(wrapper.findComponent(UserAllowlistSelector).props('modelValue')).toEqual([7, 9])
    await wrapper.get('[data-test="audit-charge-group-1"]').setValue(true)
    const multiplier = wrapper.get('[data-test="audit-group-multiplier-1"]')
    expect(multiplier.element).toHaveProperty('value', '0.290001')
    expect(multiplier.classes()).toEqual(expect.arrayContaining(['!text-red-700', 'dark:!text-red-300']))
    expect(multiplier.element.closest('label')?.textContent).toContain('破甲倍率')
    await wrapper.get('[data-test="audit-shared-group-1"]').setValue(false)
    await save(wrapper)
    expect(auditCenterMocks.updateConfig).toHaveBeenLastCalledWith(expect.objectContaining({ shared: { group_ids: [] }, ranking: expect.objectContaining({ auto_disable_enabled: false, user_allowlist_ids: [7, 9], group_rules: [{ ...rule, auto_deduct_enabled: true }] }) }))
    expect(rule.auto_deduct_enabled).toBe(false)
    wrapper.findComponent(UserAllowlistSelector).vm.$emit('update:modelValue', [])
    await wrapper.get('[data-test="audit-clear-rule-1"]').trigger('click')
    await save(wrapper)
    expect(auditCenterMocks.updateConfig).toHaveBeenLastCalledWith(expect.objectContaining({ shared: { user_allowlist_ids: [] }, ranking: expect.objectContaining({ group_rules: [] }) }))
  })

  it.each(['0', '0.0000001', '1000.000001', '1e2', '-1', 'not a number'])('rejects invalid group multiplier %s even when the group is not selected', async multiplier => {
    const wrapper = await mountCenter({ ...auditSettingsData(), group_rules: [{ group_id: 1, multiplier: '1', auto_deduct_enabled: true }] })
    await wrapper.get('[data-test="audit-group-multiplier-1"]').setValue(multiplier)
    await save(wrapper)
    expect(auditCenterMocks.updateConfig).not.toHaveBeenCalled()
    expect(auditCenterMocks.showError).toHaveBeenCalledWith('securityPolicyRanking.invalidGroupMultiplier')
  })

  it('keeps exact boundary decimals and removes missing rules only on explicit action', async () => {
    const missing = { group_id: 99, multiplier: '1000.000000', auto_deduct_enabled: true }
    const wrapper = await mountCenter({ ...auditSettingsData(), group_ids: [1], group_rules: [missing] })
    await wrapper.get('[data-test="audit-charge-group-1"]').setValue(true)
    await wrapper.get('[data-test="audit-group-multiplier-1"]').setValue('0.000001')
    await wrapper.get('[data-test="audit-charge-group-1"]').setValue(false)
    await save(wrapper)
    expect(auditCenterMocks.updateConfig).toHaveBeenLastCalledWith(expect.objectContaining({ ranking: expect.objectContaining({ group_rules: [missing, { group_id: 1, multiplier: '0.000001', auto_deduct_enabled: false }] }) }))
    await wrapper.get('[data-test="audit-clear-rule-99"]').trigger('click')
    await save(wrapper)
    expect(auditCenterMocks.updateConfig).toHaveBeenLastCalledWith(expect.objectContaining({ ranking: expect.objectContaining({ group_rules: [{ group_id: 1, multiplier: '0.000001', auto_deduct_enabled: false }] }) }))
    expect(wrapper.find('[data-test="audit-clear-rule-99"]').exists()).toBe(false)
  })
})
