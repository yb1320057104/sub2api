import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import IntelligenceTestSettings from '../IntelligenceTestSettings.vue'
import Select from '@/components/common/Select.vue'
import type { IntelligenceTestConfig, IntelligenceTestModelCatalog } from '@/api/intelligenceTest'
import en from '@/i18n/locales/en/intelligenceTest'
import zh from '@/i18n/locales/zh/intelligenceTest'

const api = vi.hoisted(() => ({ config: vi.fn(), models: vi.fn(), saveConfig: vi.fn(), syncKeys: vi.fn(), groups: vi.fn() }))
const setEnabled = vi.hoisted(() => vi.fn())
vi.mock('@/api/intelligenceTest', () => ({ intelligenceTestAPI: api }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: vi.fn() }) }))
vi.mock('@/stores/intelligenceTest', () => ({ useIntelligenceTestStore: () => ({ setEnabled }) }))

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
