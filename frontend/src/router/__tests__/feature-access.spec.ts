import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { SMART_OPERATION_TOOLS, SMART_OPERATIONS_ROUTE_NAME } from '@/utils/smartOperations'
import type { RouteRecordRaw } from 'vue-router'

type NavigationGuard = (
  to: Record<string, any>,
  from: Record<string, any>,
  next: ReturnType<typeof vi.fn>
) => Promise<void>

const routerHarness = vi.hoisted(() => ({
  guard: null as NavigationGuard | null,
  routes: [] as Array<{ path: string; name?: string; meta?: Record<string, unknown>; redirect?: unknown }>,
}))

const authStore = vi.hoisted(() => ({
  checkAuth: vi.fn(),
  isAuthenticated: true,
  isAdmin: false,
  isObserver: false,
  canManageAccounts: false,
  isSimpleMode: false,
  hasPendingAuthSession: false,
}))

const appStore = vi.hoisted(() => ({
  siteName: 'Sub2API',
  backendModeEnabled: false,
  publicSettingsLoaded: false,
  cachedPublicSettings: null as null | {
    payment_enabled?: boolean
    risk_control_enabled?: boolean
    subscription_enabled?: boolean
    support_ticket_enabled?: boolean
    custom_menu_items?: []
  },
  fetchPublicSettings: vi.fn(),
}))

const adminSettings = vi.hoisted(() => ({ customMenuItems: [], requestCaptureEnabled: false, fetch: vi.fn() }))

vi.mock('vue-router', () => ({
  createWebHistory: vi.fn(() => ({})),
  createRouter: vi.fn((options: { routes: Array<{ path: string; meta?: Record<string, unknown>; redirect?: unknown }> }) => {
    routerHarness.routes = options.routes
    return {
      beforeEach: vi.fn((guard: NavigationGuard) => {
        routerHarness.guard = guard
      }),
      afterEach: vi.fn(),
      onError: vi.fn(),
    }
  }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => authStore,
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => appStore,
}))

vi.mock('@/stores/adminSettings', () => ({
  useAdminSettingsStore: () => adminSettings,
}))

vi.mock('@/stores/adminCompliance', () => ({
  useAdminComplianceStore: () => ({
    initialized: true,
    fetchStatus: vi.fn(),
    requireAcknowledgement: vi.fn(),
  }),
}))

vi.mock('@/composables/useNavigationLoading', () => ({
  useNavigationLoadingState: () => ({
    startNavigation: vi.fn(),
    endNavigation: vi.fn(),
    isLoading: { value: false },
  }),
}))

vi.mock('@/composables/useRoutePrefetch', () => ({
  useRoutePrefetch: () => ({
    triggerPrefetch: vi.fn(),
    cancelPendingPrefetch: vi.fn(),
    resetPrefetchState: vi.fn(),
  }),
}))

function createDeferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise
  })
  return { promise, resolve }
}

function runGuard(meta: Record<string, unknown>, path: string, query: Record<string, unknown> = {}, name: unknown = routerHarness.routes.find(route => route.path === path)?.name ?? 'FeatureRoute') {
  if (!routerHarness.guard) {
    throw new Error('router guard was not registered')
  }

  const next = vi.fn()
  const navigation = routerHarness.guard(
    {
      path,
      fullPath: path,
      name,
      params: {},
      query,
      hash: '',
      meta: { requiresAuth: true, ...meta },
    },
    {},
    next
  )
  return { navigation, next }
}

describe('feature route guard', () => {
  beforeAll(async () => {
    await import('@/router')
  })

  beforeEach(() => {
    authStore.isAuthenticated = true
    authStore.isAdmin = false
    authStore.isObserver = false
    appStore.backendModeEnabled = false
    authStore.isSimpleMode = false
    appStore.publicSettingsLoaded = false
    appStore.cachedPublicSettings = null
    appStore.fetchPublicSettings.mockReset()
  })

  it('allows observers own usage in backend mode without opening other user or admin pages', async () => {
    appStore.backendModeEnabled = true
    authStore.isObserver = true
    for (const simple of [false, true]) {
      authStore.isSimpleMode = simple
      const own = runGuard({}, '/usage')
      await own.navigation
      expect(own.next).toHaveBeenCalledWith()
    }
    const admin = runGuard({ requiresAdmin: true }, '/admin/usage')
    await admin.navigation
    expect(admin.next).toHaveBeenCalledWith('/dashboard')
    const other = runGuard({}, '/keys')
    await other.navigation
    expect(other.next).toHaveBeenCalledWith('/login')
    authStore.isObserver = false
    const user = runGuard({}, '/usage')
    await user.navigation
    expect(user.next).toHaveBeenCalledWith('/login')
  })

  it('waits for the first public-settings request before deciding payment access', async () => {
    const deferred = createDeferred<{ payment_enabled: boolean }>()
    appStore.fetchPublicSettings.mockImplementation(async () => {
      const settings = await deferred.promise
      appStore.cachedPublicSettings = settings
      appStore.publicSettingsLoaded = true
      return settings
    })

    const { navigation, next } = runGuard({ requiresPayment: true }, '/purchase')

    await vi.waitFor(() => expect(appStore.fetchPublicSettings).toHaveBeenCalledTimes(1))
    expect(next).not.toHaveBeenCalled()

    deferred.resolve({ payment_enabled: true })
    await navigation
    expect(next).toHaveBeenCalledOnce()
    expect(next).toHaveBeenCalledWith()
  })

  it.each([
    ['payment', { requiresPayment: true }, '/purchase'],
    ['risk control', { requiresRiskControl: true }, '/admin/risk-control'],
    ['subscription', { requiresSubscription: true }, '/subscriptions'],
  ])('does not treat a failed %s settings load as explicitly disabled', async (_name, meta, path) => {
    authStore.isAdmin = meta.requiresRiskControl === true
    appStore.fetchPublicSettings.mockResolvedValue(null)

    const { navigation, next } = runGuard(meta, path)
    await navigation

    expect(appStore.publicSettingsLoaded).toBe(false)
    expect(next).toHaveBeenCalledOnce()
    expect(next).toHaveBeenCalledWith()
  })

  it.each([
    ['payment', { requiresPayment: true }, { payment_enabled: false }, '/dashboard'],
    [
      'risk control',
      { requiresRiskControl: true },
      { risk_control_enabled: false },
      '/admin/settings',
    ],
    ['subscription', { requiresSubscription: true }, { subscription_enabled: false }, '/dashboard'],
  ])('redirects when loaded settings explicitly disable %s', async (_name, meta, settings, target) => {
    authStore.isAdmin = meta.requiresRiskControl === true
    appStore.cachedPublicSettings = settings
    appStore.publicSettingsLoaded = true

    const { navigation, next } = runGuard(meta, '/feature')
    await navigation

    expect(appStore.fetchPublicSettings).not.toHaveBeenCalled()
    expect(next).toHaveBeenCalledOnce()
    expect(next).toHaveBeenCalledWith(target)
  })
})

describe('unified smart operations access and bookmarks', () => {
  beforeEach(() => {
    authStore.isAuthenticated = true
    authStore.isAdmin = true
    authStore.isObserver = false
    authStore.isSimpleMode = false
    appStore.backendModeEnabled = false
    adminSettings.fetch.mockReset().mockResolvedValue(undefined)
    adminSettings.requestCaptureEnabled = false
  })

  it.each(SMART_OPERATION_TOOLS)('redirects the actual $path route with filters and hash intact', tool => {
    const route = routerHarness.routes.find(item => item.path === tool.path)!
    expect(route.meta?.requiresAdmin).toBe(true)
    const redirect = route.redirect as (route: Record<string, unknown>) => Record<string, any>
    const target = redirect({ path: tool.path, query: { search: 'account' }, hash: '#saved' })
    expect(target.path).toBe('/admin/smart-ops')
    expect(target.query).toMatchObject({ tool: tool.id, search: 'account' })
    expect(target.hash).toBe('#saved')
  })

  it('keeps the canonical workbench restricted to admins', async () => {
    const route = routerHarness.routes.find(item => item.path === '/admin/smart-ops')!
    expect(route.meta).toMatchObject({ requiresAuth: true, requiresAdmin: true })
    authStore.isAdmin = false
    const { navigation, next } = runGuard(route.meta!, route.path, { tool: 'radar' })
    await navigation
    expect(next).toHaveBeenCalledWith('/dashboard')
  })

  it.each([false, true])('keeps the capture feature gate after redirect (enabled=%s)', async enabled => {
    adminSettings.requestCaptureEnabled = enabled
    const route = routerHarness.routes.find(item => item.path === '/admin/smart-ops')!
    const { navigation, next } = runGuard(route.meta!, route.path, { tool: 'captures' })
    await navigation
    expect(adminSettings.fetch).toHaveBeenCalledWith(true)
    if (enabled) expect(next).toHaveBeenCalledWith()
    else expect(next).toHaveBeenCalledWith('/admin/settings')
  })

  it.each(['/admin/smart-ops/', '/ADMIN/SMART-OPS', '/ADMIN/SMART-OPS/'])('gates captures through the actual matched route for %s', async path => {
    const actualRouter = await vi.importActual<typeof import('vue-router')>('vue-router')
    const matcher = actualRouter.createRouter({ history: actualRouter.createMemoryHistory(), routes: routerHarness.routes as RouteRecordRaw[] })
    const resolved = matcher.resolve({ path, query: { tool: 'captures' } })
    expect(resolved.name).toBe(SMART_OPERATIONS_ROUTE_NAME)
    expect(resolved.path).toBe(path)
    const blocked = runGuard(resolved.meta, resolved.path, resolved.query, resolved.name)
    await blocked.navigation
    expect(adminSettings.fetch).toHaveBeenCalledWith(true)
    expect(blocked.next).toHaveBeenCalledWith('/admin/settings')
    adminSettings.requestCaptureEnabled = true
    const enabled = runGuard(resolved.meta, resolved.path, resolved.query, resolved.name)
    await enabled.navigation
    expect(enabled.next).toHaveBeenCalledWith()
    authStore.isAdmin = false
    const denied = runGuard(resolved.meta, resolved.path, resolved.query, resolved.name)
    await denied.navigation
    expect(denied.next).toHaveBeenCalledWith('/dashboard')
  })

  it.each([
    ['/admin/token-guard-v2/', 'history', 'credentials', 'history'],
    ['/ADMIN/ACCOUNT-AUTHORIZATION/', 'history', 'credentials', 'history'],
    ['/ADMIN/RADAR/', 'authorization', 'credentials', 'inspection'],
    ['/admin/radar/', 'emails', 'notifications', undefined],
    ['/ADMIN/REQUEST-CAPTURES/', 'details', 'captures', 'details'],
  ])('maps matched legacy variant %s to its original tool', async (path, tab, tool, expectedTab) => {
    const actualRouter = await vi.importActual<typeof import('vue-router')>('vue-router')
    const matcher = actualRouter.createRouter({ history: actualRouter.createMemoryHistory(), routes: routerHarness.routes as RouteRecordRaw[] })
    const resolved = matcher.resolve({ path, query: { tab, search: 'account' }, hash: '#saved' })
    expect(resolved.matched).toHaveLength(1)
    const redirect = resolved.matched[0]!.redirect as (to: unknown) => Record<string, any>
    const target = redirect(resolved)
    expect(target.path).toBe('/admin/smart-ops')
    expect(target.query).toMatchObject({ tool, search: 'account' })
    expect(target.query.tab).toBe(expectedTab)
    expect(target.hash).toBe('#saved')
    if (tool === 'notifications') expect(target.query.notification).toBe('overview')
  })

  it('normalizes the default tool and old radar tabs before mounting a page', async () => {
    const route = routerHarness.routes.find(item => item.path === '/admin/smart-ops')!
    const initial = runGuard(route.meta!, route.path)
    await initial.navigation
    expect(initial.next).toHaveBeenCalledWith({ path: route.path, query: { tool: 'radar' }, hash: '', replace: true })
    const oldTab = runGuard(route.meta!, route.path, { tool: 'radar', tab: 'emails' })
    await oldTab.navigation
    expect(oldTab.next).toHaveBeenCalledWith({ path: route.path, query: { tool: 'notifications', notification: 'overview' }, hash: '', replace: true })
  })
})

describe('PKSecurityPolicyRanking route access', () => {
  beforeEach(() => {
    appStore.backendModeEnabled = false
    authStore.isSimpleMode = false
  })

  it.each([false, true])('opens the ranking directly without redirecting to audit settings (admin=%s)', async (admin) => {
    authStore.isAuthenticated = true
    authStore.isAdmin = admin
    const route = routerHarness.routes.find(item => item.path === '/security-policy-ranking')
    expect(route?.meta).toMatchObject({ requiresAuth: true, requiresAdmin: false, titleKey: 'securityPolicyRanking.title' })
    const { navigation, next } = runGuard(route!.meta!, route!.path)
    await navigation
    expect(next).toHaveBeenCalledOnce()
    expect(next).toHaveBeenCalledWith()
  })

  it.each([
    ['/admin/risk-control', 'content'],
    ['/admin/prompt-audit', 'prompt'],
  ])('opens the matching audit menu for the legacy URL %s', (path, section) => {
    const route = routerHarness.routes.find(item => item.path === path)
    const target = typeof route?.redirect === 'function' ? route.redirect({ path, query: {}, hash: '' }) : route?.redirect
    expect(target).toMatchObject({ path: '/admin/audit-center', query: { section } })
  })

  it.each([false, true])('keeps the ranking accessible with risk control off and simple mode on (admin=%s)', async (admin) => {
    authStore.isAuthenticated = true
    authStore.isAdmin = admin
    authStore.isSimpleMode = true
    appStore.publicSettingsLoaded = true
    appStore.cachedPublicSettings = { risk_control_enabled: false }
    const route = routerHarness.routes.find(item => item.path === '/security-policy-ranking')!
    const { navigation, next } = runGuard(route.meta!, route.path)
    await navigation
    expect(next).toHaveBeenCalledOnce()
    expect(next).toHaveBeenCalledWith()
  })

  it('keeps audit diagnostics accessible to admins while global risk control is off', async () => {
    authStore.isAuthenticated = true
    authStore.isAdmin = true
    appStore.publicSettingsLoaded = true
    appStore.cachedPublicSettings = { risk_control_enabled: false }
    const route = routerHarness.routes.find(item => item.path === '/admin/audit-center')!
    expect(route.meta?.requiresAdmin).toBe(true)
    const { navigation, next } = runGuard(route.meta!, route.path)
    await navigation
    expect(next).toHaveBeenCalledWith()
  })

  it('does not expose the unified configuration to ordinary users', async () => {
    authStore.isAuthenticated = true
    authStore.isAdmin = false
    const route = routerHarness.routes.find(item => item.path === '/admin/audit-center')!
    const { navigation, next } = runGuard(route.meta!, route.path)
    await navigation
    expect(next).toHaveBeenCalledWith('/dashboard')
  })

  it('requires login before showing the ranking', async () => {
    authStore.isAuthenticated = false
    authStore.isAdmin = false
    const route = routerHarness.routes.find(item => item.path === '/security-policy-ranking')
    const { navigation, next } = runGuard(route!.meta!, route!.path)
    await navigation
    expect(next).toHaveBeenCalledWith({ path: '/login', query: { redirect: '/security-policy-ranking' } })
    authStore.isAuthenticated = true
  })
})

describe('subscription route guard (opt-out flag)', () => {
  beforeEach(() => {
    authStore.isAdmin = false
    authStore.isSimpleMode = false
    appStore.publicSettingsLoaded = true
    appStore.fetchPublicSettings.mockReset()
  })

  it.each([
    ['missing key', {}],
    ['explicit true', { subscription_enabled: true }],
  ])('lets /subscriptions through when the flag is %s', async (_name, settings) => {
    appStore.cachedPublicSettings = settings

    const { navigation, next } = runGuard({ requiresSubscription: true }, '/subscriptions')
    await navigation

    expect(next).toHaveBeenCalledOnce()
    expect(next).toHaveBeenCalledWith()
  })

  it('sends admins to the admin dashboard when subscriptions are disabled', async () => {
    authStore.isAdmin = true
    appStore.cachedPublicSettings = { subscription_enabled: false }

    const { navigation, next } = runGuard({ requiresSubscription: true }, '/subscriptions')
    await navigation

    expect(next).toHaveBeenCalledWith('/admin/dashboard')
  })
})

describe('support ticket route guard (opt-in flag)', () => {
  beforeEach(() => {
    authStore.isAdmin = false
    authStore.isSimpleMode = false
    appStore.publicSettingsLoaded = true
    appStore.fetchPublicSettings.mockReset()
  })

  it.each([
    ['missing', {}, false, '/dashboard'],
    ['off', { support_ticket_enabled: false }, false, '/dashboard'],
    ['off for an admin', { support_ticket_enabled: false }, true, '/admin/settings'],
  ])('redirects when the switch is %s', async (_name, settings, admin, target) => {
    authStore.isAdmin = admin
    appStore.cachedPublicSettings = settings
    const { navigation, next } = runGuard({ requiresSupportTickets: true, requiresAdmin: admin }, '/support-tickets')
    await navigation
    expect(next).toHaveBeenCalledWith(target)
  })

  it('opens the pages when the switch is on', async () => {
    appStore.cachedPublicSettings = { support_ticket_enabled: true }
    const { navigation, next } = runGuard({ requiresSupportTickets: true }, '/support-tickets/7')
    await navigation
    expect(next).toHaveBeenCalledWith()
  })

  it('loads public settings before deciding', async () => {
    appStore.publicSettingsLoaded = false
    appStore.cachedPublicSettings = null
    appStore.fetchPublicSettings.mockImplementation(async () => {
      appStore.cachedPublicSettings = { support_ticket_enabled: true }
      appStore.publicSettingsLoaded = true
    })
    const { navigation, next } = runGuard({ requiresSupportTickets: true }, '/support-tickets')
    await navigation
    expect(appStore.fetchPublicSettings).toHaveBeenCalledTimes(1)
    expect(next).toHaveBeenCalledWith()
  })
})
