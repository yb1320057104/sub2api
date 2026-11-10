import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import IntelligenceTestSettings from '../IntelligenceTestSettings.vue'
import QualityHistoryTimeline from '../QualityHistoryTimeline.vue'
import IntelligenceTestView from '@/views/user/IntelligenceTestView.vue'
import Select from '@/components/common/Select.vue'
import type { IntelligenceTestConfig, IntelligenceTestModelCatalog, IntelligenceTestQualityHistory, IntelligenceTestQualityState } from '@/api/intelligenceTest'
import en from '@/i18n/locales/en/intelligenceTest'
import zh from '@/i18n/locales/zh/intelligenceTest'

const api = vi.hoisted(() => ({ config: vi.fn(), models: vi.fn(), saveConfig: vi.fn(), syncKeys: vi.fn(), groups: vi.fn(), qualityGroups: vi.fn(), qualityResults: vi.fn(), results: vi.fn() }))
const setEnabled = vi.hoisted(() => vi.fn())
const qualityNavigation = vi.hoisted(() => ({ route: { query: { mode: 'candy' } }, replace: vi.fn() }))
vi.mock('vue-router', () => ({ useRoute: () => qualityNavigation.route, useRouter: () => ({ replace: qualityNavigation.replace }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isAdmin: false }) }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))
vi.mock('@/components/intelligenceTest/ResultArtifact.vue', () => ({ default: { template: '<div />' } }))
vi.mock('@/components/intelligenceTest/ResultDetails.vue', () => ({ default: { template: '<div data-testid="safe-result-details" />' } }))
vi.mock('@/api/intelligenceTest', () => ({ intelligenceTestAPI: api }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: vi.fn() }) }))
vi.mock('@/stores/intelligenceTest', () => ({ useIntelligenceTestStore: () => ({ setEnabled, enabled: true, loaded: true, refresh: async () => true }) }))

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: Error) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

function settings(): IntelligenceTestConfig {
  return {
    enabled: false,
    svg: { enabled: true, prompt: 'Draw SVG', model: 'saved-svg', effort: 'persistent', interval_minutes: 5, group_overrides: [{ group_id: 7, model: 'saved-svg-group', effort: 'none' }] },
    candy: { enabled: true, prompt: 'Solve puzzle', model: 'saved-candy', effort: 'ultra', expected_answer: 21, interval_minutes: 10, group_overrides: [{ group_id: 7, model: 'saved-candy-group', effort: '' }] },
  }
}

function catalog(models: string[], excluded_models: string[] = []): IntelligenceTestModelCatalog {
  return { models, excluded_models }
}

let wrapper: VueWrapper | undefined
function render(locale = 'en') {
  wrapper = mount(IntelligenceTestSettings, {
    props: { groups: [{ id: 7, name: 'Group A', platform: 'openai', selected: false, key_configured: true }], groupsLoaded: true },
    global: { plugins: [createI18n({ legacy: false, locale, messages: { en, zh }, missingWarn: false, fallbackWarn: false })] },
  })
  return wrapper
}
function select(label: string) {
  const found = wrapper!.findAllComponents(Select).find(component => component.props('ariaLabel') === label)
  if (!found) throw new Error(`Missing select ${label}`)
  return found
}
function reloadButton() {
  return wrapper!.findAll('button').find(button => button.text() === 'Reload model choices')!
}

beforeEach(() => {
  vi.clearAllMocks()
  api.config.mockResolvedValue(settings())
  api.models.mockResolvedValue(catalog(['gpt-6.1-sol', 'claude-sonnet-4-6', 'gpt-6.1-sol']))
  api.saveConfig.mockImplementation(async (config: IntelligenceTestConfig) => structuredClone(config))
  api.qualityGroups.mockResolvedValue({ enabled: true, groups: [{ id: 7, name: 'Group A', platform: 'openai' }, { id: 8, name: 'Group B', platform: 'openai' }] })
  api.qualityResults.mockImplementation(async (id: number) => qualityHistory(id))
  qualityNavigation.route.query.mode = 'candy'
})

function qualityHistory(groupID = 7, states: IntelligenceTestQualityState[] = ['correct']): IntelligenceTestQualityHistory {
  return {
    enabled: true, group_id: groupID, interval_minutes: 5, history_limit: 30, stale_after_seconds: 600,
    observed_at: '2026-11-10T12:05:00Z',
    results: states.map((state, index) => ({
      id: 30 - index, model: `group-${groupID}-model`, effort: 'high', state,
      status: state === 'running' ? 'running' : state === 'error' ? 'failed' : 'completed',
      passed: state === 'correct' ? true : state === 'incorrect' ? false : undefined,
      empty_pool: state === 'empty_pool', started_at: '2026-11-10T12:00:00Z',
      finished_at: state === 'running' ? undefined : '2026-11-10T12:01:00Z',
    })),
  }
}

function qualityPlugins(locale = 'en') {
  return [createI18n({ legacy: false, locale, messages: { en, zh }, missingWarn: false, fallbackWarn: false })]
}

describe('user quality history display', () => {
  it('keeps all six real result states distinct and gray placeholders unclassified', () => {
    wrapper = mount(QualityHistoryTimeline, {
      props: { enabled: true, loading: false, history: qualityHistory(7, ['correct', 'incorrect', 'error', 'unknown', 'empty_pool', 'running']) },
      global: { plugins: qualityPlugins() },
    })
    expect(wrapper.findAll('[data-testid="quality-history-slot"]')).toHaveLength(30)
    expect(wrapper.findAll('[data-testid="quality-history-slot"][role="img"]')).toHaveLength(6)
    for (const text of ['Normal', 'Incorrect answer', 'Check failed', 'Unknown outcome', 'Empty pool', 'Checking']) expect(wrapper.text()).toContain(text)
    expect(api.qualityResults).not.toHaveBeenCalled()
    expect(api.saveConfig).not.toHaveBeenCalled()
  })

  it('marks old observations stale without changing the recorded answer verdict', () => {
    const history = qualityHistory()
    history.observed_at = '2026-11-10T13:00:00Z'
    wrapper = mount(QualityHistoryTimeline, { props: { enabled: true, loading: false, history }, global: { plugins: qualityPlugins() } })
    expect(wrapper.get('[data-testid="quality-current-state"]').text()).toBe('Result is stale')
    expect(wrapper.get('[role="img"]').attributes('aria-label')).toContain('Normal')
    expect(history.results[0].state).toBe('correct')
  })

  it('does not fabricate normal outcomes for an empty or disabled history', async () => {
    wrapper = mount(QualityHistoryTimeline, { props: { enabled: true, loading: false, history: qualityHistory(7, []) }, global: { plugins: qualityPlugins('zh') } })
    expect(wrapper.text()).toContain('暂无检测记录')
    expect(wrapper.find('[role="img"]').exists()).toBe(false)
    await wrapper.setProps({ enabled: false, history: null })
    expect(wrapper.text()).toContain('糖果题检测未开启')
    expect(wrapper.find('[data-testid="quality-current-state"]').exists()).toBe(false)
  })

  it('uses safe user reads on the candy deep link and preserves administrator mutation isolation', async () => {
    api.qualityResults.mockResolvedValueOnce(qualityHistory(7, ['error', 'empty_pool', 'unknown', 'incorrect', 'correct']))
    wrapper = mount(IntelligenceTestView, { global: { plugins: qualityPlugins() } })
    await flushPromises()
    expect(api.qualityGroups).toHaveBeenCalledOnce()
    expect(api.qualityResults).toHaveBeenCalledOnce()
    expect(api.groups).not.toHaveBeenCalled()
    expect(api.results).not.toHaveBeenCalled()
    expect(api.saveConfig).not.toHaveBeenCalled()
    expect(api.syncKeys).not.toHaveBeenCalled()
    expect(wrapper.findAll('[data-testid="safe-result-details"]')).toHaveLength(3)
    expect(wrapper.text()).toContain('group-7-model')
  })

  it('aborts old group reads and ignores their late result after changing groups', async () => {
    const old = deferred<IntelligenceTestQualityHistory>()
    api.qualityResults.mockReturnValueOnce(old.promise)
    wrapper = mount(IntelligenceTestView, { global: { plugins: qualityPlugins() } })
    await flushPromises()
    const signal = api.qualityResults.mock.calls[0][1] as AbortSignal
    await wrapper.get('#intelligence-group-8').trigger('click')
    await flushPromises()
    expect(signal.aborted).toBe(true)
    old.resolve(qualityHistory(7))
    await flushPromises()
    expect(wrapper.text()).toContain('group-8-model')
    expect(wrapper.text()).not.toContain('group-7-model')
  })

  it('clears old results when display is disabled during a refresh', async () => {
    wrapper = mount(IntelligenceTestView, { global: { plugins: qualityPlugins() } })
    await flushPromises()
    api.qualityResults.mockResolvedValueOnce({ ...qualityHistory(8, []), enabled: false })
    await wrapper.get('#intelligence-group-8').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Candy puzzle checks are disabled')
    expect(wrapper.text()).not.toContain('group-7-model')
    expect(wrapper.get('#intelligence-results').attributes('aria-busy')).toBe('false')
  })

  it('cancels group and history reads when the page unmounts', async () => {
    const old = deferred<IntelligenceTestQualityHistory>()
    api.qualityResults.mockReturnValueOnce(old.promise)
    wrapper = mount(IntelligenceTestView, { global: { plugins: qualityPlugins() } })
    await flushPromises()
    const groupSignal = api.qualityGroups.mock.calls[0][0] as AbortSignal
    const signal = api.qualityResults.mock.calls[0][1] as AbortSignal
    wrapper.unmount()
    wrapper = undefined
    expect(groupSignal.aborted).toBe(true)
    expect(signal.aborted).toBe(true)
    old.resolve(qualityHistory())
    await flushPromises()
    expect(api.saveConfig).not.toHaveBeenCalled()
  })
})
afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  document.body.innerHTML = ''
})

describe('intelligence model choices and effort', () => {
  it('merges built-in/read-only choices with both saved defaults and group overrides without starting work', async () => {
    render()
    await flushPromises()
    const values = select('SVG Model').props('options').map(option => option.value)
    expect(values).toEqual(expect.arrayContaining(['gpt-6.1-sol', 'claude-sonnet-4-6', 'saved-svg', 'saved-svg-group', 'saved-candy', 'saved-candy-group', 'MiniMax-M3', 'mistral-large-latest', 'llama-3.3-70b-instruct', 'doubao-vision-pro-32k']))
    expect(values.filter(value => value === 'gpt-6.1-sol')).toHaveLength(1)
    for (const media of ['gpt-image-2', 'gemini-3.1-flash-image', 'grok-imagine', 'grok-imagine-video', 'gpt-4o-audio-preview', 'gpt-4o-realtime-preview', 'cogview-3', 'cogvideo']) expect(values).not.toContain(media)
    expect(select('SVG Group A Model').props('options')[0]).toEqual({ value: '', label: 'Inherit default (saved-svg)' })
    for (const label of ['SVG Model', 'Candy puzzle Model', 'SVG Group A Model', 'Candy puzzle Group A Model']) {
      expect(select(label).props()).toMatchObject({ searchable: true, creatable: true })
    }
    expect(select('SVG Model').props('modelValue')).toBe('saved-svg')
    expect(api.config).toHaveBeenCalledOnce()
    expect(api.models).toHaveBeenCalledOnce()
    expect(api.saveConfig).not.toHaveBeenCalled()
    expect(api.syncKeys).not.toHaveBeenCalled()
    expect(setEnabled).toHaveBeenCalledWith(false)
  })

  it('keeps edited defaults and overrides when a delayed catalog response arrives', async () => {
    const pending = deferred<IntelligenceTestModelCatalog>()
    api.models.mockReturnValueOnce(pending.promise)
    render()
    await flushPromises()
    expect(select('SVG Model').props('options').map(option => option.value)).toContain('gpt-6.1-sol')
    select('SVG Model').vm.$emit('update:modelValue', 'my-new-model')
    select('Candy puzzle Group A Model').vm.$emit('update:modelValue', 'my-group-model')
    await flushPromises()
    pending.resolve(catalog(['latest-local-choice'], ['gpt-6.1-sol', 'my-group-model']))
    await flushPromises()
    expect(select('SVG Model').props('modelValue')).toBe('my-new-model')
    expect(select('Candy puzzle Group A Model').props('modelValue')).toBe('my-group-model')
    expect(select('SVG Model').props('options').map(option => option.value)).toEqual(expect.arrayContaining(['latest-local-choice', 'my-new-model', 'my-group-model']))
    // The server may have excluded a built-in name mapped to an image/video
    // model. Only explicit saved or draft choices may keep excluded names.
    expect(select('SVG Model').props('options').map(option => option.value)).not.toContain('gpt-6.1-sol')
    expect(api.config).toHaveBeenCalledOnce()
    expect(api.saveConfig).not.toHaveBeenCalled()
  })

  it('retains the draft and previous choices across a failed reload and allows retry', async () => {
    render()
    await flushPromises()
    select('SVG Model').vm.$emit('update:modelValue', 'typed-custom')
    await flushPromises()
    api.models.mockRejectedValueOnce(new Error('unavailable'))
    await reloadButton().trigger('click')
    await flushPromises()
    expect(wrapper!.text()).toContain('Your draft and existing choices are kept')
    expect(select('SVG Model').props('modelValue')).toBe('typed-custom')
    expect(select('SVG Model').props('options').map(option => option.value)).toContain('gpt-6.1-sol')
    api.models.mockResolvedValueOnce(catalog(['recovered-choice']))
    await reloadButton().trigger('click')
    await flushPromises()
    expect(wrapper!.text()).not.toContain('Model choices could not be read')
    expect(select('SVG Model').props('modelValue')).toBe('typed-custom')
    expect(select('SVG Model').props('options').map(option => option.value)).toContain('recovered-choice')
    expect(api.config).toHaveBeenCalledOnce()
  })

  it('exposes all effort values with translated labels and preserves raw default/group values when saving', async () => {
    render()
    await flushPromises()
    const effort = select('SVG Reasoning effort')
    const values = ['', 'none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max', 'ultra', 'persistent']
    expect(effort.props('options').map(option => option.value)).toEqual(values)
    expect(effort.props('options').find(option => option.value === 'xhigh')?.label).toBe('Extra high (xhigh)')
    expect(select('SVG Group A Reasoning effort').props('options').map(option => option.value)).toEqual(values)
    expect(select('SVG Group A Reasoning effort').props('options')[0].label).toBe('Inherit default')
    effort.vm.$emit('update:modelValue', 'max')
    select('Candy puzzle Group A Reasoning effort').vm.$emit('update:modelValue', 'persistent')
    select('SVG Model').vm.$emit('update:modelValue', 'not-in-any-catalog')
    await flushPromises()
    await wrapper!.get('form').trigger('submit')
    await flushPromises()
    expect(api.saveConfig).toHaveBeenCalledWith(expect.objectContaining({
      enabled: false,
      svg: expect.objectContaining({ model: 'not-in-any-catalog', effort: 'max', group_overrides: [expect.objectContaining({ effort: 'none' })] }),
      candy: expect.objectContaining({ effort: 'ultra', group_overrides: [expect.objectContaining({ effort: 'persistent' })] }),
    }))
    expect(api.syncKeys).not.toHaveBeenCalled()
  })

  it('supports actual search and custom creation while a first catalog load has failed', async () => {
    api.models.mockRejectedValueOnce(new Error('unavailable'))
    render()
    await flushPromises()
    const model = select('SVG Model')
    const values = model.props('options').map(option => option.value)
    expect(values).toEqual(expect.arrayContaining(['MiniMax-M3', 'mistral-large-latest', 'llama-3.3-70b-instruct', 'doubao-vision-pro-32k']))
    for (const media of ['gpt-image-2', 'gemini-3.1-flash-image', 'grok-imagine', 'grok-imagine-video', 'gpt-4o-audio-preview', 'gpt-4o-realtime-preview', 'cogview-3', 'cogvideo']) expect(values).not.toContain(media)
    await model.get('button').trigger('click')
    const search = document.body.querySelector<HTMLInputElement>('.select-search-input')!
    search.value = 'my-custom-text-model'
    search.dispatchEvent(new Event('input', { bubbles: true }))
    await flushPromises()
    const option = document.body.querySelector<HTMLElement>('[role="option"]')!
    expect(option.textContent).toContain('my-custom-text-model')
    option.click()
    await flushPromises()
    expect(model.props('modelValue')).toBe('my-custom-text-model')
    expect(api.saveConfig).not.toHaveBeenCalled()
  })

  it('shows Chinese effort labels without changing protocol values', async () => {
    render('zh')
    await flushPromises()
    const choices = select('SVG 思考强度').props('options')
    expect(choices.find(option => option.value === 'none')?.label).toBe('不启用推理 (none)')
    expect(choices.find(option => option.value === 'persistent')?.label).toBe('持续推理 (persistent)')
  })

  it('aborts outstanding reads on unmount and ignores their late results', async () => {
    const configRead = deferred<IntelligenceTestConfig>()
    const modelsRead = deferred<IntelligenceTestModelCatalog>()
    api.config.mockReturnValueOnce(configRead.promise)
    api.models.mockReturnValueOnce(modelsRead.promise)
    render()
    const configSignal = api.config.mock.calls[0][0] as AbortSignal
    const modelSignal = api.models.mock.calls[0][0] as AbortSignal
    wrapper!.unmount()
    wrapper = undefined
    expect(configSignal.aborted).toBe(true)
    expect(modelSignal.aborted).toBe(true)
    configRead.resolve({ ...settings(), enabled: true })
    modelsRead.resolve(catalog(['too-late']))
    await flushPromises()
    expect(setEnabled).not.toHaveBeenCalled()
    expect(api.saveConfig).not.toHaveBeenCalled()
  })
})
