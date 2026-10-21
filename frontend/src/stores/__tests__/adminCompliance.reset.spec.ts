import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { defineComponent, reactive } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import type { AdminComplianceStatus } from '@/api/admin/compliance'
import AdminComplianceDialog from '@/components/admin/AdminComplianceDialog.vue'
import LegalDocumentView from '@/views/public/LegalDocumentView.vue'
import { useAdminComplianceStore } from '../adminCompliance'

const api = vi.hoisted(() => ({ getStatus: vi.fn(), accept: vi.fn() }))
vi.mock('@/api/admin/compliance', () => ({ default: api }))
vi.mock('@/i18n', () => ({ getLocale: () => 'en' }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/stores', () => ({
  useAdminComplianceStore: () => useAdminComplianceStore(),
  useAuthStore: () => adminAuthState,
  useAppStore: () => appState
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => appState }))

const adminAuthState = reactive({
  isAuthenticated: true,
  isAdmin: true,
  logout: vi.fn()
})
const appState = {
  cachedPublicSettings: { site_name: 'DP API', site_logo: '', login_agreement_documents: [] },
  fetchPublicSettings: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn()
}

enableAutoUnmount(afterEach)

function status(required: boolean, version = 'current'): AdminComplianceStatus {
  return {
    required, version, document_path_zh: '', document_path_en: '',
    document_url_zh: '', document_url_en: '', ack_phrase_zh: '测试', ack_phrase_en: 'test'
  }
}

function deferred() {
  let resolve!: (value: AdminComplianceStatus) => void
  let reject!: (error: Error) => void
  const promise = new Promise<AdminComplianceStatus>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.resetAllMocks()
})

it('uses the DP API compliance revision and local document without accepting for the admin', () => {
  const store = useAdminComplianceStore()
  store.requireAcknowledgement()

  expect(store.status?.version).toBe('v2026.10.21')
  expect(store.status?.document_url_zh).toBe('/legal/admin-compliance')
  expect(store.status?.document_url_en).toBe('/legal/admin-compliance')
  expect(store.status?.ack_phrase_zh).toBe('我已阅读、理解并同意 DP API 部署与运营合规承诺')
  expect(store.expectedPhrase).toBe('I have read, understood, and agree to the DP API Deployment and Operation Compliance Commitment')
  expect(store.shouldShow).toBe(true)
  expect(api.accept).not.toHaveBeenCalled()
})

describe.each(['fetch', 'accept'] as const)('compliance %s across logout', (action) => {
  const mock = () => action === 'fetch' ? api.getStatus : api.accept
  const run = (store: ReturnType<typeof useAdminComplianceStore>) =>
    action === 'fetch' ? store.fetchStatus() : store.accept('test')
  const busy = (store: ReturnType<typeof useAdminComplianceStore>) =>
    action === 'fetch' ? store.loading : store.submitting

  it('does not repopulate cleared state when an old request succeeds', async () => {
    const pending = deferred()
    mock().mockReturnValueOnce(pending.promise)
    const store = useAdminComplianceStore()
    const request = run(store)
    store.reset()
    const response = status(true, 'old')
    pending.resolve(response)
    expect(await request).toEqual(response)
    expect(store.status).toBeNull()
    expect(store.initialized).toBe(false)
    expect(store.shouldShow).toBe(false)
    expect(busy(store)).toBe(false)
  })

  it.each(['resolve', 'reject'] as const)('keeps a new session pending when the old request %s', async (outcome) => {
    const old = deferred()
    const current = deferred()
    mock().mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
    const store = useAdminComplianceStore()
    const first = run(store).catch(error => error)
    store.reset()
    const second = run(store)
    if (outcome === 'resolve') old.resolve(status(false, 'old'))
    else old.reject(new Error('old failure'))
    await first
    expect(busy(store)).toBe(true)
    expect(store.status).toBeNull()
    current.resolve(status(true))
    await second
    expect(busy(store)).toBe(false)
    expect(store.status).toEqual(status(true))
    expect(store.shouldShow).toBe(true)
  })
})

it('still updates compliance status during a normal fetch and acceptance', async () => {
  api.getStatus.mockResolvedValue(status(true))
  api.accept.mockResolvedValue(status(false))
  const store = useAdminComplianceStore()
  await store.fetchStatus()
  expect(store.initialized).toBe(true)
  expect(store.shouldShow).toBe(true)
  await store.accept('test')
  expect(api.accept).toHaveBeenCalledWith({ phrase: 'test', language: 'en' })
  expect(store.status).toEqual(status(false))
  expect(store.shouldShow).toBe(false)
})

describe('admin compliance document reader', () => {
  beforeEach(() => {
    adminAuthState.isAuthenticated = true
    adminAuthState.isAdmin = true
    appState.fetchPublicSettings.mockResolvedValue(appState.cachedPublicSettings)
  })

  async function mountRoute(path: string) {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        {
          path: '/legal/:documentId',
          name: 'LegalDocument',
          component: LegalDocumentView
        },
        {
          path: '/admin/dashboard',
          name: 'AdminDashboard',
          component: { template: '<main>Admin dashboard</main>' }
        }
      ]
    })
    await router.push(path)
    await router.isReady()
    const wrapper = mount(defineComponent({
      components: { AdminComplianceDialog },
      template: '<RouterView /><AdminComplianceDialog />'
    }), {
      global: {
        plugins: [router],
        stubs: { Icon: true, Input: true, RouterLink: true, teleport: true, transition: false }
      }
    })
    await flushPromises()
    return { wrapper, router }
  }

  function requireCurrentAgreement() {
    const store = useAdminComplianceStore()
    store.status = {
      ...status(true, 'v2026.10.21'),
      acknowledgement: {
        version: 'v2026.06.10',
        document_zh: 'docs/legal/admin-compliance.zh.md',
        document_en: 'docs/legal/admin-compliance.en.md',
        admin_user_id: 7,
        accepted_at: '2026-06-11T00:00:00Z'
      }
    }
    store.requireAcknowledgement()
    return store
  }

  it.each([
    '/legal/admin-compliance',
    '/legal/admin-compliance?source=dialog#scope'
  ])('keeps the full agreement readable without accepting or clearing the pending state (%s)', async (path) => {
    const store = requireCurrentAgreement()
    const previousState = JSON.stringify(store.$state)
    const accept = vi.spyOn(store, 'accept')
    const reset = vi.spyOn(store, 'reset')

    const { wrapper } = await mountRoute(path)

    expect(wrapper.get('article').text()).toContain('DP API Deployment and Operation Compliance Commitment')
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
    expect(document.body.classList.contains('modal-open')).toBe(false)
    expect(store.required).toBe(true)
    expect(store.shouldShow).toBe(true)
    expect(JSON.stringify(store.$state)).toBe(previousState)
    expect(accept).not.toHaveBeenCalled()
    expect(reset).not.toHaveBeenCalled()
    expect(api.accept).not.toHaveBeenCalled()
  })

  it('shows the pending acknowledgement again after returning from the document to the dashboard', async () => {
    const store = requireCurrentAgreement()
    const previousState = JSON.stringify(store.$state)
    const accept = vi.spyOn(store, 'accept')
    const reset = vi.spyOn(store, 'reset')
    const { wrapper, router } = await mountRoute('/admin/dashboard')

    expect(wrapper.find('[role="dialog"]').exists()).toBe(true)
    expect(document.body.classList.contains('modal-open')).toBe(true)
    await router.push('/legal/admin-compliance')
    await flushPromises()
    expect(wrapper.get('article').text()).toContain('DP API Deployment and Operation Compliance Commitment')
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
    expect(document.body.classList.contains('modal-open')).toBe(false)

    await router.push('/admin/dashboard')
    await flushPromises()

    expect(wrapper.find('[role="dialog"]').exists()).toBe(true)
    expect(document.body.classList.contains('modal-open')).toBe(true)
    expect(JSON.stringify(store.$state)).toBe(previousState)
    expect(accept).not.toHaveBeenCalled()
    expect(reset).not.toHaveBeenCalled()
    expect(api.accept).not.toHaveBeenCalled()
  })

  it.each([
    '/legal/privacy',
    '/legal/admin-compliance-extra',
    '/legal/admin-compliance/',
    '/admin/dashboard?next=/legal/admin-compliance'
  ])('does not suppress acknowledgement for a different route (%s)', async (path) => {
    requireCurrentAgreement()

    const { wrapper } = await mountRoute(path)

    expect(wrapper.find('[role="dialog"]').exists()).toBe(true)
    expect(api.accept).not.toHaveBeenCalled()
  })

  it.each([
    { authenticated: false, admin: true, required: true, visible: false },
    { authenticated: true, admin: false, required: true, visible: false },
    { authenticated: true, admin: true, required: false, visible: false },
    { authenticated: true, admin: true, required: true, visible: true }
  ])('preserves the original authentication and acknowledgement visibility conditions (%j)', async (scenario) => {
    adminAuthState.isAuthenticated = scenario.authenticated
    adminAuthState.isAdmin = scenario.admin
    const store = useAdminComplianceStore()
    store.status = status(scenario.required)

    const { wrapper } = await mountRoute('/admin/dashboard')

    expect(wrapper.find('[role="dialog"]').exists()).toBe(scenario.visible)
    expect(api.accept).not.toHaveBeenCalled()
  })
})
