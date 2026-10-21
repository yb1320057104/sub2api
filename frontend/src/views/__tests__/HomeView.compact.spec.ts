import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, RouterLinkStub } from '@vue/test-utils'

import HomeView from '../HomeView.vue'

const { appStore, authStore } = vi.hoisted(() => ({
  appStore: {
    cachedPublicSettings: {} as Record<string, unknown>,
    siteName: 'Fallback site',
    siteLogo: '',
    docUrl: '',
    publicSettingsLoaded: true,
    fetchPublicSettings: vi.fn(),
  },
  authStore: {
    isAuthenticated: false,
    isAdmin: false,
    user: null as { email?: string } | null,
    checkAuth: vi.fn(),
  },
}))

vi.mock('@/stores', () => ({
  useAppStore: () => appStore,
  useAuthStore: () => authStore,
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => appStore,
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

function mountHome(settings: Record<string, unknown> = {}) {
  appStore.cachedPublicSettings = {
    site_name: 'Test site',
    site_subtitle: 'Test subtitle',
    ...settings,
  }

  return mount(HomeView, {
    global: {
      stubs: {
        RouterLink: RouterLinkStub,
        LocaleSwitcher: { template: '<div data-testid="locale-switcher" />' },
        Icon: { template: '<span data-testid="icon" />' },
      },
    },
  })
}

function compactDestination(wrapper: ReturnType<typeof mountHome>) {
  return wrapper.get('[data-testid="compact-home"]').findComponent(RouterLinkStub).props('to')
}

function modelPlazaDestination(wrapper: ReturnType<typeof mountHome>) {
  return wrapper
    .findAllComponents(RouterLinkStub)
    .find((link) => link.props('to') === '/model-plaza')
    ?.props('to')
}

describe('HomeView compact mode', () => {
  beforeEach(() => {
    authStore.isAuthenticated = false
    authStore.isAdmin = false
    authStore.user = null
    authStore.checkAuth.mockClear()
    appStore.fetchPublicSettings.mockClear()
    localStorage.clear()
    document.documentElement.classList.remove('dark')
    vi.spyOn(window, 'matchMedia').mockReturnValue({ matches: false } as MediaQueryList)
  })

  it('renders custom HTML ahead of compact mode', () => {
    const wrapper = mountHome({
      compact_home_enabled: true,
      home_content: '<section id="custom-home">Custom home</section>',
    })

    expect(wrapper.get('#custom-home').text()).toBe('Custom home')
    expect(wrapper.find('[data-testid="compact-home"]').exists()).toBe(false)
  })

  it('renders custom URL content ahead of compact mode', () => {
    const wrapper = mountHome({
      compact_home_enabled: true,
      home_content: ' https://example.com/home ',
    })

    expect(wrapper.get('iframe').attributes('src')).toBe('https://example.com/home')
    expect(wrapper.find('[data-testid="compact-home"]').exists()).toBe(false)
  })

  it('treats whitespace-only custom content as empty and selects compact mode', () => {
    const wrapper = mountHome({ compact_home_enabled: true, home_content: ' \n\t ' })

    expect(wrapper.get('[data-testid="compact-home"]').text()).toContain('Test site')
  })

  it.each([undefined, false])('selects the default home when compact mode is %s', (enabled) => {
    const settings = enabled === undefined ? {} : { compact_home_enabled: enabled }
    const wrapper = mountHome(settings)

    expect(wrapper.find('[data-testid="compact-home"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="default-home"]').exists()).toBe(true)
  })

  it('links unauthenticated visitors to login', () => {
    expect(compactDestination(mountHome({ compact_home_enabled: true }))).toBe('/login')
  })

  it('links authenticated users to their dashboard', () => {
    authStore.isAuthenticated = true

    expect(compactDestination(mountHome({ compact_home_enabled: true }))).toBe('/dashboard')
  })

  it('links administrators to the admin dashboard', () => {
    authStore.isAuthenticated = true
    authStore.isAdmin = true

    const wrapper = mountHome({ compact_home_enabled: true })
    expect(compactDestination(wrapper)).toBe('/admin/dashboard')
    expect(authStore.checkAuth).toHaveBeenCalledOnce()
    expect(appStore.fetchPublicSettings).not.toHaveBeenCalled()
  })

  it('shows the model plaza link to anonymous visitors when public access is enabled', () => {
    const wrapper = mountHome({
      compact_home_enabled: true,
      model_plaza_enabled: true,
      model_plaza_require_auth: false,
    })

    expect(modelPlazaDestination(wrapper)).toBe('/model-plaza')
  })

  it('hides the model plaza link from anonymous visitors when sign-in is required', () => {
    const wrapper = mountHome({
      compact_home_enabled: true,
      model_plaza_enabled: true,
      model_plaza_require_auth: true,
    })

    expect(modelPlazaDestination(wrapper)).toBeUndefined()
  })

  it('shows the model plaza link to authenticated visitors when sign-in is required', () => {
    authStore.isAuthenticated = true

    const wrapper = mountHome({
      compact_home_enabled: true,
      model_plaza_enabled: true,
      model_plaza_require_auth: true,
    })

    expect(modelPlazaDestination(wrapper)).toBe('/model-plaza')
  })

  it('shows the model plaza link in the default home header', () => {
    const wrapper = mountHome({
      model_plaza_enabled: true,
      model_plaza_require_auth: false,
    })

    expect(modelPlazaDestination(wrapper)).toBe('/model-plaza')
  })

  it('hides the model plaza link when the feature is disabled', () => {
    const wrapper = mountHome({
      compact_home_enabled: true,
      model_plaza_enabled: false,
      model_plaza_require_auth: false,
    })

    expect(modelPlazaDestination(wrapper)).toBeUndefined()
  })

  it.each([
    { authenticated: false, admin: false, destination: '/login' },
    { authenticated: true, admin: false, destination: '/dashboard' },
    { authenticated: true, admin: true, destination: '/admin/dashboard' },
  ])('preserves the default home primary destination for $destination', ({ authenticated, admin, destination }) => {
    authStore.isAuthenticated = authenticated
    authStore.isAdmin = admin
    const wrapper = mountHome()

    const primary = wrapper.findAllComponents(RouterLinkStub)
      .find((link) => link.attributes('data-testid') === 'home-primary-action')
    expect(primary?.props('to')).toBe(destination)
  })

  it.each([
    { enabled: true, requiresAuth: false, authenticated: false, visible: true },
    { enabled: true, requiresAuth: true, authenticated: false, visible: false },
    { enabled: true, requiresAuth: true, authenticated: true, visible: true },
    { enabled: false, requiresAuth: false, authenticated: false, visible: false },
    { enabled: false, requiresAuth: false, authenticated: true, visible: false },
  ])('keeps every default home model entry behind its access settings: $enabled/$requiresAuth/$authenticated', ({ enabled, requiresAuth, authenticated, visible }) => {
    authStore.isAuthenticated = authenticated
    const wrapper = mountHome({
      model_plaza_enabled: enabled,
      model_plaza_require_auth: requiresAuth,
    })

    const modelLinks = wrapper.findAllComponents(RouterLinkStub)
      .filter((link) => link.props('to') === '/model-plaza')
    expect(modelLinks).toHaveLength(visible ? 2 : 0)
    expect(wrapper.find('[data-testid="home-models-action"]').exists()).toBe(visible)
  })

  it('keeps configured branding and multiline subtitle in the redesigned home', () => {
    const wrapper = mountHome({
      site_name: 'Our team gateway',
      site_logo: '/team-logo.svg',
      site_subtitle: 'First line\nSecond line',
    })

    expect(wrapper.text()).toContain('Our team gateway')
    expect(wrapper.text()).toContain('First line\nSecond line')
    expect(wrapper.findAll('img').some((image) => image.attributes('src') === '/team-logo.svg')).toBe(true)
  })

  it.each(['AI API Gateway Platform', 'Subscription to API Conversion Platform'])('localizes the built-in default subtitle: %s', (siteSubtitle) => {
    const wrapper = mountHome({ site_subtitle: siteSubtitle })

    expect(wrapper.get('.home-description').text()).toBe('home.redesign.description')
  })

  it.each(['', 'javascript:alert(1)'])('does not expose an absent or unsafe documentation link: %s', (docUrl) => {
    const wrapper = mountHome({ doc_url: docUrl })

    expect(wrapper.findAll('a').some((link) => link.attributes('target') === '_blank')).toBe(false)
  })

  it('keeps configured documentation links safe when opening a new tab', () => {
    const wrapper = mountHome({ doc_url: 'https://docs.example.com/start' })
    const links = wrapper.findAll('a').filter((link) => link.attributes('href') === 'https://docs.example.com/start')

    expect(links).toHaveLength(2)
    for (const link of links) {
      expect(link.attributes('target')).toBe('_blank')
      expect(link.attributes('rel')).toBe('noopener noreferrer')
    }
  })

  it('switches and persists the theme from the redesigned home', async () => {
    const wrapper = mountHome()

    await wrapper.get('button[title="home.switchToDark"]').trigger('click')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    expect(localStorage.getItem('theme')).toBe('dark')
    await wrapper.get('button[title="home.switchToLight"]').trigger('click')
    expect(document.documentElement.classList.contains('dark')).toBe(false)
    expect(localStorage.getItem('theme')).toBe('light')
  })
})
