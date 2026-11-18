/// <reference path="../../../../vite-env.d.ts" />
import { webcrypto } from 'node:crypto'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, type PropType } from 'vue'
import { enableAutoUnmount, flushPromises, mount, type DOMWrapper, type VueWrapper } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { createMemoryHistory, createRouter } from 'vue-router'
import KiloWorkbench from '../KiloWorkbench.vue'
import { parseKiloImport } from '../import'
import common from '../../../../i18n/locales/en/common'
import kilo from '../../../../i18n/locales/en/kilo'
import tokenGuardV2 from '../../../../i18n/locales/en/tokenGuardV2'
import type { KiloBatchInput, KiloEgress, KiloJob, KiloOnboardingBatchInput, KiloOnboardingImportInput, KiloOverview, KiloPrepared } from '../../../../api/admin/kilo'

const client = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: client }))

// Keep feature components and their API adapter real. These two shell controls
// remove Teleport/layout dependencies without replacing business validation.
const DialogShell = defineComponent({
  props: { show: Boolean, title: String },
  template: '<section v-if="show" role="dialog" :aria-label="title"><h2>{{ title }}</h2><slot /><footer><slot name="footer" /></footer></section>'
})
const SelectControl = defineComponent({
  props: {
    id: String,
    modelValue: { type: [String, Number, Boolean, Array], default: null },
    options: { type: Array as PropType<Array<{ value: string | number; label: string; disabled?: boolean }>>, default: () => [] },
    multiple: Boolean,
    disabled: Boolean,
    ariaLabel: String
  },
  emits: ['update:modelValue', 'change'],
  setup(props, { emit }) {
    function change(event: Event) {
      const input = event.target as HTMLSelectElement
      const values = Array.from(input.selectedOptions).flatMap(option => {
        const match = props.options.find(item => String(item.value) === option.value)
        return match ? [match.value] : []
      })
      const value = props.multiple ? values : values[0] ?? null
      emit('update:modelValue', value)
      emit('change', value)
    }
    return { change }
  },
  template: '<select :id="id" :value="modelValue" :multiple="multiple" :disabled="disabled" :aria-label="ariaLabel" @change="change"><option v-if="!multiple" value="" /><option v-for="option in options" :key="option.value" :value="option.value" :disabled="option.disabled">{{ option.label }}</option></select>'
})

const sources = [
  { id: 101, name: 'ChatGPT Alpha', email: 'alpha@example.test', status: 'active' },
  { id: 102, name: 'ChatGPT Beta', email: 'beta@example.test', status: 'active' }
]
function prepared(id: number, name: string, patch: Partial<KiloPrepared> = {}): KiloPrepared {
  return { id, name, email: `${id}@example.test`, identity_mode: 'independent', auth_method: 'api_key', organization_id: '', status: 'ready', account_id: null, source_account_id: null, login_proxy_id: 41, login_proxy_name: 'Fixed login', has_credential: true, last_error: '', created_at: '2026-11-12T00:00:00Z', ...patch }
}
function awaitingJob(): KiloJob {
  return { id: 90, revision: 3, recovery_actions: ['retry'], batch_id: 'accepted-batch', prepared_id: 12, prepared_name: 'Kilo Beta', source_account_id: 101, source_name: 'ChatGPT Alpha', account_id: null, stage: 'official_authorization', status: 'awaiting_authorization', last_error: '', verification_url: 'https://app.kilo.ai/byok', created_at: '2026-11-12T00:00:00Z', finished_at: null }
}
let overview: KiloOverview
let exits: KiloEgress[]
function response<T>(data: T) { return { data: structuredClone(data) } }

beforeEach(() => {
  vi.resetAllMocks()
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  vi.stubGlobal('crypto', webcrypto)
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
  localStorage.clear()
  sessionStorage.clear()
  overview = { prepared: [prepared(11, 'Kilo Alpha'), prepared(12, 'Kilo Beta')], jobs: [], encryption_ready: true }
  exits = []
  client.get.mockImplementation(async (url: string) => {
    if (url === '/admin/account-ops/token-guard-v2/encryption') return response({ configured: true, source: 'server_config' })
    if (url === '/admin/kilo/overview') return response(overview)
    if (url === '/admin/kilo/accounts/candidates') return response({ items: sources })
    if (url === '/admin/kilo/groups') return response({ items: [{ id: 7, name: 'OpenAI target' }] })
    if (url === '/admin/kilo/proxy-groups') return response({ items: [{ id: 9, name: 'Exclusive exits', active_proxy_count: 20 }] })
    if (url === '/admin/kilo/proxies') return response({ items: [{ id: 41, name: 'Exit one', status: 'active' }] })
    if (/^\/admin\/kilo\/accounts\/\d+\/egress$/.test(url)) return response({ items: exits })
    throw new Error(`Unexpected fake GET: ${url}`)
  })
  client.post.mockRejectedValue(new Error('Unexpected fake POST'))
  client.put.mockRejectedValue(new Error('Unexpected fake PUT'))
  client.delete.mockRejectedValue(new Error('Unexpected fake DELETE'))
})
enableAutoUnmount(afterEach)
afterEach(() => {
  vi.clearAllTimers()
  vi.useRealTimers()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

async function workspace(path = '/admin/kilo') {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:pathMatch(.*)*', component: { template: '<div />' } }] })
  await router.push(path)
  await router.isReady()
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: { ...common, kilo, tokenGuardV2 } } })
  const wrapper = mount(KiloWorkbench, { global: { plugins: [router, i18n], stubs: { BaseDialog: DialogShell, Select: SelectControl } } })
  await flushPromises()
  return wrapper
}
function button(root: Pick<VueWrapper | DOMWrapper<Element>, 'findAll'>, text: string) {
  const match = root.findAll('button').find(item => item.text() === text)
  if (!match) throw new Error(`Missing button: ${text}`)
  return match
}
function batchDialog(wrapper: VueWrapper) { return wrapper.get(`[role="dialog"][aria-label="${kilo.batch.title}"]`) }
async function selectPrepared(wrapper: VueWrapper, names = ['Kilo Beta', 'Kilo Alpha']) {
  for (const name of names) await wrapper.get(`input[aria-label="Select ${name}"]`).setValue(true)
  await button(wrapper, kilo.prepared.createBatch).trigger('click')
  await flushPromises()
}
async function configureBatch(wrapper: VueWrapper, selectedSources = ['ChatGPT Alpha', 'ChatGPT Beta']) {
  const dialog = batchDialog(wrapper)
  await dialog.get('#kilo-bind-sources').trigger('click')
  for (const name of selectedSources) {
    const label = dialog.findAll('label').find(item => item.text().includes(name))!
    await label.get('input[type="checkbox"]').setValue(true)
  }
  await dialog.get('#kilo-batch-proxy').setValue('9')
  await dialog.get('#kilo-batch-groups').setValue(['7'])
  await dialog.get('#kilo-byok-enabled-confirmed').setValue(true)
  const checkboxes = dialog.get('#kilo-batch-form').findAll('input[type="checkbox"]')
  await checkboxes[checkboxes.length - 1]!.setValue(true)
  return dialog
}
function recoveryKeys(operation: 'batch' | 'import' | 'onboarding' | 'onboarding-import' | 'unbind') {
  return Array.from({ length: sessionStorage.length }, (_, index) => sessionStorage.key(index)!).filter(key => key.startsWith(`kilo-pending-${operation}-`))
}

// The assertions follow the operator's observable result, including uncertain
// acknowledgments and authority-owned state, rather than internal refs.
describe('Kilo workspace batch and ownership contracts', () => {
  it('imports raw GPT materials through one atomic endpoint and recovers the request without browser plaintext storage', async () => {
    const submissions: KiloOnboardingImportInput[] = []
    const writes = vi.spyOn(Storage.prototype, 'setItem')
    const onboardJob = { ...awaitingJob(), batch_id: 'raw-import-batch', prepared_id: null, prepared_name: '', job_kind: 'onboard_source' as const }
    const originalGet = client.get.getMockImplementation()!
    client.get.mockImplementation(async (url: string, ...args: unknown[]) => url === '/admin/kilo/onboarding/batches/raw-import-batch'
      ? response({ batch_id: 'raw-import-batch', items: [onboardJob] }) : originalGet(url, ...args))
    client.post.mockImplementation(async (url: string, input: KiloOnboardingImportInput) => {
      expect(url).toBe('/admin/kilo/onboarding/import')
      submissions.push(structuredClone(input))
      if (submissions.length === 1) throw new Error('Acknowledgment lost')
      return response({ batch_id: 'raw-import-batch', items: [{ line: 1, source_account_id: 101, job_id: 90, status: 'queued' }] })
    })
    const configure = async (wrapper: Awaited<ReturnType<typeof workspace>>) => {
      await wrapper.get('[data-testid="kilo-open-two-fa"]').trigger('click')
      await flushPromises()
      const form = wrapper.get('[data-testid="kilo-two-fa-import"]')
      await form.get('#kilo-two-fa-credentials').setValue('raw@example.test----fixture-password-only----FIXTURETOTP')
      await form.get('[data-testid="kilo-two-fa-preview"]').trigger('click')
      await form.get('#kilo-two-fa-0-login').setValue('41')
      await form.get('#kilo-import-routing-groups').setValue(['7'])
      await form.get('#kilo-import-routing-pool').setValue('9')
      expect(form.findAll('input[type="radio"]').find(input => input.attributes('value') === 'prefer_idle')!.element).toHaveProperty('checked', true)
      expect(form.find('input[type="password"]').exists()).toBe(false)
      expect(form.find('#kilo-two-fa-0-organization').exists()).toBe(false)
      expect((form.get('#kilo-two-fa-0-workspace').element as HTMLSelectElement).value).toBe('auto')
      return form
    }
    let wrapper = await workspace()
    let form = await configure(wrapper)
    await form.get('[data-testid="kilo-two-fa-submit"]').trigger('click')
    await vi.waitFor(() => expect(submissions).toHaveLength(1))
    await flushPromises()
    expect(submissions[0]).toMatchObject({ binding_strategy: 'prefer_idle', items: [{ email: 'raw@example.test', password: 'fixture-password-only', totp_secret: 'FIXTURETOTP', login_proxy_id: 41, workspace_mode: 'auto', organization_id: '', verified_model_ids: [] }], group_ids: [7], activate_when_ready: true, routing: { proxy_group_id: 9, proxies_per_account: 5, proxy_concurrency: 20, account_concurrency: 100 } })
    expect(recoveryKeys('onboarding-import')).toHaveLength(1)
    expect(form.text()).toContain(kilo.errors.onboarding)
    wrapper.unmount()
    wrapper = await workspace()
    form = await configure(wrapper)
    await form.get('[data-testid="kilo-two-fa-submit"]').trigger('click')
    await vi.waitFor(() => expect(submissions).toHaveLength(2))
    await flushPromises()
    expect(submissions[1]).toEqual(submissions[0])
    expect(recoveryKeys('onboarding-import')).toEqual([])
    expect(wrapper.find('[data-testid="kilo-two-fa-import"]').exists()).toBe(false)
    expect(client.get).toHaveBeenCalledWith('/admin/kilo/onboarding/batches/raw-import-batch', { signal: expect.any(AbortSignal) })
    for (const [key, value] of writes.mock.calls) {
      expect(`${key}:${value}`).not.toContain('fixture-password-only')
      expect(`${key}:${value}`).not.toContain('FIXTURETOTP')
    }
  })

  it('targets a reusable slot with its existing login proxy and never mistakes disabled exits for a banned identity', async () => {
    overview.prepared = [
      prepared(11, 'Reusable identity', { status: 'ready', account_id: 501, login_proxy_id: 77, organization_id: 'saved-org', identity_mode: 'independent', identity_status: 'active', binding_state: 'empty', binding_revision: 4, reusable: true }),
      prepared(12, 'Exits stopped', { status: 'disabled', account_id: 502, source_account_id: 102, identity_status: 'active', binding_state: 'recovering', reusable: false, recovery_task_id: 55 })
    ]
    const wrapper = await workspace()
    expect(wrapper.get('[data-testid="kilo-pool-idle"]').text()).toBe('1 reusable idle slots')
    const states = wrapper.findAll('[data-testid="kilo-pool-status"]')
    expect(states[1]!.text()).toContain(kilo.pool.identity.active)
    expect(states[1]!.text()).toContain(kilo.pool.binding.recovering)
    expect(states[1]!.text()).not.toContain(kilo.pool.identity.banned)
    await wrapper.get('[data-testid="kilo-fill-slot"]').trigger('click')
    await flushPromises()
    const form = wrapper.get('[data-testid="kilo-two-fa-import"]')
    await form.get('#kilo-two-fa-credentials').setValue('replacement@example.test----fixture-password-only----FIXTURETOTP')
    await form.get('[data-testid="kilo-two-fa-preview"]').trigger('click')
    expect(form.find('#kilo-two-fa-0-login').exists()).toBe(false)
    expect(form.text()).toContain('proxy #77')
    await form.get('#kilo-import-routing-groups').setValue(['7'])
    await form.get('#kilo-import-routing-pool').setValue('9')
    client.post.mockResolvedValue(response({ batch_id: 'specific-slot', items: [] }))
    await form.get('[data-testid="kilo-two-fa-submit"]').trigger('click')
    await vi.waitFor(() => expect(client.post).toHaveBeenCalledTimes(1))
    expect(client.post.mock.calls[0]![1]).toMatchObject({ binding_strategy: 'prefer_idle', items: [{ prepared_id: 11, login_proxy_id: 77, organization_id: 'saved-org' }] })
  })

  it('only offers verified independent slots even when coupled or unknown snapshots claim reuse eligibility', async () => {
    overview.prepared = [
      prepared(11, 'Independent slot', { identity_mode: 'independent', identity_status: 'active', binding_state: 'empty', reusable: true }),
      prepared(12, 'Coupled slot', { identity_mode: 'gpt_coupled', identity_status: 'active', binding_state: 'empty', reusable: true }),
      prepared(13, 'Unknown different email', { identity_mode: 'unknown', identity_status: 'active', binding_state: 'empty', reusable: true }),
      prepared(14, 'Retired slot', { identity_mode: 'independent', identity_status: 'retired', binding_state: 'empty', reusable: true })
    ]
    const wrapper = await workspace()
    expect(wrapper.get('[data-testid="kilo-pool-idle"]').text()).toBe('1 reusable idle slots')
    expect(wrapper.findAll('[data-testid="kilo-fill-slot"]')).toHaveLength(1)
    await wrapper.get('[data-testid="kilo-open-two-fa"]').trigger('click')
    await flushPromises()
    const form = wrapper.get('[data-testid="kilo-two-fa-import"]')
    await form.get('#kilo-two-fa-credentials').setValue('replacement@example.test----fixture-password-only----FIXTURETOTP')
    await form.get('[data-testid="kilo-two-fa-preview"]').trigger('click')
    expect(form.get('#kilo-two-fa-0-slot').findAll('option').map(option => option.attributes('value'))).toEqual(['', '0', '11'])
    expect(form.text()).toContain(kilo.pool.newIdentityHint)
    expect(form.text()).toContain(kilo.twoFA.slotHint)
    expect(client.post).not.toHaveBeenCalled()
  })

  it('verifies an identity from its row, blocks duplicate actions and reloads the official classification', async () => {
    overview.prepared = [prepared(11, 'Needs verification', { identity_mode: 'unknown', identity_status: 'active', binding_state: 'empty', reusable: true })]
    let complete!: (value: ReturnType<typeof response>) => void
    client.post.mockImplementation(() => new Promise(resolve => { complete = resolve }))
    const wrapper = await workspace()
    expect(wrapper.find('[data-testid="kilo-fill-slot"]').exists()).toBe(false)
    const initialLoads = client.get.mock.calls.filter(call => call[0] === '/admin/kilo/overview').length
    await wrapper.get('[data-testid="kilo-refresh-identity"]').trigger('click')
    expect(client.post.mock.calls).toEqual([['/admin/kilo/prepared/11/identity/refresh', {}, { signal: expect.any(AbortSignal), timeout: 60000 }]])
    expect(wrapper.get('[data-testid="kilo-refresh-identity"]').text()).toBe(kilo.pool.refreshingIdentity)
    expect(wrapper.get('[data-testid="kilo-refresh-identity"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="kilo-refresh-identity"]').trigger('click')
    expect(client.post).toHaveBeenCalledTimes(1)
    overview.prepared = [prepared(11, 'Needs verification', { identity_mode: 'independent', identity_status: 'active', binding_state: 'empty', reusable: true, identity_checked_at: '2026-11-15T12:00:00Z' })]
    complete(response({ mode: 'independent' }))
    await flushPromises()
    expect(client.get.mock.calls.filter(call => call[0] === '/admin/kilo/overview')).toHaveLength(initialLoads + 1)
    expect(wrapper.get('[data-testid="kilo-identity-mode"]').text()).toBe(kilo.pool.mode.independent)
    expect(wrapper.find('[data-testid="kilo-fill-slot"]').exists()).toBe(true)
    expect(wrapper.text()).toContain(kilo.pool.identityRefreshed)
    expect(wrapper.get('[data-testid="kilo-refresh-identity"]').attributes('disabled')).toBeUndefined()
  })

  it('keeps an unverified key out of the pool when identity verification fails and permits a later retry', async () => {
    overview.prepared = [prepared(11, 'Unknown key', { identity_mode: 'unknown', identity_status: 'active', binding_state: 'empty', reusable: true })]
    client.post.mockRejectedValueOnce({ status: 503, message: 'Official identity provider unavailable' })
    const wrapper = await workspace()
    await wrapper.get('[data-testid="kilo-refresh-identity"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Official identity provider unavailable')
    expect(wrapper.text()).not.toContain(kilo.pool.identityRefreshed)
    expect(wrapper.find('[data-testid="kilo-fill-slot"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="kilo-refresh-identity"]').attributes('disabled')).toBeUndefined()
  })

  it('warns that unbinding a coupled identity retires it instead of returning a cross-GPT slot', async () => {
    overview.prepared = [prepared(11, 'Coupled identity', { status: 'bound', identity_mode: 'gpt_coupled', identity_status: 'active', binding_state: 'bound', source_account_id: 126, identity_source_account_id: 122, account_id: 501, reusable: false })]
    client.post.mockImplementation(async () => {
      overview.prepared = [prepared(11, 'Coupled identity', { status: 'disabled', identity_mode: 'gpt_coupled', identity_status: 'retired', binding_state: 'blocked', identity_source_account_id: 122, account_id: 501, reusable: false })]
      return response({ status: 'queued' })
    })
    const wrapper = await workspace()
    expect(wrapper.get('[data-testid="kilo-identity-origin"]').text()).toContain('#122')
    await wrapper.get('[data-testid="kilo-unbind"]').trigger('click')
    const dialog = wrapper.get(`[role="dialog"][aria-label="${kilo.pool.unbindTitle}"]`)
    expect(dialog.text()).toContain(kilo.pool.unbindCoupledConfirmation.replace('{name}', 'Coupled identity'))
    await dialog.get('[data-testid="kilo-confirm-unbind"]').trigger('click')
    await vi.waitFor(() => expect(client.post).toHaveBeenCalledTimes(1))
    await flushPromises()
    expect(wrapper.text()).toContain(kilo.pool.unbindCoupledQueued)
    expect(wrapper.get('[data-testid="kilo-identity-retired"]').text()).toBe(kilo.pool.retiredHint)
    expect(wrapper.find('[data-testid="kilo-fill-slot"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="kilo-unbind"]').exists()).toBe(false)
  })

  it.each<[string, Partial<KiloPrepared>, string, string | null]>([
    ['allows an independent identity', { identity_mode: 'independent' }, 'ChatGPT Beta', null],
    ['allows a coupled identity with its original source', { identity_mode: 'gpt_coupled', identity_source_account_id: 101 }, 'ChatGPT Alpha', null],
    ['rejects a coupled identity with a different source', { identity_mode: 'gpt_coupled', identity_source_account_id: 101 }, 'ChatGPT Beta', kilo.pool.modeHint.gpt_coupled],
    ['rejects a coupled identity without an original source', { identity_mode: 'gpt_coupled', identity_source_account_id: null }, 'ChatGPT Alpha', kilo.pool.modeHint.gpt_coupled],
    ['rejects an unknown identity even with a matching email', { identity_mode: 'unknown', email: 'alpha@example.test' }, 'ChatGPT Alpha', kilo.pool.modeHint.unknown],
    ['rejects a retired identity', { identity_mode: 'independent', identity_status: 'retired' }, 'ChatGPT Alpha', kilo.batch.notReady]
  ])('legacy pairing %s, including direct form submission', async (_name, patch, sourceName, expectedError) => {
    overview.prepared = [prepared(11, 'Kilo Alpha', { identity_status: 'active' })]
    const wrapper = await workspace()
    await selectPrepared(wrapper, ['Kilo Alpha'])
    const dialog = await configureBatch(wrapper, [sourceName])
    // Refresh while the dialog is open to exercise current identity policy, not its opening snapshot.
    overview.prepared = [prepared(11, 'Kilo Alpha', { identity_status: 'active', ...patch })]
    await button(wrapper, common.common.refresh).trigger('click')
    await flushPromises()
    await dialog.get('#kilo-byok-enabled-confirmed').setValue(true)
    const confirmations = dialog.get('#kilo-batch-form').findAll('input[type="checkbox"]')
    await confirmations[confirmations.length - 1]!.setValue(true)
    client.post.mockResolvedValue(response({ batch_id: 'legacy-pairing-batch' }))
    const submit = dialog.get('button[form="kilo-batch-form"]')
    if (expectedError) {
      expect(dialog.text()).toContain(expectedError)
      expect(submit.attributes('disabled')).toBeDefined()
    } else {
      expect(submit.attributes('disabled')).toBeUndefined()
    }
    await dialog.get('#kilo-batch-form').trigger('submit')
    await flushPromises()
    if (expectedError) {
      expect(client.post).not.toHaveBeenCalled()
    } else {
      await vi.waitFor(() => expect(client.post).toHaveBeenCalledTimes(1))
      expect(client.post.mock.calls[0]![1]).toMatchObject({ prepared_ids: [11], source_account_ids: [sourceName === 'ChatGPT Alpha' ? 101 : 102] })
    }
  })

  it.each<[string, string[], boolean]>([
    ['accepts the original source at each pairing position', ['ChatGPT Beta', 'ChatGPT Alpha'], true],
    ['rejects swapped coupled sources even when both originals are selected', ['ChatGPT Alpha', 'ChatGPT Beta'], false]
  ])('legacy pairing %s', async (_name, sourceNames, allowed) => {
    overview.prepared = [
      prepared(11, 'Kilo Alpha', { identity_mode: 'gpt_coupled', identity_source_account_id: 102 }),
      prepared(12, 'Kilo Beta', { identity_mode: 'gpt_coupled', identity_source_account_id: 101 })
    ]
    const wrapper = await workspace()
    await selectPrepared(wrapper, ['Kilo Alpha', 'Kilo Beta'])
    const dialog = await configureBatch(wrapper, sourceNames)
    client.post.mockResolvedValue(response({ batch_id: 'ordered-coupled-batch' }))
    expect(dialog.get('button[form="kilo-batch-form"]').attributes('disabled') === undefined).toBe(allowed)
    await dialog.get('#kilo-batch-form').trigger('submit')
    await flushPromises()
    if (allowed) {
      await vi.waitFor(() => expect(client.post).toHaveBeenCalledTimes(1))
      expect(client.post.mock.calls[0]![1]).toMatchObject({ prepared_ids: [11, 12], source_account_ids: [102, 101] })
    } else {
      expect(dialog.text()).toContain(kilo.pool.modeHint.gpt_coupled)
      expect(client.post).not.toHaveBeenCalled()
    }
  })

  it('queues protocol unbinding separately from exit release and retains the request ID when its acknowledgment is lost', async () => {
    overview.prepared = [prepared(11, 'Bound identity', { status: 'bound', source_account_id: 101, account_id: 501, identity_status: 'active', binding_state: 'bound', binding_revision: 3, reusable: false })]
    const submissions: unknown[] = []
    client.post.mockImplementation(async (url: string, input: unknown) => {
      expect(url).toBe('/admin/kilo/prepared/11/unbind')
      submissions.push(structuredClone(input))
      if (submissions.length === 1) throw new Error('Acknowledgment lost')
      overview.prepared = overview.prepared.map(item => ({ ...item, binding_state: 'unbinding', reusable: false }))
      return response({ status: 'queued' })
    })
    const wrapper = await workspace()
    await wrapper.get('[data-testid="kilo-unbind"]').trigger('click')
    const dialog = wrapper.get(`[role="dialog"][aria-label="${kilo.pool.unbindTitle}"]`)
    await dialog.get('[data-testid="kilo-confirm-unbind"]').trigger('click')
    await vi.waitFor(() => expect(submissions).toHaveLength(1))
    await flushPromises()
    expect(recoveryKeys('unbind')).toHaveLength(1)
    expect(dialog.text()).toContain(kilo.pool.unbindFailed)
    await dialog.get('[data-testid="kilo-confirm-unbind"]').trigger('click')
    await vi.waitFor(() => expect(submissions).toHaveLength(2))
    await flushPromises()
    expect(submissions[1]).toEqual(submissions[0])
    expect(recoveryKeys('unbind')).toEqual([])
    expect(wrapper.text()).toContain(kilo.pool.unbindQueued)
    expect(wrapper.text()).toContain(kilo.pool.binding.unbinding)
    expect(wrapper.find('[data-testid="kilo-fill-slot"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="kilo-unbind"]').exists()).toBe(false)
  })

  it('onboards a source without prepared credentials, enforces activation scope and recovers an uncertain submission into batch details', async () => {
    overview.prepared = []
    const submissions: KiloOnboardingBatchInput[] = []
    const onboardJob: KiloJob = {
      ...awaitingJob(), batch_id: 'onboarding-batch', prepared_id: null, prepared_name: '', job_kind: 'onboard_source',
      stage: 'subscription_authorization', error_code: 'ACTION_REQUIRED', failed_stage: 'subscription_authorization',
      organization_id: 'org-fixture', login_proxy_id: 41, next_action: 'Confirm this subscription in the official Kilo session.',
      credential_ready: false, subscription_connected: false, quota_oauth_ready: true
    }
    const originalGet = client.get.getMockImplementation()!
    client.get.mockImplementation(async (url: string, ...args: unknown[]) => url === '/admin/kilo/onboarding/batches/onboarding-batch'
      ? response({ batch_id: 'onboarding-batch', items: [onboardJob] }) : originalGet(url, ...args))
    client.post.mockImplementation(async (url: string, input: KiloOnboardingBatchInput) => {
      expect(url).toBe('/admin/kilo/onboarding/batches')
      submissions.push(structuredClone(input))
      if (submissions.length === 1) throw new Error('Acknowledgment lost')
      return response({ batch_id: 'onboarding-batch', items: [{ line: 1, source_account_id: 101, job_id: onboardJob.id, status: 'queued' }] })
    })
    let wrapper = await workspace('/admin/kilo?source_account_id=101')
    const configure = async () => {
      await button(wrapper, kilo.onboarding.action).trigger('click')
      await flushPromises()
      const dialog = wrapper.get(`[role="dialog"][aria-label="${kilo.onboarding.title}"]`)
      expect(dialog.text()).toContain('ChatGPT Alpha')
      expect(dialog.find('input[type="password"]').exists()).toBe(false)
      await dialog.get('#kilo-onboarding-101-login').setValue('41')
      await dialog.get('#kilo-onboarding-101-workspace').setValue('business')
      await dialog.get('#kilo-onboarding-101-organization').setValue('org-fixture')
      await dialog.get('#kilo-onboarding-groups').setValue(['7'])
      await dialog.get('#kilo-onboarding-proxy-group').setValue('9')
      expect(dialog.get('#kilo-onboarding-101-models').attributes('placeholder')).toBe(kilo.onboarding.modelsPlaceholder)
      await dialog.get('#kilo-onboarding-101-models').setValue('openai/*')
      await dialog.get('#kilo-onboarding-confirm').setValue(true)
      await dialog.get('#kilo-onboarding-form').trigger('submit')
      await flushPromises()
      expect(client.post).not.toHaveBeenCalled()
      expect(dialog.text()).toContain(kilo.onboarding.invalidModels)
      await dialog.get('#kilo-onboarding-101-models').setValue('')
      await dialog.get('#kilo-onboarding-confirm').setValue(true)
      return dialog
    }
    let dialog = await configure()
    await dialog.get('#kilo-onboarding-form').trigger('submit')
    await vi.waitFor(() => expect(submissions).toHaveLength(1))
    await flushPromises()
    expect(submissions[0]).toMatchObject({ items: [{ source_account_id: 101, name: 'Kilo-ChatGPT Alpha', login_proxy_id: 41, workspace_mode: 'business', organization_id: 'org-fixture', verified_model_ids: [] }], group_ids: [7], activate_when_ready: true, routing: { proxy_group_id: 9, proxies_per_account: 5, proxy_concurrency: 20, account_concurrency: 100 } })
    expect(recoveryKeys('onboarding')).toHaveLength(1)
    expect(dialog.text()).toContain(kilo.errors.onboarding)
    wrapper.unmount()

    wrapper = await workspace('/admin/kilo?source_account_id=101')
    client.post.mockClear()
    dialog = await configure()
    await dialog.get('#kilo-onboarding-form').trigger('submit')
    await vi.waitFor(() => expect(submissions).toHaveLength(2))
    await flushPromises()
    expect(submissions[1]).toEqual(submissions[0])
    expect(recoveryKeys('onboarding')).toEqual([])
    expect(wrapper.find(`[role="dialog"][aria-label="${kilo.onboarding.title}"]`).exists()).toBe(false)
    expect(client.get).toHaveBeenCalledWith('/admin/kilo/onboarding/batches/onboarding-batch', { signal: expect.any(AbortSignal) })
    expect(wrapper.text()).toContain(kilo.jobs.credentialPending)
    expect(wrapper.text()).toContain('ACTION_REQUIRED')
    expect(wrapper.text()).toContain(onboardJob.next_action)
    expect(wrapper.text()).toContain(kilo.jobs.quotaBoundary)
    expect(wrapper.text()).not.toContain('Gateway account #0')
  })

  it('preserves ordered pair previews and reuses an uncertain batch ID across closing and reloading, then shows awaiting authorization', async () => {
    const submissions: KiloBatchInput[] = []
    client.post.mockImplementation(async (url: string, payload: KiloBatchInput) => {
      expect(url).toBe('/admin/kilo/batches')
      submissions.push(structuredClone(payload))
      if (submissions.length === 1) throw new Error('Acknowledgment lost')
      overview = { ...overview, prepared: overview.prepared.map(item => ({ ...item, status: 'binding' as const })), jobs: [awaitingJob()] }
      return response({ batch_id: 'accepted-batch' })
    })
    let wrapper = await workspace()
    await selectPrepared(wrapper)
    let dialog = await configureBatch(wrapper)
    const pairs = dialog.findAll('ol > li')
    expect(pairs.map(item => item.text())).toEqual([
      expect.stringContaining('Kilo BetaExpected ChatGPT: ChatGPT Alpha (alpha@example.test)'),
      expect.stringContaining('Kilo AlphaExpected ChatGPT: ChatGPT Beta (beta@example.test)')
    ])
    await dialog.get('#kilo-batch-form').trigger('submit')
    await vi.waitFor(() => expect(submissions).toHaveLength(1))
    await flushPromises()
    expect(submissions[0]).toMatchObject({ prepared_ids: [12, 11], source_account_ids: [101, 102], byok_enabled_confirmed: true, group_ids: [7], proxy_group_id: 9 })
    expect(dialog.text()).toContain(kilo.errors.batch)
    expect(recoveryKeys('batch')).toHaveLength(1)
    expect(sessionStorage.getItem(recoveryKeys('batch')[0]!)).toBe(submissions[0]!.request_id)
    await button(dialog, common.common.cancel).trigger('click')
    wrapper.unmount()

    wrapper = await workspace()
    await selectPrepared(wrapper)
    dialog = await configureBatch(wrapper)
    await dialog.get('#kilo-batch-form').trigger('submit')
    await vi.waitFor(() => expect(submissions).toHaveLength(2))
    await flushPromises()
    expect(submissions[1]).toEqual(submissions[0])
    expect(recoveryKeys('batch')).toEqual([])
    expect(wrapper.find(`[role="dialog"][aria-label="${kilo.batch.title}"]`).exists()).toBe(false)
    const job = wrapper.get('ol > li')
    expect(job.text()).toContain(kilo.status.awaiting_authorization)
    expect(job.text()).not.toContain(kilo.status.bound)
    expect(job.text()).not.toContain('Processing succeeded')
    expect(job.get('a').attributes('href')).toBe('https://app.kilo.ai/byok')
  })

  it('offers the next pairing step after device login without pretending that a source is connected', async () => {
    overview.prepared = [prepared(1, 'Ready device Kilo', { auth_method: 'device', account_id: 118, login_proxy_id: 10, login_proxy_name: 'Login ten' })]
    const wrapper = await workspace()
    expect(wrapper.text()).toContain(kilo.prepared.awaitingPair)
    expect(wrapper.text()).toContain('Fixed login: Login ten (proxy #10)')
    expect(wrapper.text()).toContain(kilo.prepared.noSource)
    await button(wrapper, kilo.prepared.pair).trigger('click')
    await flushPromises()
    const dialog = batchDialog(wrapper)
    expect(dialog.get('#kilo-bind-sources').attributes('aria-checked')).toBe('true')
    expect(dialog.text()).toContain('ChatGPT Alpha')
    expect(client.post).not.toHaveBeenCalled()
  })

  it('prefills a single source and submits separate fixed-login and default request routing', async () => {
    overview.prepared = [prepared(11, 'Kilo Alpha', { login_proxy_id: null })]
    const wrapper = await workspace('/admin/kilo?source_account_id=101')
    await selectPrepared(wrapper, ['Kilo Alpha'])
    const dialog = batchDialog(wrapper)
    expect(dialog.text()).toContain('Expected ChatGPT: ChatGPT Alpha (alpha@example.test)')
    expect(dialog.get('#kilo-bind-sources').attributes('disabled')).toBeDefined()
    expect((dialog.get('#kilo-egress-count').element as HTMLInputElement).value).toBe('5')
    expect((dialog.get('#kilo-proxy-concurrency').element as HTMLInputElement).value).toBe('20')
    expect(dialog.text()).toContain('Different proxies may share the same public IP without affecting their separate concurrency.')
    expect(dialog.text()).toContain(kilo.batch.egressHint)
    expect((dialog.get('#kilo-account-concurrency').element as HTMLInputElement).value).toBe('100')
    await dialog.get('#kilo-batch-proxy').setValue('9')
    await dialog.get('#kilo-batch-groups').setValue(['7'])
    expect(dialog.text()).toContain(kilo.routing.loginRequired)
    await dialog.get('#kilo-batch-login-11').setValue('41')
    const checkboxes = dialog.get('#kilo-batch-form').findAll('input[type="checkbox"]')
    await checkboxes[checkboxes.length - 1]!.setValue(true)
    client.post.mockResolvedValue(response({ batch_id: 'source-batch' }))
    expect((dialog.get('#kilo-byok-enabled-confirmed').element as HTMLInputElement).checked).toBe(false)
    expect(dialog.text()).toContain(kilo.batch.byokEnabledRequired)
    expect(dialog.get('button[form="kilo-batch-form"]').attributes('disabled')).toBeDefined()
    await dialog.get('#kilo-batch-form').trigger('submit')
    await flushPromises()
    expect(client.post).not.toHaveBeenCalled()
    await dialog.get('#kilo-byok-enabled-confirmed').setValue(true)
    await checkboxes[checkboxes.length - 1]!.setValue(true)
    await dialog.get('#kilo-batch-form').trigger('submit')
    await vi.waitFor(() => expect(client.post).toHaveBeenCalledTimes(1))
    expect(client.post.mock.calls[0]![1]).toMatchObject({ prepared_ids: [11], source_account_ids: [101], byok_enabled_confirmed: true, login_proxy_ids: { 11: 41 }, proxies_per_account: 5, proxy_concurrency: 20, account_concurrency: 100, auto_release_statuses: [401, 402, 403] })
  })

  it('allows quota-only activation without a BYOK enable confirmation', async () => {
    const wrapper = await workspace()
    await selectPrepared(wrapper, ['Kilo Alpha'])
    const dialog = batchDialog(wrapper)
    expect(dialog.find('#kilo-byok-enabled-confirmed').exists()).toBe(false)
    await dialog.get('#kilo-batch-proxy').setValue('9')
    await dialog.get('#kilo-batch-groups').setValue(['7'])
    const checkboxes = dialog.get('#kilo-batch-form').findAll('input[type="checkbox"]')
    await checkboxes[checkboxes.length - 1]!.setValue(true)
    client.post.mockResolvedValue(response({ batch_id: 'quota-batch' }))
    await dialog.get('#kilo-batch-form').trigger('submit')
    await vi.waitFor(() => expect(client.post).toHaveBeenCalledTimes(1))
    expect(client.post.mock.calls[0]![1]).toMatchObject({ source_account_ids: [], byok_enabled_confirmed: false })
  })

  it('keeps the saved login proxy fixed and allows request configuration only while ready', async () => {
    overview.prepared = [prepared(11, 'Kilo Alpha')]
    client.put.mockResolvedValue(response({ status: 'saved' }))
    const wrapper = await workspace()
    await button(wrapper, kilo.routing.action).trigger('click')
    await flushPromises()
    let dialog = wrapper.get('[role="dialog"][aria-label="Login and request settings for Kilo Alpha"]')
    expect(dialog.text()).toContain('Fixed login: Fixed login (proxy #41)')
    expect(dialog.find('#kilo-routing-login').exists()).toBe(false)
    await dialog.get('#kilo-routing-group').setValue('9')
    await dialog.get('#kilo-routing-statuses').setValue('402, 403')
    await dialog.get('#kilo-routing-form').trigger('submit')
    await flushPromises()
    expect(client.put.mock.calls[0]).toEqual(['/admin/kilo/prepared/11/routing', { login_proxy_id: 41, proxy_group_id: 9, proxies_per_account: 5, proxy_concurrency: 20, account_concurrency: 100, priority: 1, auto_release_statuses: [402, 403] }, { signal: expect.any(AbortSignal) }])
    overview.prepared = [prepared(11, 'Kilo Alpha', { status: 'bound', account_id: 501 })]
    await button(wrapper, common.common.refresh).trigger('click')
    await flushPromises()
    await button(wrapper, kilo.routing.action).trigger('click')
    await flushPromises()
    dialog = wrapper.get('[role="dialog"][aria-label="Login and request settings for Kilo Alpha"]')
    expect(dialog.get('#kilo-routing-statuses').attributes('disabled')).toBeDefined()
    await dialog.get('#kilo-routing-form').trigger('submit')
    expect(client.put).toHaveBeenCalledTimes(1)
  })

  it('blocks submission when prepared and source counts differ, even if the form is submitted directly', async () => {
    const wrapper = await workspace()
    await selectPrepared(wrapper)
    const dialog = await configureBatch(wrapper, ['ChatGPT Alpha'])
    expect(dialog.text()).toContain('2 prepared accounts and 1 source accounts selected. The counts must match.')
    expect(dialog.get('button[form="kilo-batch-form"]').attributes('disabled')).toBeDefined()
    await dialog.get('#kilo-batch-form').trigger('submit')
    await flushPromises()
    expect(client.post).not.toHaveBeenCalled()
    expect(recoveryKeys('batch')).toEqual([])
  })

  it('retains disabled account associations and draining exits after in-flight release conflicts without announcing success', async () => {
    overview.prepared = [prepared(11, 'Kilo Alpha', { status: 'bound', account_id: 501, source_account_id: 101, login_proxy_id: 40 })]
    const conflict = 'Exit still has 2 requests in flight; retry after draining.'
    client.post.mockImplementation(async (url: string) => {
      expect(url).toBe('/admin/kilo/prepared/11/release')
      overview.prepared = overview.prepared.map(item => ({ ...item, status: 'disabled' as const }))
      throw { status: 409, message: conflict }
    })
    exits = [{ id: 61, account_id: 501, proxy_id: 41, egress_ip: '198.51.100.4', state: 'active', verified_at: '2026-11-12T00:00:00Z', max_concurrency: 3, in_flight: 2, last_error: '' }]
    client.delete.mockImplementation(async (url: string) => {
      expect(url).toBe('/admin/kilo/egress/61')
      exits = exits.map(item => ({ ...item, state: 'draining' }))
      throw { status: 409, message: conflict }
    })
    const wrapper = await workspace()
    await button(wrapper, kilo.release.action).trigger('click')
    expect(client.post).not.toHaveBeenCalled()
    let dialog = wrapper.get(`[role="dialog"][aria-label="${kilo.release.title}"]`)
    await button(dialog, kilo.release.confirm).trigger('click')
    await flushPromises()
    expect(dialog.text()).toContain(conflict)
    expect(wrapper.text()).toContain(kilo.status.disabled)
    expect(wrapper.text()).toContain('Managed account #501')
    expect(wrapper.text()).toContain('Source account #101')
    expect(wrapper.text()).not.toContain(kilo.release.requested)
    await button(dialog, common.common.cancel).trigger('click')
    await button(wrapper, kilo.egress.action).trigger('click')
    await flushPromises()
    dialog = wrapper.get('[role="dialog"][aria-label="Request proxies for Kilo Alpha"]')
    await button(dialog, kilo.egress.release).trigger('click')
    expect(client.delete).not.toHaveBeenCalled()
    await button(dialog, kilo.egress.confirmRelease).trigger('click')
    await flushPromises()
    expect(client.delete).toHaveBeenCalledTimes(1)
    expect(dialog.text()).toContain(conflict)
    expect(dialog.text()).toContain('198.51.100.4')
    expect(dialog.text()).toContain(kilo.status.draining)
    expect(button(dialog, kilo.egress.finishRelease).exists()).toBe(true)
    expect(dialog.text()).not.toContain(kilo.egress.released)
  })

  it('shows proxy occupancy and enables only available or current-account proxies', async () => {
    overview.prepared = [prepared(11, 'Kilo Alpha', { status: 'bound', account_id: 501 })]
    const original = client.get.getMockImplementation()!
    client.get.mockImplementation(async (url: string, ...args: unknown[]) => {
      if (url === '/admin/kilo/proxies') return response({ items: [
        { id: 41, name: 'Owned exit', status: 'active', owned: true, owner_account_id: 501, owner_account_name: 'Kilo Alpha', owner_deleted: false },
        { id: 42, name: 'Other exit', status: 'active', owned: true, owner_account_id: 502, owner_account_name: 'Kilo Beta', owner_deleted: false },
        { id: 43, name: 'Free exit', status: 'active', owned: false, owner_account_id: null, owner_account_name: '', owner_deleted: false },
        { id: 44, name: 'Deleted exit', status: 'active', owned: true, owner_account_id: 503, owner_account_name: '', owner_deleted: true }
      ] })
      return original(url, ...args)
    })
    exits = [{ id: 60, account_id: 501, proxy_id: 41, egress_ip: '198.51.100.10', state: 'active', verified_at: null, max_concurrency: 1, in_flight: 0, last_error: '' }]
    const wrapper = await workspace()
    await button(wrapper, kilo.egress.action).trigger('click')
    await flushPromises()
    const exitDialog = wrapper.get('[role="dialog"][aria-label="Request proxies for Kilo Alpha"]')
    expect(exitDialog.text()).toContain(kilo.routing.fixedLogin)
    expect(exitDialog.findAll('button').some(item => item.text() === kilo.egress.release)).toBe(false)
    const select = wrapper.get('#kilo-add-proxy')
    expect(select.get('option[value="41"]').attributes('disabled')).toBeUndefined()
    expect(select.get('option[value="41"]').text()).toContain(kilo.proxyOwn)
    expect(select.get('option[value="42"]').attributes('disabled')).toBeDefined()
    expect(select.get('option[value="43"]').attributes('disabled')).toBeUndefined()
    expect(select.get('option[value="44"]').attributes('disabled')).toBeDefined()
  })

  it('checks a failed login proxy without requiring a prepared identity, then changes the route without retrying', async () => {
    overview.prepared = []
    overview.jobs = [{ ...awaitingJob(), id: 6, prepared_id: null, prepared_name: '', job_kind: 'onboard_source', status: 'failed', stage: 'quota_oauth', login_proxy_id: 15, account_id: 132, source_account_id: 131, finished_at: '2026-11-16T00:00:00Z' }]
    const originalGet = client.get.getMockImplementation()!
    client.get.mockImplementation(async (url: string, ...args: unknown[]) => url === '/admin/kilo/proxies'
      ? response({ items: [{ id: 15, name: 'Bad login', status: 'active', owned: true, owner_account_id: 132, owner_deleted: false }, { id: 41, name: 'New login', status: 'active', owned: false }] }) : originalGet(url, ...args))
    client.post.mockImplementation(async (url: string, input: unknown) => {
      if (url === '/admin/kilo/proxies/15/preflight') return response({ proxy_id: 15, reachable: false, checks: [{ target: 'openai', reachable: false, latency_ms: 10, error: 'tls_error' }, { target: 'kilo_api', reachable: false, latency_ms: 11, error: 'tls_error' }, { target: 'kilo_login', reachable: false, latency_ms: 12, error: 'tls_error' }] })
      expect(url).toBe('/admin/kilo/jobs/6/login-proxy')
      expect(input).toEqual({ login_proxy_id: 41 })
      overview.jobs = overview.jobs.map(job => ({ ...job, login_proxy_id: 41 }))
      return response({ status: 'updated' })
    })
    const wrapper = await workspace('/admin/kilo?tab=jobs')
    expect(wrapper.find('[data-testid="kilo-job-delete"]').exists()).toBe(true)
    await wrapper.get('[data-testid="kilo-job-change-proxy"]').trigger('click')
    await flushPromises()
    const dialog = wrapper.get('[role="dialog"][aria-label="Change login proxy for job #6"]')
    await dialog.get('[data-testid="kilo-proxy-check"]').trigger('click')
    await flushPromises()
    expect(dialog.get('[data-testid="kilo-proxy-check-result"]').text()).toContain(kilo.preflight.unavailable)
    expect(dialog.text()).toContain('OpenAI authorization')
    expect(dialog.text()).toContain('Kilo API')
    expect(dialog.text()).toContain('Kilo login')
    expect(client.post.mock.calls).toHaveLength(1)
    await dialog.get('#kilo-job-login-proxy').setValue('41')
    expect(dialog.find('[data-testid="kilo-proxy-check-result"]').exists()).toBe(false)
    await dialog.get('#kilo-job-proxy-form').trigger('submit')
    await flushPromises()
    expect(client.post.mock.calls).toHaveLength(2)
    expect(wrapper.text()).toContain(kilo.jobs.proxySaved)
    expect(wrapper.text()).toContain('Proxy #41')
  })

  it('uses server recovery eligibility rather than subscription error codes and preserves ordinary retry', async () => {
    overview.jobs = [
      { ...awaitingJob(), id: 90, prepared_id: null, job_kind: 'onboard_source', stage: 'subscription_authorization', status: 'failed', error_code: 'BYOK_ENABLE_UNVERIFIED', recovery_actions: [] },
      { ...awaitingJob(), id: 91, status: 'failed', recovery_actions: ['retry'] }
    ]
    client.post.mockResolvedValue(response({ status: 'queued' }))
    const wrapper = await workspace('/admin/kilo?tab=jobs')
    expect(wrapper.find('[data-testid="kilo-job-reconcile-subscription"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="kilo-job-reauthorize-subscription"]').exists()).toBe(false)
    expect(wrapper.findAll('button').filter(item => item.text() === kilo.jobs.retry)).toHaveLength(1)
    await button(wrapper, kilo.jobs.retry).trigger('click')
    await flushPromises()
    expect(client.post.mock.calls).toEqual([['/admin/kilo/jobs/91/retry', {}, { signal: expect.any(AbortSignal) }]])
  })

  it('reconciles only the original job revision after confirmation and prevents duplicate requests', async () => {
    overview.jobs = [{ ...awaitingJob(), prepared_id: null, job_kind: 'onboard_source', stage: 'subscription_authorization', status: 'failed', login_proxy_id: 41, error_code: 'BYOK_ENABLE_UNVERIFIED', recovery_actions: ['reconcile_subscription'] }]
    let complete!: (value: ReturnType<typeof response>) => void
    client.post.mockImplementation(() => new Promise(resolve => { complete = resolve }))
    const wrapper = await workspace('/admin/kilo?tab=jobs')
    expect(wrapper.findAll('button').some(item => item.text() === kilo.jobs.retry)).toBe(false)
    expect(wrapper.find('[data-testid="kilo-job-reauthorize-subscription"]').exists()).toBe(false)
    await wrapper.get('[data-testid="kilo-job-reconcile-subscription"]').trigger('click')
    const dialog = wrapper.get(`[role="dialog"][aria-label="${kilo.jobs.recovery.reconcileTitle}"]`)
    expect(dialog.text()).toContain(kilo.jobs.recovery.reconcileDescription)
    expect(dialog.text()).toContain('Original job #90 · revision 3 · source #101 · fixed login proxy #41')
    expect(client.post).not.toHaveBeenCalled()
    await dialog.get('[data-testid="kilo-confirm-subscription-recovery"]').trigger('click')
    expect(dialog.get('[data-testid="kilo-confirm-subscription-recovery"]').attributes('disabled')).toBeDefined()
    await dialog.get('[data-testid="kilo-confirm-subscription-recovery"]').trigger('click')
    expect(client.post.mock.calls).toEqual([['/admin/kilo/jobs/90/reconcile-subscription', { expected_revision: 3 }, { signal: expect.any(AbortSignal), timeout: 35000 }]])
    overview.jobs = overview.jobs.map(job => ({ ...job, revision: 4, status: 'queued', recovery_actions: [] }))
    complete(response({ status: 'queued' }))
    await flushPromises()
    expect(wrapper.text()).toContain(kilo.jobs.recovery.reconcileQueued)
    expect(wrapper.find(`[role="dialog"][aria-label="${kilo.jobs.recovery.reconcileTitle}"]`).exists()).toBe(false)
  })

  it('requires explicit new-authorization consent before reauthorizing the same original job', async () => {
    overview.jobs = [{ ...awaitingJob(), prepared_id: null, job_kind: 'onboard_source', stage: 'subscription_authorization', status: 'failed', error_code: 'IDENTITY_MISMATCH', login_proxy_id: 41, recovery_actions: ['reconcile_subscription', 'reauthorize_subscription'] }]
    client.post.mockImplementation(async () => {
      overview.jobs = overview.jobs.map(job => ({ ...job, revision: 4, status: 'queued', recovery_actions: [] }))
      return response({ status: 'queued' })
    })
    const wrapper = await workspace('/admin/kilo?tab=jobs')
    await wrapper.get('[data-testid="kilo-job-reauthorize-subscription"]').trigger('click')
    const dialog = wrapper.get(`[role="dialog"][aria-label="${kilo.jobs.recovery.reauthorizeTitle}"]`)
    expect(dialog.text()).toContain(kilo.jobs.recovery.reauthorizeDescription)
    expect(dialog.text()).toContain(kilo.jobs.recovery.authorizationConfirmation)
    expect(dialog.get('[data-testid="kilo-confirm-subscription-recovery"]').attributes('disabled')).toBeDefined()
    await dialog.get('[data-testid="kilo-confirm-subscription-recovery"]').trigger('click')
    expect(client.post).not.toHaveBeenCalled()
    await dialog.get('#kilo-reauthorization-confirmed').setValue(true)
    await dialog.get('[data-testid="kilo-confirm-subscription-recovery"]').trigger('click')
    await flushPromises()
    expect(client.post.mock.calls).toEqual([['/admin/kilo/jobs/90/reauthorize-subscription', { expected_revision: 3 }, { signal: expect.any(AbortSignal), timeout: 35000 }]])
    expect(wrapper.text()).toContain(kilo.jobs.recovery.reauthorizeQueued)
    expect(sessionStorage.length).toBe(0)
  })

  it('invalidates an open authorization confirmation when the server advances its revision', async () => {
    overview.jobs = [{ ...awaitingJob(), prepared_id: null, job_kind: 'onboard_source', stage: 'subscription_authorization', status: 'failed', recovery_actions: ['reauthorize_subscription'] }]
    const wrapper = await workspace('/admin/kilo?tab=jobs')
    await wrapper.get('[data-testid="kilo-job-reauthorize-subscription"]').trigger('click')
    const dialog = wrapper.get(`[role="dialog"][aria-label="${kilo.jobs.recovery.reauthorizeTitle}"]`)
    await dialog.get('#kilo-reauthorization-confirmed').setValue(true)
    overview.jobs = overview.jobs.map(job => ({ ...job, revision: 4 }))
    await button(wrapper, common.common.refresh).trigger('click')
    await flushPromises()
    expect(dialog.text()).toContain(kilo.jobs.recovery.stale)
    expect(dialog.get('[data-testid="kilo-confirm-subscription-recovery"]').attributes('disabled')).toBeDefined()
    await dialog.get('[data-testid="kilo-confirm-subscription-recovery"]').trigger('click')
    expect(client.post).not.toHaveBeenCalled()
  })

  it('keeps the same revision after an unconfirmed recovery and refreshes conflicts before another attempt', async () => {
    overview.jobs = [{ ...awaitingJob(), prepared_id: null, job_kind: 'onboard_source', stage: 'subscription_authorization', status: 'failed', recovery_actions: ['reconcile_subscription'] }]
    client.post.mockRejectedValueOnce(new Error('Acknowledgment lost'))
    const wrapper = await workspace('/admin/kilo?tab=jobs')
    await wrapper.get('[data-testid="kilo-job-reconcile-subscription"]').trigger('click')
    const dialog = wrapper.get(`[role="dialog"][aria-label="${kilo.jobs.recovery.reconcileTitle}"]`)
    await dialog.get('[data-testid="kilo-confirm-subscription-recovery"]').trigger('click')
    await flushPromises()
    expect(dialog.text()).toContain(kilo.jobs.recovery.unconfirmed)
    expect(dialog.get('[data-testid="kilo-confirm-subscription-recovery"]').attributes('disabled')).toBeUndefined()
    client.post.mockImplementationOnce(async () => {
      overview.jobs = overview.jobs.map(job => ({ ...job, revision: 4, recovery_actions: [] }))
      throw { status: 409, message: 'Original revision changed' }
    })
    await dialog.get('[data-testid="kilo-confirm-subscription-recovery"]').trigger('click')
    await flushPromises()
    expect(client.post.mock.calls.map(call => call.slice(0, 2))).toEqual([
      ['/admin/kilo/jobs/90/reconcile-subscription', { expected_revision: 3 }],
      ['/admin/kilo/jobs/90/reconcile-subscription', { expected_revision: 3 }]
    ])
    expect(dialog.text()).toContain('Original revision changed')
    expect(dialog.text()).toContain(kilo.jobs.recovery.stale)
    expect(dialog.get('[data-testid="kilo-confirm-subscription-recovery"]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).not.toContain(kilo.jobs.recovery.reconcileQueued)
  })

  it('confirms deletion for a failed job with no prepared identity and keeps a rejected cleanup visible', async () => {
    overview.prepared = []
    overview.jobs = [{ ...awaitingJob(), id: 6, prepared_id: null, prepared_name: '', job_kind: 'onboard_source', status: 'failed', stage: 'quota_oauth', finished_at: '2026-11-16T00:00:00Z' }]
    client.delete.mockRejectedValueOnce({ status: 409, message: 'This job still has an active lease.' })
    const wrapper = await workspace('/admin/kilo?tab=jobs')
    await wrapper.get('[data-testid="kilo-job-delete"]').trigger('click')
    expect(client.delete).not.toHaveBeenCalled()
    const dialog = wrapper.get(`[role="dialog"][aria-label="${kilo.cleanup.title}"]`)
    await dialog.get('[data-testid="kilo-confirm-delete"]').trigger('click')
    await flushPromises()
    expect(dialog.text()).toContain('This job still has an active lease.')
    expect(wrapper.find('[data-testid="kilo-job-delete"]').exists()).toBe(true)
    client.delete.mockImplementation(async (url: string) => {
      expect(url).toBe('/admin/kilo/jobs/6')
      overview.jobs = []
      return response({ status: 'deleted' })
    })
    await dialog.get('[data-testid="kilo-confirm-delete"]').trigger('click')
    await flushPromises()
    expect(client.delete).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[data-testid="kilo-job-delete"]').exists()).toBe(false)
    expect(wrapper.text()).toContain(kilo.cleanup.jobDeleted)
  })

  it('retains login ownership in the confirmation when deleting a failed prepared identity', async () => {
    overview.prepared = [prepared(11, 'Failed identity', { status: 'disabled', account_id: 501 })]
    overview.jobs = [{ ...awaitingJob(), prepared_id: 11, prepared_name: 'Failed identity', status: 'failed', finished_at: '2026-11-16T00:00:00Z', credential_ready: true }]
    client.delete.mockImplementation(async (url: string) => {
      expect(url).toBe('/admin/kilo/prepared/11')
      overview.prepared = []
      overview.jobs = []
      return response({ status: 'deleted' })
    })
    const wrapper = await workspace()
    await wrapper.get('[data-testid="kilo-prepared-delete"]').trigger('click')
    const dialog = wrapper.get(`[role="dialog"][aria-label="${kilo.cleanup.title}"]`)
    expect(dialog.text()).toContain(kilo.cleanup.retainedIdentity)
    await dialog.get('[data-testid="kilo-confirm-delete"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="kilo-prepared-delete"]').exists()).toBe(false)
    expect(wrapper.text()).toContain(kilo.cleanup.preparedDeleted)
  })

  it('does not offer failure cleanup or proxy replacement for active jobs', async () => {
    overview.jobs = [{ ...awaitingJob(), prepared_id: null, job_kind: 'onboard_source', status: 'running' }]
    const wrapper = await workspace('/admin/kilo?tab=jobs')
    expect(wrapper.find('[data-testid="kilo-job-delete"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="kilo-job-change-proxy"]').exists()).toBe(false)
    expect(button(wrapper, kilo.jobs.cancel).exists()).toBe(true)
  })

  it('imports line and exported account inputs with a key-free preview and no plaintext browser storage', async () => {
    const firstKey = 'fixture-only-import-secret-alpha'
    const secondKey = 'fixture-only-import-secret-beta'
    const example = parseKiloImport(JSON.stringify({ accounts: [{ name: 'Example', credential: { api_key: firstKey } }] }))
    expect(example.issue).toBeUndefined()
    expect(example.rows[0]?.item).toEqual({ name: 'Example', api_key: firstKey })
    const writes = vi.spyOn(Storage.prototype, 'setItem')
    overview.prepared = []
    client.post.mockImplementation(async (url: string) => {
      expect(url).toBe('/admin/kilo/prepared/import')
      return response({ items: [{ line: 1, id: 201, status: 'imported' }, { line: 2, id: 202, status: 'imported' }], imported: 2, duplicates: 0, failed: 0 })
    })
    const wrapper = await workspace()
    await button(wrapper, kilo.import.title).trigger('click')
    const dialog = wrapper.get(`[role="dialog"][aria-label="${kilo.import.title}"]`)
    await dialog.get('#kilo-import-input').setValue(`\nImported Alpha----${firstKey}\n\nImported Beta----${secondKey}`)
    await button(dialog, kilo.import.preview).trigger('click')
    const rows = dialog.findAll('tbody > tr')
    expect(rows.map(row => row.get('td').text())).toEqual(['2', '4'])
    expect(dialog.text()).not.toContain(firstKey)
    expect(dialog.text()).not.toContain(secondKey)
    await button(dialog, 'Import 2 accounts').trigger('click')
    await vi.waitFor(() => expect(client.post).toHaveBeenCalledTimes(1))
    await flushPromises()
    expect(client.post.mock.calls[0]![1]).toMatchObject({ items: [{ name: 'Imported Alpha', api_key: firstKey }, { name: 'Imported Beta', api_key: secondKey }] })
    expect(dialog.text()).toContain('2 imported, 0 duplicates, 0 failed.')
    expect(recoveryKeys('import')).toEqual([])
    for (const [key, value] of writes.mock.calls) {
      expect(`${key}:${value}`).not.toContain(firstKey)
      expect(`${key}:${value}`).not.toContain(secondKey)
    }
    expect(localStorage.length).toBe(0)
    expect(dialog.text()).not.toContain(firstKey)
  })
})
