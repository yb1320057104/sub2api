import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import type { DOMWrapper, VueWrapper } from '@vue/test-utils'

import RiskControlView from '../RiskControlView.vue'
import type { ContentModerationAPIKeyStatus, ContentModerationConfig, UpdateContentModerationConfig } from '@/api/admin/riskControl'
import { CONTENT_AUDIT_KEYWORD_TEMPLATES } from '@/utils/contentAuditKeywordTemplates'
import UserAllowlistSelector from '@/components/admin/user/UserAllowlistSelector.vue'
import { defaultAuditRiskPolicy } from '@/types/auditRiskPolicy'

const userMocks = vi.hoisted(() => ({ list: vi.fn(), getById: vi.fn() }))
vi.mock('@/api/admin/users', () => ({ default: userMocks }))

const {
  getConfig,
  updateConfig,
  getStatus,
  listLogs,
  getGroups,
  getProxies,
  testAPIKeys,
  showError,
  showSuccess,
} = vi.hoisted(() => ({
  getConfig: vi.fn(),
  updateConfig: vi.fn(),
  getStatus: vi.fn(),
  listLogs: vi.fn(),
  getGroups: vi.fn(),
  getProxies: vi.fn(),
  testAPIKeys: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    riskControl: {
      getConfig,
      updateConfig,
      getStatus,
      listLogs,
      testAPIKeys,
      deleteFlaggedHash: vi.fn(),
      clearFlaggedHashes: vi.fn(),
      unbanUser: vi.fn(),
    },
    groups: {
      getAll: getGroups,
    },
    proxies: {
      getAll: getProxies,
    },
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError,
    showSuccess,
  }),
}))

vi.mock('@/utils/apiError', () => ({
  extractApiErrorMessage: (_err: unknown, fallback: string) => fallback,
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, string | number>) => {
        if (key === 'admin.riskControl.preBlockAPIKeyLoadSummary') {
          return `同步并发 ${params?.active} / 可用 Key ${params?.available}，累计 ${params?.total} 次，worker：${params?.workerActive} / ${params?.workerTotal}`
        }
        return key.replace(/\{(\w+)\}/g, (_, token) => String(params?.[token] ?? `{${token}}`))
      },
    }),
  }
})

const baseConfig = (): ContentModerationConfig => ({
  enabled: true,
  mode: 'pre_block',
  base_url: 'https://api.openai.com',
  model: 'omni-moderation-latest',
  proxy_id: null,
  api_key_configured: false,
  api_key_masked: '',
  api_key_count: 0,
  api_key_masks: [],
  api_key_statuses: [],
  timeout_ms: 3000,
  sample_rate: 100,
  all_groups: false,
  group_ids: [],
  record_non_hits: false,
  worker_count: 4,
  queue_size: 32768,
  block_status: 403,
  block_message: '内容审计命中风险规则，请调整输入后重试',
  email_on_hit: true,
  auto_ban_enabled: true,
  ban_threshold: 10,
  violation_window_hours: 720,
  retry_count: 2,
  hit_retention_days: 180,
  non_hit_retention_days: 3,
  pre_hash_check_enabled: false,
  blocked_keywords: [],
  keyword_blocking_mode: 'keyword_and_api',
  thresholds: {
    harassment: 0.98,
    sexual: 0.65,
  },
  model_filter: {
    type: 'all',
    models: [],
  },
})

const runtimeStatus = () => ({
  enabled: true,
  risk_control_enabled: true,
  mode: 'pre_block',
  worker_count: 4,
  max_workers: 32,
  active_workers: 0,
  idle_workers: 4,
  queue_size: 32768,
  queue_length: 0,
  queue_usage_percent: 0,
  enqueued: 0,
  dropped: 0,
  processed: 0,
  errors: 0,
  pre_block_active: 0,
  pre_block_checked: 0,
  pre_block_allowed: 0,
  pre_block_blocked: 0,
  pre_block_errors: 0,
  pre_block_avg_latency_ms: 0,
  pre_block_api_key_active: 0,
  pre_block_api_key_available_count: 0,
  pre_block_api_key_total_calls: 0,
  pre_block_api_key_loads: [],
  api_key_statuses: [],
  flagged_hash_count: 0,
  last_cleanup_deleted_hit: 0,
  last_cleanup_deleted_non_hit: 0,
})

const AppLayoutStub = { template: '<div><slot /></div>' }
const BaseDialogStub = defineComponent({
  props: {
    show: {
      type: Boolean,
      default: false,
    },
  },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
})
const ModelWhitelistSelectorStub = defineComponent({
  props: {
    modelValue: {
      type: Array,
      default: () => [],
    },
  },
  emits: ['update:modelValue'],
  setup(props, { emit }) {
    const onInput = (event: Event) => {
      const value = (event.target as HTMLInputElement).value
      emit(
        'update:modelValue',
        value
          .split(/[,\n]/)
          .map((item) => item.trim())
          .filter(Boolean)
      )
    }
    return () =>
      h('input', {
        'data-test': 'model-filter-input',
        value: (props.modelValue as string[]).join('\n'),
        onInput,
      })
  },
})

function findButtonByText(wrapper: VueWrapper, text: string): DOMWrapper<HTMLButtonElement> {
  const button = wrapper.findAll<HTMLButtonElement>('button').find((item) => item.text().includes(text))
  if (!button) {
    throw new Error(`button not found: ${text}`)
  }
  return button
}

describe('admin RiskControlView', () => {
  beforeEach(() => {
    getConfig.mockReset()
    updateConfig.mockReset()
    getStatus.mockReset()
    listLogs.mockReset()
    getGroups.mockReset()
    showError.mockReset()
    showSuccess.mockReset()
    testAPIKeys.mockReset()
    userMocks.list.mockReset().mockResolvedValue({ items: [], total: 0 })
    userMocks.getById.mockReset().mockRejectedValue(new Error('not found'))

    getConfig.mockResolvedValue(baseConfig())
    getStatus.mockResolvedValue(runtimeStatus())
    listLogs.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20, pages: 1 })
    getGroups.mockResolvedValue([])
    getProxies.mockResolvedValue([])
    updateConfig.mockImplementation(async (payload: UpdateContentModerationConfig) => ({
      ...baseConfig(),
      ...payload,
      model_filter: payload.model_filter ?? baseConfig().model_filter,
      api_key_configured: false,
      api_key_masked: '',
      api_key_count: 0,
      api_key_masks: [],
      api_key_statuses: [],
    }))
  })

  it('exposes an embedded editor draft without separate saves or shared scope controls', async () => {
    const config = { ...baseConfig(), group_ids: [7], user_allowlist_ids: [42], blocked_keywords: ['preserved-custom'], risk_policy: defaultAuditRiskPolicy() }
    const wrapper = mount(RiskControlView, { props: { embedded: true, editorOnly: true, initialConfig: config }, global: { stubs: { AppLayout: AppLayoutStub, BaseDialog: BaseDialogStub, Icon: true, Select: true, Toggle: false, Pagination: true, ModelWhitelistSelector: ModelWhitelistSelectorStub, ProxySelector: true } } })
    await flushPromises()
    expect(getConfig).not.toHaveBeenCalled()
    expect(updateConfig).not.toHaveBeenCalled()
    expect(listLogs).not.toHaveBeenCalled()
    expect(wrapper.text()).not.toContain('admin.riskControl.saveConfig')
    await findButtonByText(wrapper, 'admin.riskControl.tabs.scope').trigger('click')
    expect(wrapper.find('[data-test="content-audit-user-allowlist"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="audit-group-7"]').exists()).toBe(false)
    const payload = (wrapper.vm as unknown as { preparePayload(): UpdateContentModerationConfig }).preparePayload()
    expect(payload).toMatchObject({ group_ids: [7], user_allowlist_ids: [42], blocked_keywords: ['preserved-custom'] })
    expect(payload.api_keys).toBeUndefined()
    expect(updateConfig).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('keeps content and keyword drafts in one editor when switching category menus', async () => {
    const config = { ...baseConfig(), blocked_keywords: ['original keyword'], keyword_blocking_mode: 'keyword_only' as const }
    const wrapper = mount(RiskControlView, { props: { embedded: true, editorOnly: true, editorSection: 'content', initialConfig: config }, global: { stubs: { AppLayout: AppLayoutStub, BaseDialog: BaseDialogStub, Icon: true, Select: true, Toggle: false, Pagination: true, ModelWhitelistSelector: ModelWhitelistSelectorStub, ProxySelector: true } } })
    await flushPromises()
    expect(wrapper.text()).not.toContain('admin.riskControl.tabs.keywords')
    await wrapper.setProps({ editorSection: 'keywords' })
    expect(wrapper.emitted('changed')).toBeUndefined()
    expect(wrapper.text()).not.toContain('admin.riskControl.tabs.basic')
    await wrapper.get('textarea').setValue('edited keyword')
    await wrapper.setProps({ editorSection: 'content' })
    await wrapper.get('[data-test="audit-base-url"]').setValue('https://audit.example.com')
    await findButtonByText(wrapper, 'admin.riskControl.tabs.runtime').trigger('click')
    await wrapper.setProps({ editorSection: 'keywords' })
    expect(wrapper.get<HTMLTextAreaElement>('textarea').element.value).toBe('edited keyword')
    await wrapper.setProps({ editorSection: 'content' })
    expect(wrapper.find('[aria-label="admin.riskControl.recordNonHits"]').exists()).toBe(true)
    const payload = (wrapper.vm as unknown as { preparePayload(): UpdateContentModerationConfig }).preparePayload()
    expect(payload).toMatchObject({ base_url: 'https://audit.example.com', blocked_keywords: ['edited keyword'] })
    expect(getConfig).not.toHaveBeenCalled()
    expect(updateConfig).not.toHaveBeenCalled()
    expect(listLogs).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('shows only content events without a configuration editor or runtime polling', async () => {
    getProxies.mockClear()
    const wrapper = mount(RiskControlView, { props: { embedded: true, eventsOnly: true }, global: { stubs: { AppLayout: AppLayoutStub, BaseDialog: BaseDialogStub, Icon: true, Select: true, Pagination: true } } })
    await flushPromises()
    expect(wrapper.find('[data-test="content-event-workspace"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="active-audit-engine"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="audit-engine-select"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('admin.riskControl.openSettings')
    expect(listLogs).toHaveBeenCalledOnce()
    expect(getStatus).not.toHaveBeenCalled()
    expect(getProxies).not.toHaveBeenCalled()
    expect(updateConfig).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('keeps old audit policy until enabled, saves keyword scores and clears scored mode explicitly', async () => {
    const config = { ...baseConfig(), blocked_keywords: ['Custom', '忽略之前的所有指令'], keyword_scores: { custom: 0, '忽略之前的所有指令': 7 } }
    getConfig.mockResolvedValue(config)
    updateConfig.mockImplementation(async (payload: UpdateContentModerationConfig) => ({ ...config, ...payload }))
    const wrapper = mount(RiskControlView, { global: { stubs: { AppLayout: AppLayoutStub, BaseDialog: BaseDialogStub, Icon: true, Select: true, Toggle: false, Pagination: true, ModelWhitelistSelector: ModelWhitelistSelectorStub, ProxySelector: true } } })
    await flushPromises()
    await findButtonByText(wrapper, 'admin.riskControl.openSettings').trigger('click')
    await findButtonByText(wrapper, 'admin.riskControl.tabs.riskThresholds').trigger('click')
    expect(wrapper.find('[data-test="content-risk-policy-legacy"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="legacy-category-thresholds"]').exists()).toBe(true)
    expect(updateConfig).not.toHaveBeenCalled()
    await wrapper.get('[data-test="content-risk-policy-enabled"]').trigger('click')
    await wrapper.get('[data-test="content-risk-medium-action"]').setValue('charge_allow')
    expect(wrapper.find('[data-test="legacy-category-thresholds"]').exists()).toBe(false)
    await findButtonByText(wrapper, 'admin.riskControl.tabs.keywords').trigger('click')
    await wrapper.get('[data-test="keyword-template-instruction_override"]').trigger('click')
    await wrapper.get('[data-test="keyword-template-credential_theft"]').trigger('click')
    await findButtonByText(wrapper, 'admin.riskControl.saveConfig').trigger('click')
    await flushPromises()
    expect(updateConfig).toHaveBeenLastCalledWith(expect.objectContaining({
      risk_policy: { ...defaultAuditRiskPolicy(), medium_action: 'charge_allow' },
      keyword_scores: expect.objectContaining({ custom: 0, '忽略之前的所有指令': 7, '忽略上面的安全规则': 20, '窃取他人的登录密码': 80 }),
    }))
    await findButtonByText(wrapper, 'admin.riskControl.openSettings').trigger('click')
    await findButtonByText(wrapper, 'admin.riskControl.tabs.riskThresholds').trigger('click')
    await wrapper.get('[data-test="content-risk-policy-enabled"]').trigger('click')
    await findButtonByText(wrapper, 'admin.riskControl.saveConfig').trigger('click')
    await flushPromises()
    expect(updateConfig).toHaveBeenLastCalledWith(expect.objectContaining({ risk_policy: null, keyword_scores: expect.objectContaining({ custom: 0 }) }))
    expect(testAPIKeys).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('rejects invalid thresholds and keyword scores before sending any save request', async () => {
    getConfig.mockResolvedValue({ ...baseConfig(), risk_policy: defaultAuditRiskPolicy(), blocked_keywords: ['Custom'] })
    const wrapper = mount(RiskControlView, { global: { stubs: { AppLayout: AppLayoutStub, BaseDialog: BaseDialogStub, Icon: true, Select: true, Toggle: false, Pagination: true, ModelWhitelistSelector: ModelWhitelistSelectorStub, ProxySelector: true } } })
    await flushPromises()
    await findButtonByText(wrapper, 'admin.riskControl.openSettings').trigger('click')
    await findButtonByText(wrapper, 'admin.riskControl.tabs.riskThresholds').trigger('click')
    await wrapper.get('[data-test="content-risk-medium-threshold"]').setValue(20)
    await findButtonByText(wrapper, 'admin.riskControl.saveConfig').trigger('click')
    expect(showError).toHaveBeenLastCalledWith('admin.promptAudit.riskPolicy.invalid')
    await wrapper.get('[data-test="content-risk-medium-threshold"]').setValue(40)
    await findButtonByText(wrapper, 'admin.riskControl.tabs.keywords').trigger('click')
    await wrapper.get('[data-keyword-score="Custom"]').setValue('')
    await findButtonByText(wrapper, 'admin.riskControl.saveConfig').trigger('click')
    expect(showError).toHaveBeenLastCalledWith('admin.promptAudit.riskPolicy.invalidScore')
    expect(updateConfig).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('selects content audit exempt users by email or ID, preserves missing IDs, and explicitly clears the saved list', async () => {
    const selectedUser = { id: 7, email: 'selected@example.com', username: 'Selected', status: 'disabled', deleted_at: null }
    const searchedUser = { id: 8, email: 'search@example.com', username: 'Found', status: 'active', deleted_at: null }
    userMocks.getById.mockImplementation(async (id: number) => {
      if (id === 7) return selectedUser
      if (id === 8) return searchedUser
      throw new Error('not found')
    })
    userMocks.list.mockResolvedValue({ items: [selectedUser, searchedUser, { id: 9, email: 'deleted@example.com', deleted_at: '2026-01-01' }], total: 3 })
    getConfig.mockResolvedValue({ ...baseConfig(), user_allowlist_ids: [42, 7], user_allowlist_inherited: true, group_ids: [5], blocked_keywords: ['custom keyword'] })
    const wrapper = mount(RiskControlView, { global: { stubs: { AppLayout: AppLayoutStub, BaseDialog: BaseDialogStub, Icon: true, Select: true, Toggle: false, Pagination: true, ModelWhitelistSelector: ModelWhitelistSelectorStub, ProxySelector: true } } })
    await flushPromises()
    await findButtonByText(wrapper, 'admin.riskControl.openSettings').trigger('click')
    await findButtonByText(wrapper, 'admin.riskControl.tabs.scope').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="allowlist-selected-42"]').text()).toContain('admin.userAllowlist.unresolved')
    expect(wrapper.get('[data-test="allowlist-selected-7"]').text()).toContain(selectedUser.email)
    expect(wrapper.get('[data-test="content-audit-allowlist-inherited"]').text()).toContain('admin.riskControl.userAllowlistInherited')
    expect(updateConfig).not.toHaveBeenCalled()
    await wrapper.get('[data-test="allowlist-search"]').setValue('search@example.com')
    await wrapper.get('[data-test="allowlist-search-button"]').trigger('click')
    await flushPromises()
    expect(userMocks.list).toHaveBeenCalledWith(1, 10, { search: 'search@example.com' }, { signal: expect.any(AbortSignal) })
    expect(wrapper.find('[data-test="allowlist-add-9"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="allowlist-add-7"]').attributes()).toHaveProperty('disabled')
    await wrapper.get('[data-test="allowlist-add-8"]').trigger('click')
    expect(wrapper.getComponent(UserAllowlistSelector).props('modelValue')).toEqual([7, 8, 42])
    await wrapper.get('[data-test="allowlist-remove-42"]').trigger('click')
    await wrapper.get('[data-test="allowlist-search"]').setValue('#8')
    await wrapper.get('[data-test="allowlist-search-button"]').trigger('click')
    await flushPromises()
    expect(userMocks.getById).toHaveBeenCalledWith(8)
    expect(updateConfig).not.toHaveBeenCalled()
    await findButtonByText(wrapper, 'admin.riskControl.saveConfig').trigger('click')
    await flushPromises()
    expect(updateConfig).toHaveBeenLastCalledWith(expect.objectContaining({ user_allowlist_ids: [7, 8], group_ids: [5], blocked_keywords: ['custom keyword'], enabled: true, mode: 'pre_block' }))
    await findButtonByText(wrapper, 'admin.riskControl.openSettings').trigger('click')
    await findButtonByText(wrapper, 'admin.riskControl.tabs.scope').trigger('click')
    await wrapper.get('[data-test="allowlist-clear"]').trigger('click')
    await findButtonByText(wrapper, 'admin.riskControl.saveConfig').trigger('click')
    await flushPromises()
    expect(updateConfig).toHaveBeenLastCalledWith(expect.objectContaining({ user_allowlist_ids: [], group_ids: [5], blocked_keywords: ['custom keyword'] }))
    expect(testAPIKeys).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('does not restore stale user search results after a query changes or clears', async () => {
    let resolveOld!: (value: unknown) => void
    userMocks.list.mockImplementationOnce(() => new Promise((resolve) => { resolveOld = resolve }))
    const wrapper = mount(UserAllowlistSelector, { props: { modelValue: [], title: 'Users', hint: 'Independent scope' } })
    await wrapper.get('[data-test="allowlist-search"]').setValue('old@example.com')
    await wrapper.get('[data-test="allowlist-search-button"]').trigger('click')
    const signal = userMocks.list.mock.calls[0][3].signal as AbortSignal
    await wrapper.get('[data-test="allowlist-search"]').setValue('')
    expect(signal.aborted).toBe(true)
    resolveOld({ items: [{ id: 99, email: 'old@example.com' }], total: 1 })
    await flushPromises()
    expect(wrapper.find('[data-test="allowlist-add-99"]').exists()).toBe(false)
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    wrapper.unmount()
  })

  it('appends bilingual keyword categories and all templates without changing activation, scope or mode', async () => {
    const duplicate = CONTENT_AUDIT_KEYWORD_TEMPLATES[0].keywords[12].toUpperCase()
    getConfig.mockResolvedValue({ ...baseConfig(), enabled: false, mode: 'observe', keyword_blocking_mode: 'api_only', group_ids: [7], blocked_keywords: ['自定义业务词', duplicate, duplicate.toLowerCase()] })
    const wrapper = mount(RiskControlView, { global: { stubs: { AppLayout: AppLayoutStub, BaseDialog: BaseDialogStub, Icon: true, Select: true, Toggle: false, Pagination: true, ModelWhitelistSelector: ModelWhitelistSelectorStub, ProxySelector: true } } })
    await flushPromises()
    await findButtonByText(wrapper, 'admin.riskControl.openSettings').trigger('click')
    expect(wrapper.get('[data-test="audit-no-key-guide"]').text()).toContain('admin.riskControl.noKeyGuide')
    await findButtonByText(wrapper, 'admin.riskControl.tabs.keywords').trigger('click')
    const input = () => wrapper.get<HTMLTextAreaElement>('[data-test="blocked-keywords-input"]')
    await wrapper.get('[data-test="keyword-template-instruction_override"]').trigger('click')
    expect(input().element.value.split('\n')).toHaveLength(25)
    expect(input().element.value.split('\n').slice(0, 2)).toEqual(['自定义业务词', duplicate])
    await wrapper.get('[data-test="keyword-template-all"]').trigger('click')
    const allText = input().element.value
    const words = allText.split('\n')
    expect(words).toHaveLength(217)
    expect(new Set(words.map((word) => word.toLowerCase())).size).toBe(words.length)
    for (const category of CONTENT_AUDIT_KEYWORD_TEMPLATES) {
      expect(category.keywords.some((word) => /[\u4e00-\u9fff]/.test(word))).toBe(true)
      expect(category.keywords.some((word) => /^[a-z]/i.test(word))).toBe(true)
      expect(category.keywords.every((word) => word.length <= 200)).toBe(true)
      expect(words.map((word) => word.toLowerCase())).toEqual(expect.arrayContaining(category.keywords.map((word) => word.toLowerCase())))
    }
    await wrapper.get('[data-test="keyword-template-all"]').trigger('click')
    expect(input().element.value).toBe(allText)
    expect(input().attributes()).toHaveProperty('disabled')
    expect(updateConfig).not.toHaveBeenCalled()
    expect(testAPIKeys).not.toHaveBeenCalled()
    await findButtonByText(wrapper, 'admin.riskControl.saveConfig').trigger('click')
    await flushPromises()
    expect(updateConfig).toHaveBeenCalledWith(expect.objectContaining({ enabled: false, mode: 'observe', keyword_blocking_mode: 'api_only', group_ids: [7], blocked_keywords: words }))
    wrapper.unmount()
  })

  it('leaves custom keywords intact when appending would exceed the saved keyword limit', async () => {
    const custom = Array.from({ length: 10000 }, (_, index) => `custom phrase ${index}`)
    getConfig.mockResolvedValue({ ...baseConfig(), blocked_keywords: custom })
    const wrapper = mount(RiskControlView, { global: { stubs: { AppLayout: AppLayoutStub, BaseDialog: BaseDialogStub, Icon: true, Select: true, Toggle: false, Pagination: true, ModelWhitelistSelector: ModelWhitelistSelectorStub, ProxySelector: true } } })
    await flushPromises()
    await findButtonByText(wrapper, 'admin.riskControl.openSettings').trigger('click')
    await findButtonByText(wrapper, 'admin.riskControl.tabs.keywords').trigger('click')
    await wrapper.get('[data-test="keyword-template-all"]').trigger('click')
    expect(wrapper.get<HTMLTextAreaElement>('[data-test="blocked-keywords-input"]').element.value).toBe(custom.join('\n'))
    expect(showError).toHaveBeenCalledWith('admin.riskControl.keywordTemplatesLimit')
    expect(updateConfig).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it.each(['openai', 'typesafe'] as const)('shows the selected draft engine keys while %s remains active', async (activeEngine) => {
    const openaiKey: ContentModerationAPIKeyStatus = {
      index: 1, key_hash: 'openai-hash', masked: '********oa01', status: 'frozen',
      failure_count: 1, success_count: 0, last_error: '', last_latency_ms: 100,
      last_http_status: 429, last_tested: false, configured: true,
    }
    const typesafeKey: ContentModerationAPIKeyStatus = { ...openaiKey, key_hash: 'typesafe-hash', masked: '********ts02', status: 'ok', failure_count: 0, success_count: 1, last_http_status: 200 }
    const profiles = {
      openai: { ...baseConfig(), engine: 'openai' as const, api_key_configured: true, api_key_count: 1, api_key_statuses: [openaiKey] },
      typesafe: { ...baseConfig(), engine: 'typesafe' as const, api_key_configured: true, api_key_count: 1, api_key_statuses: [typesafeKey] },
    }
    const otherEngine = activeEngine === 'openai' ? 'typesafe' : 'openai'
    getConfig.mockResolvedValue({ ...profiles[activeEngine], engine_configs: profiles })
    getStatus.mockResolvedValue({ ...runtimeStatus(), engine: activeEngine, api_key_statuses: profiles[activeEngine].api_key_statuses })
    const wrapper = mount(RiskControlView, { global: { stubs: { AppLayout: AppLayoutStub, BaseDialog: BaseDialogStub, Icon: true, Select: true, Toggle: false, Pagination: true, ModelWhitelistSelector: ModelWhitelistSelectorStub, ProxySelector: true } } })
    await flushPromises()
    await findButtonByText(wrapper, 'admin.riskControl.openSettings').trigger('click')
    const engine = () => wrapper.findComponent('[data-test="audit-engine-select"]')
    const keyPanel = () => wrapper.get('[data-test="audit-key-statuses"]')
    expect(keyPanel().text()).toContain(profiles[activeEngine].api_key_statuses[0].masked)
    engine().vm.$emit('update:modelValue', otherEngine)
    await flushPromises()
    expect(keyPanel().text()).toContain(profiles[otherEngine].api_key_statuses[0].masked)
    expect(keyPanel().text()).not.toContain(profiles[activeEngine].api_key_statuses[0].masked)
    expect(keyPanel().text()).toContain(otherEngine === 'openai' ? 'admin.riskControl.apiKeyStatusFrozen' : 'admin.riskControl.apiKeyStatusOk')
    await findButtonByText(wrapper, 'admin.riskControl.refreshStatus').trigger('click')
    await flushPromises()
    expect(keyPanel().text()).toContain(profiles[otherEngine].api_key_statuses[0].masked)
    expect(keyPanel().text()).not.toContain(profiles[activeEngine].api_key_statuses[0].masked)
    engine().vm.$emit('update:modelValue', activeEngine)
    await flushPromises()
    expect(keyPanel().text()).toContain(profiles[activeEngine].api_key_statuses[0].masked)
    expect(keyPanel().text()).not.toContain(profiles[otherEngine].api_key_statuses[0].masked)
    expect(updateConfig).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('shows an empty key panel for an unconfigured draft engine', async () => {
    getStatus.mockResolvedValue({ ...runtimeStatus(), api_key_statuses: [{ masked: '********oa01', configured: true, status: 'ok' }] })
    const wrapper = mount(RiskControlView, { global: { stubs: { AppLayout: AppLayoutStub, BaseDialog: BaseDialogStub, Icon: true, Select: true, Toggle: false, Pagination: true, ModelWhitelistSelector: ModelWhitelistSelectorStub, ProxySelector: true } } })
    await flushPromises()
    await findButtonByText(wrapper, 'admin.riskControl.openSettings').trigger('click')
    wrapper.findComponent('[data-test="audit-engine-select"]').vm.$emit('update:modelValue', 'typesafe')
    await flushPromises()
    expect(wrapper.get('[data-test="audit-key-statuses"]').text()).toContain('admin.riskControl.apiKeyHealthEmpty')
    expect(wrapper.get('[data-test="audit-key-statuses"]').text()).not.toContain('********oa01')
    expect(updateConfig).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('preserves both engine drafts and saves thresholds independently', async () => {
    const wrapper = mount(RiskControlView, { global: { stubs: { AppLayout: AppLayoutStub, BaseDialog: BaseDialogStub, Icon: true, Select: true, Toggle: false, Pagination: true, ModelWhitelistSelector: ModelWhitelistSelectorStub, ProxySelector: true } } })
    await flushPromises()
    await findButtonByText(wrapper, 'admin.riskControl.openSettings').trigger('click')
    await wrapper.get('[data-test="audit-base-url"]').setValue('https://openai-edited.example')
    await findButtonByText(wrapper, 'admin.riskControl.tabs.riskThresholds').trigger('click')
    await wrapper.get('[data-test="risk-threshold-sexual"]').setValue('73')
    await findButtonByText(wrapper, 'admin.riskControl.tabs.basic').trigger('click')
    const engine = () => wrapper.findComponent('[data-test="audit-engine-select"]')
    engine().vm.$emit('update:modelValue', 'typesafe')
    await flushPromises()
    expect((wrapper.get('[data-test="audit-model"]').element as HTMLInputElement).value).toBe('jev-latest')
    await wrapper.get('[data-test="audit-base-url"]').setValue('https://typesafe-edited.example')
    await findButtonByText(wrapper, 'admin.riskControl.tabs.riskThresholds').trigger('click')
    expect((wrapper.get('[data-test="risk-threshold-sexual"]').element as HTMLInputElement).value).toBe('65')
    await wrapper.get('[data-test="risk-threshold-sexual"]').setValue('91')
    await findButtonByText(wrapper, 'admin.riskControl.tabs.basic').trigger('click')
    engine().vm.$emit('update:modelValue', 'openai')
    await flushPromises()
    expect((wrapper.get('[data-test="audit-base-url"]').element as HTMLInputElement).value).toBe('https://openai-edited.example')
    await findButtonByText(wrapper, 'admin.riskControl.saveConfig').trigger('click')
    await flushPromises()
    expect(updateConfig).toHaveBeenCalledWith(expect.objectContaining({ engine: 'openai', engine_configs: {
      openai: expect.objectContaining({ base_url: 'https://openai-edited.example', thresholds: expect.objectContaining({ sexual: 0.73 }) }),
      typesafe: expect.objectContaining({ base_url: 'https://typesafe-edited.example', thresholds: expect.objectContaining({ sexual: 0.91 }) }),
    } }))
    expect(showError).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('tests the draft engine without switching the active engine', async () => {
    testAPIKeys.mockResolvedValue({ items: [{ status: 'ok', configured: false }], image_count: 0 })
    const wrapper = mount(RiskControlView, { global: { stubs: { AppLayout: AppLayoutStub, BaseDialog: BaseDialogStub, Icon: true, Select: true, Toggle: false, Pagination: true, ModelWhitelistSelector: ModelWhitelistSelectorStub, ProxySelector: true } } })
    await flushPromises()
    await findButtonByText(wrapper, 'admin.riskControl.openSettings').trigger('click')
    wrapper.findComponent('[data-test="audit-engine-select"]').vm.$emit('update:modelValue', 'typesafe')
    await flushPromises()
    await wrapper.get('textarea[autocomplete="new-password"]').setValue('test-only-key')
    await findButtonByText(wrapper, 'admin.riskControl.testInputApiKeys').trigger('click')
    await flushPromises()
    expect(testAPIKeys).toHaveBeenCalledWith(expect.objectContaining({ engine: 'typesafe', api_keys: ['test-only-key'], model: 'jev-latest', thresholds: expect.objectContaining({ sexual: 0.65 }) }))
    expect(updateConfig).not.toHaveBeenCalled()
    expect(wrapper.get('[data-test="active-audit-engine"]').text()).not.toContain('TypeSafe AI')
    wrapper.unmount()
  })

  it.each(['openai', 'typesafe'] as const)('restores the shared defaults for %s without replacing saved thresholds on load', async (engine) => {
    getConfig.mockResolvedValue({ ...baseConfig(), engine, thresholds: { sexual: 0.8, harassment: 0.8 } })
    const wrapper = mount(RiskControlView, { global: { stubs: { AppLayout: AppLayoutStub, BaseDialog: BaseDialogStub, Icon: true, Select: true, Toggle: false, Pagination: true, ModelWhitelistSelector: ModelWhitelistSelectorStub, ProxySelector: true } } })
    await flushPromises()
    await findButtonByText(wrapper, 'admin.riskControl.openSettings').trigger('click')
    await findButtonByText(wrapper, 'admin.riskControl.tabs.riskThresholds').trigger('click')
    expect((wrapper.get('[data-test="risk-threshold-sexual"]').element as HTMLInputElement).value).toBe('80')
    await findButtonByText(wrapper, 'admin.riskControl.riskThresholdReset').trigger('click')
    const defaults = {
      harassment: 98, 'harassment/threatening': 90, hate: 65, 'hate/threatening': 65,
      illicit: 95, 'illicit/violent': 95, 'self-harm': 65, 'self-harm/intent': 85,
      'self-harm/instructions': 65, sexual: 65, 'sexual/minors': 65, violence: 95, 'violence/graphic': 95,
    }
    for (const [category, value] of Object.entries(defaults)) {
      expect((wrapper.get(`[data-test="risk-threshold-${category}"]`).element as HTMLInputElement).value).toBe(String(value))
    }
    if (engine === 'typesafe') expect(wrapper.text()).toContain('admin.riskControl.typeSafeThresholds')
    expect(updateConfig).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('requires explicit group selection even for legacy all-groups configuration and can clear the selection', async () => {
    getConfig.mockResolvedValue({ ...baseConfig(), all_groups: true })
    getGroups.mockResolvedValue([
      { id: 7, name: 'Audit group', platform: 'openai' },
      { id: 8, name: 'Skipped group', platform: 'openai' },
    ])
    const wrapper = mount(RiskControlView, { global: { stubs: {
      AppLayout: AppLayoutStub, BaseDialog: BaseDialogStub, Icon: true, Select: true, Toggle: false,
      Pagination: true, ModelWhitelistSelector: ModelWhitelistSelectorStub, ProxySelector: true,
    } } })
    await flushPromises()
    const openScope = async () => {
      await findButtonByText(wrapper, 'admin.riskControl.openSettings').trigger('click')
      await findButtonByText(wrapper, 'admin.riskControl.tabs.scope').trigger('click')
    }
    await openScope()
    expect(wrapper.find('[data-test="audit-groups-empty"]').exists()).toBe(true)
    expect(wrapper.findAll('button').some((button) => button.text() === 'admin.riskControl.allGroups')).toBe(false)
    await wrapper.get('[data-test="audit-group-7"]').trigger('click')
    expect(wrapper.get('[data-test="audit-group-7"]').attributes('aria-pressed')).toBe('true')
    expect(wrapper.get('[data-test="audit-group-8"]').attributes('aria-pressed')).toBe('false')
    await findButtonByText(wrapper, 'admin.riskControl.saveConfig').trigger('click')
    await flushPromises()
    expect(updateConfig).toHaveBeenLastCalledWith(expect.objectContaining({ all_groups: false, group_ids: [7] }))
    await openScope()
    await wrapper.get('[data-test="audit-group-7"]').trigger('click')
    expect(wrapper.find('[data-test="audit-groups-empty"]').exists()).toBe(true)
    await findButtonByText(wrapper, 'admin.riskControl.saveConfig').trigger('click')
    await flushPromises()
    expect(updateConfig).toHaveBeenLastCalledWith(expect.objectContaining({ all_groups: false, group_ids: [] }))
    expect(showError).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('saves the selected model filter mode and models', async () => {
    const wrapper = mount(RiskControlView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub,
          BaseDialog: BaseDialogStub,
          Icon: true,
          Select: true,
          Toggle: false,
          Pagination: true,
          ModelWhitelistSelector: ModelWhitelistSelectorStub,
          ProxySelector: true,
        },
      },
    })

    await flushPromises()

    await findButtonByText(wrapper, 'admin.riskControl.openSettings').trigger('click')
    await findButtonByText(wrapper, 'admin.riskControl.tabs.scope').trigger('click')
    await findButtonByText(wrapper, 'admin.riskControl.modelFilterInclude').trigger('click')
    await wrapper.get('[data-test="model-filter-input"]').setValue('gpt-5.5, gpt-5.4')
    await findButtonByText(wrapper, 'admin.riskControl.saveConfig').trigger('click')
    await flushPromises()

    expect(updateConfig).toHaveBeenCalledWith(expect.objectContaining({
      model_filter: {
        type: 'include',
        models: ['gpt-5.5', 'gpt-5.4'],
      },
    }))
    expect(showError).not.toHaveBeenCalled()
  })

  it('submits edited risk control thresholds when saving moderation config', async () => {
    const wrapper = mount(RiskControlView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub,
          BaseDialog: BaseDialogStub,
          Icon: true,
          Select: true,
          Toggle: false,
          Pagination: true,
          ModelWhitelistSelector: ModelWhitelistSelectorStub,
          ProxySelector: true,
        },
      },
    })

    await flushPromises()

    await findButtonByText(wrapper, 'admin.riskControl.openSettings').trigger('click')
    await findButtonByText(wrapper, 'admin.riskControl.tabs.riskThresholds').trigger('click')
    await wrapper.get('[data-test="risk-threshold-sexual"]').setValue('72')
    await wrapper.get('[data-test="risk-threshold-harassment"]').setValue('99')
    await findButtonByText(wrapper, 'admin.riskControl.saveConfig').trigger('click')
    await flushPromises()

    expect(updateConfig).toHaveBeenCalledWith(expect.objectContaining({
      thresholds: expect.objectContaining({
        sexual: 0.72,
        harassment: 0.99,
      }),
    }))
    expect(showError).not.toHaveBeenCalled()
  })

  it('describes worker runtime as async audit and pre-block record processing', async () => {
    getStatus.mockResolvedValue({
      ...runtimeStatus(),
      mode: 'observe',
      processed: 12,
      queue_length: 2,
    })

    const wrapper = mount(RiskControlView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub,
          BaseDialog: BaseDialogStub,
          Icon: true,
          Select: true,
          Toggle: false,
          Pagination: true,
          ModelWhitelistSelector: ModelWhitelistSelectorStub,
          ProxySelector: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.text()).toContain('admin.riskControl.workerStatusHint')
    expect(wrapper.text()).not.toContain('admin.riskControl.preBlockSyncStatus')
    expect(wrapper.text()).toContain('admin.riskControl.records')
    expect(wrapper.text()).toContain('12')
    expect(wrapper.text()).toContain('2 / 32,768')
  })

  it('shows pre-block synchronous moderation metrics separately from worker queue', async () => {
    getStatus.mockResolvedValue({
      ...runtimeStatus(),
      pre_block_active: 2,
      pre_block_checked: 128,
      pre_block_allowed: 120,
      pre_block_blocked: 8,
      pre_block_errors: 1,
      pre_block_avg_latency_ms: 86,
      pre_block_api_key_active: 2,
      pre_block_api_key_available_count: 2,
      pre_block_api_key_total_calls: 128,
      active_workers: 3,
      worker_count: 7,
      pre_block_api_key_loads: [
        {
          index: 0,
          key_hash: 'hash-one',
          masked: 'sk-...one',
          status: 'ok',
          active: 1,
          total: 72,
          success: 70,
          errors: 2,
          avg_latency_ms: 84,
          last_latency_ms: 80,
          last_http_status: 200,
        },
        {
          index: 1,
          key_hash: 'hash-two',
          masked: 'sk-...two',
          status: 'ok',
          active: 1,
          total: 56,
          success: 56,
          errors: 0,
          avg_latency_ms: 90,
          last_latency_ms: 92,
          last_http_status: 200,
        },
      ],
    })

    const wrapper = mount(RiskControlView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub,
          BaseDialog: BaseDialogStub,
          Icon: true,
          Select: true,
          Toggle: false,
          Pagination: true,
          ModelWhitelistSelector: ModelWhitelistSelectorStub,
          ProxySelector: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.text()).toContain('admin.riskControl.preBlockSyncStatus')
    expect(wrapper.text()).toContain('admin.riskControl.preBlockSyncHint')
    expect(wrapper.text()).not.toContain('admin.riskControl.workerStatus')
    expect(wrapper.text()).toContain('admin.riskControl.records')
    expect(wrapper.text()).toContain('128')
    expect(wrapper.text()).toContain('120')
    expect(wrapper.text()).toContain('8')
    expect(wrapper.text()).toContain('86 ms')
    expect(wrapper.text()).toContain('admin.riskControl.preBlockAPIKeyLoad')
    expect(wrapper.text()).toContain('sk-...one')
    expect(wrapper.text()).toContain('sk-...two')
    expect(wrapper.text()).toContain('72')
    expect(wrapper.text()).toContain('56')
    expect(wrapper.text()).toContain('同步并发 2 / 可用 Key 2，累计 128 次，worker：3 / 7')

    const runtimeCards = wrapper.get('[data-test="pre-block-runtime-cards"]')
    const syncCard = wrapper.get('[data-test="pre-block-sync-card"]')
    const apiKeyLoadCard = wrapper.get('[data-test="pre-block-api-key-load-card"]')

    expect(runtimeCards.classes()).toEqual(expect.arrayContaining([
      'grid',
      'grid-cols-1',
      'xl:grid-cols-[minmax(0,520px)_minmax(0,1fr)]',
    ]))
    expect(syncCard.element.parentElement).toBe(runtimeCards.element)
    expect(apiKeyLoadCard.element.parentElement).toBe(runtimeCards.element)
    expect(syncCard.classes()).toContain('card')
    expect(apiKeyLoadCard.classes()).toContain('card')
    expect(syncCard.get('h2').text()).toBe('admin.riskControl.preBlockSyncStatus')
    expect(syncCard.text()).toContain('admin.riskControl.preBlockSyncHint')
    expect(apiKeyLoadCard.get('h2').text()).toBe('admin.riskControl.preBlockAPIKeyLoad')
    expect(apiKeyLoadCard.text()).toContain('admin.riskControl.preBlockAPIKeyLoadHint')
    expect(wrapper.get('[data-test="pre-block-api-key-load-list"]').classes()).toEqual(expect.arrayContaining([
      'max-h-[280px]',
      'overflow-y-auto',
    ]))
  })
})
