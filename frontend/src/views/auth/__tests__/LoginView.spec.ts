import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import LoginView from '@/views/auth/LoginView.vue'

const {
  clearAllAffiliateReferralCodesMock,
  getPublicSettingsMock,
  isTotp2FARequiredMock,
  isWeChatWebOAuthEnabledMock,
  loginMock,
  login2FAMock,
  loginWithPasskeyMock,
  pushMock,
  routeState,
  showErrorMock,
  showSuccessMock,
  showWarningMock
} = vi.hoisted(() => ({
  clearAllAffiliateReferralCodesMock: vi.fn(),
  getPublicSettingsMock: vi.fn(),
  isTotp2FARequiredMock: vi.fn(),
  isWeChatWebOAuthEnabledMock: vi.fn(),
  loginMock: vi.fn(),
  login2FAMock: vi.fn(),
  loginWithPasskeyMock: vi.fn(),
  pushMock: vi.fn(),
  routeState: { query: {} as { redirect?: string } },
  showErrorMock: vi.fn(),
  showSuccessMock: vi.fn(),
  showWarningMock: vi.fn()
}))

enableAutoUnmount(afterEach)

const agreementStorageKey = 'sub2api_login_agreement_consent'

const publicSettings = {
  registration_enabled: true,
  turnstile_enabled: false,
  turnstile_site_key: '',
  tencent_captcha_enabled: false,
  tencent_captcha_app_id: '',
  aliyun_captcha_enabled: false,
  aliyun_captcha_scene_id: '',
  aliyun_captcha_prefix: '',
  linuxdo_oauth_enabled: false,
  dingtalk_oauth_enabled: false,
  wechat_oauth_enabled: false,
  backend_mode_enabled: false,
  oidc_oauth_enabled: false,
  oidc_oauth_provider_name: 'OIDC',
  github_oauth_enabled: false,
  google_oauth_enabled: false,
  password_reset_enabled: false,
  passkey_enabled: false,
  login_agreement_enabled: false,
  login_agreement_documents: []
}

vi.mock('vue-router', () => ({
  useRouter: () => ({
    push: pushMock,
    currentRoute: { value: routeState }
  })
}))

vi.mock('vue-i18n', () => ({
  createI18n: () => ({
    global: {
      t: (key: string) => key
    }
  }),
  useI18n: () => ({
    t: (key: string) => key
  })
}))

vi.mock('@/stores', () => ({
  useAuthStore: () => ({
    login: loginMock,
    loginWithPasskey: loginWithPasskeyMock,
    login2FA: login2FAMock
  }),
  useAppStore: () => ({
    showError: showErrorMock,
    showSuccess: showSuccessMock,
    showWarning: showWarningMock
  })
}))

vi.mock('@/api/auth', () => ({
  buildOAuthLoginStartURL: vi.fn(),
  getPublicSettings: (...args: unknown[]) => getPublicSettingsMock(...args),
  isTotp2FARequired: isTotp2FARequiredMock,
  isWeChatWebOAuthEnabled: isWeChatWebOAuthEnabledMock,
  startOAuthLogin: vi.fn()
}))

vi.mock('@/utils/oauthAffiliate', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/utils/oauthAffiliate')>(),
  clearAllAffiliateReferralCodes: clearAllAffiliateReferralCodesMock
}))

function mountLogin() {
  return mount(LoginView, {
    global: {
      stubs: {
        LoginLayout: { template: '<div><slot /><slot name="footer" /></div>' },
        DingTalkOAuthSection: true,
        EmailOAuthButtons: true,
        Icon: true,
        LinuxDoOAuthSection: true,
        LoginAgreementPrompt: true,
        OidcOAuthSection: true,
        RouterLink: { template: '<a><slot /></a>' },
        TotpLoginModal: {
          name: 'TotpLoginModal',
          props: ['tempToken', 'userEmailMasked'],
          emits: ['verify', 'cancel'],
          methods: {
            setVerifying: vi.fn(),
            setError: vi.fn()
          },
          template: '<div />'
        },
        TurnstileWidget: true,
        WechatOAuthSection: true,
        transition: false
      }
    }
  })
}

describe('LoginView authentication controls', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    localStorage.clear()
    sessionStorage.clear()
    routeState.query = {}
    getPublicSettingsMock.mockResolvedValue(publicSettings)
    loginMock.mockResolvedValue({})
    login2FAMock.mockResolvedValue({})
    loginWithPasskeyMock.mockResolvedValue({})
    isTotp2FARequiredMock.mockImplementation(
      (response: { requires_2fa?: boolean }) => response?.requires_2fa === true
    )
    isWeChatWebOAuthEnabledMock.mockImplementation(
      (settings: { wechat_oauth_enabled?: boolean }) => settings.wechat_oauth_enabled === true
    )
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    localStorage.clear()
    sessionStorage.clear()
  })

  it('shows the registration entry when registration is enabled', async () => {
    const wrapper = mountLogin()
    await flushPromises()

    expect(wrapper.text()).toContain('auth.signUp')
  })

  it('hides the registration entry when registration is disabled', async () => {
    getPublicSettingsMock.mockResolvedValueOnce({
      ...publicSettings,
      registration_enabled: false
    })

    const wrapper = mountLogin()
    await flushPromises()

    expect(wrapper.text()).not.toContain('auth.signUp')
  })

  it('disables the form and hides registration until public settings finish loading', async () => {
    let resolveSettings!: (settings: typeof publicSettings) => void
    getPublicSettingsMock.mockReturnValueOnce(new Promise<typeof publicSettings>((resolve) => {
      resolveSettings = resolve
    }))

    const wrapper = mountLogin()

    expect(wrapper.get('#email').attributes('disabled')).toBeDefined()
    expect(wrapper.get('#password').attributes('disabled')).toBeDefined()
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).not.toContain('auth.signUp')
    expect(loginMock).not.toHaveBeenCalled()

    resolveSettings(publicSettings)
    await flushPromises()

    expect(wrapper.get('#email').attributes('disabled')).toBeUndefined()
    expect(wrapper.get('#password').attributes('disabled')).toBeUndefined()
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.text()).toContain('auth.signUp')
  })

  it('hides registration, password reset and OAuth in backend mode without Passkey', async () => {
    getPublicSettingsMock.mockResolvedValueOnce({
      ...publicSettings,
      backend_mode_enabled: true,
      password_reset_enabled: true,
      linuxdo_oauth_enabled: true,
      dingtalk_oauth_enabled: true,
      wechat_oauth_enabled: true,
      oidc_oauth_enabled: true,
      github_oauth_enabled: true,
      google_oauth_enabled: true
    })

    const wrapper = mountLogin()
    await flushPromises()

    expect(wrapper.text()).not.toContain('auth.signUp')
    expect(wrapper.text()).not.toContain('auth.forgotPassword')
    for (const name of [
      'EmailOAuthButtons',
      'LinuxDoOAuthSection',
      'DingTalkOAuthSection',
      'WechatOAuthSection',
      'OidcOAuthSection'
    ]) {
      expect(wrapper.findComponent({ name }).exists()).toBe(false)
    }
    expect(wrapper.get('#email').attributes('disabled')).toBeUndefined()
  })

  it.each(['checkbox', 'modal'] as const)(
    'keeps password, Passkey and OAuth gated until the %s agreement is accepted',
    async (mode) => {
      vi.stubGlobal('PublicKeyCredential', class {})
      getPublicSettingsMock.mockResolvedValueOnce({
        ...publicSettings,
        github_oauth_enabled: true,
        passkey_enabled: true,
        login_agreement_enabled: true,
        login_agreement_mode: mode,
        login_agreement_revision: 'terms-v2',
        login_agreement_documents: [{ id: 'terms', title: 'Terms of service' }]
      })

      const wrapper = mountLogin()
      await flushPromises()

      const agreement = wrapper.findComponent({ name: 'LoginAgreementPrompt' })
      const oauth = wrapper.findComponent({ name: 'EmailOAuthButtons' })
      const passkey = wrapper.get('button.btn-secondary.w-full')
      expect(agreement.props('accepted')).toBe(false)
      expect(agreement.props('visible')).toBe(mode === 'modal')
      expect(wrapper.get('#email').attributes('disabled')).toBeDefined()
      expect(wrapper.get('#password').attributes('disabled')).toBeDefined()
      expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
      expect(passkey.attributes('disabled')).toBeDefined()
      expect(oauth.props('disabled')).toBe(true)

      await wrapper.get('form').trigger('submit')
      expect(loginMock).not.toHaveBeenCalled()
      expect(loginWithPasskeyMock).not.toHaveBeenCalled()
      expect(showWarningMock).toHaveBeenCalledWith('legal.loginAgreementPrompt.loginRequiredWarning')

      agreement.vm.$emit('accept')
      await flushPromises()

      expect(agreement.props('accepted')).toBe(true)
      expect(agreement.props('visible')).toBe(false)
      expect(wrapper.get('#email').attributes('disabled')).toBeUndefined()
      expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeUndefined()
      expect(passkey.attributes('disabled')).toBeUndefined()
      expect(oauth.props('disabled')).toBe(false)
      expect(JSON.parse(localStorage.getItem(agreementStorageKey) || '{}').revision).toBe('terms-v2')

      agreement.vm.$emit('reject')
      await flushPromises()

      expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
      expect(passkey.attributes('disabled')).toBeDefined()
      expect(oauth.props('disabled')).toBe(true)
      expect(localStorage.getItem(agreementStorageKey)).toBeNull()
    }
  )

  it.each([
    ['terms-v2', true],
    ['terms-v1', false]
  ] as const)('only restores consent for the current agreement revision (%s)', async (revision, accepted) => {
    localStorage.setItem(agreementStorageKey, JSON.stringify({ revision }))
    getPublicSettingsMock.mockResolvedValueOnce({
      ...publicSettings,
      login_agreement_enabled: true,
      login_agreement_mode: 'modal',
      login_agreement_revision: 'terms-v2',
      login_agreement_documents: [{ id: 'terms', title: 'Terms of service' }]
    })

    const wrapper = mountLogin()
    await flushPromises()

    const agreement = wrapper.findComponent({ name: 'LoginAgreementPrompt' })
    expect(agreement.props('accepted')).toBe(accepted)
    expect(agreement.props('visible')).toBe(!accepted)
    expect(wrapper.get('button[type="submit"]').attributes('disabled') !== undefined).toBe(!accepted)
  })

  it('updates the password visibility control accessible name and pressed state', async () => {
    const wrapper = mountLogin()
    await flushPromises()

    const toggle = wrapper.get('button[aria-label="auth.loginShowcase.showPassword"]')
    expect(toggle.attributes('type')).toBe('button')
    expect(toggle.attributes('aria-pressed')).toBe('false')
    expect(wrapper.get('#password').attributes('type')).toBe('password')

    await toggle.trigger('click')

    expect(toggle.attributes('aria-label')).toBe('auth.loginShowcase.hidePassword')
    expect(toggle.attributes('aria-pressed')).toBe('true')
    expect(wrapper.get('#password').attributes('type')).toBe('text')

    await toggle.trigger('click')

    expect(toggle.attributes('aria-label')).toBe('auth.loginShowcase.showPassword')
    expect(toggle.attributes('aria-pressed')).toBe('false')
    expect(wrapper.get('#password').attributes('type')).toBe('password')
    expect(loginMock).not.toHaveBeenCalled()
  })

  it('preserves the credential payload, loading state and requested redirect on submit', async () => {
    let resolveLogin!: (response: object) => void
    loginMock.mockReturnValueOnce(new Promise<object>((resolve) => {
      resolveLogin = resolve
    }))
    routeState.query = { redirect: '/keys' }

    const wrapper = mountLogin()
    await flushPromises()
    await wrapper.get('#email').setValue('user@example.com')
    await wrapper.get('#password').setValue('secret-123')
    await wrapper.get('form').trigger('submit')

    expect(loginMock).toHaveBeenCalledOnce()
    expect(loginMock).toHaveBeenCalledWith({
      email: 'user@example.com',
      password: 'secret-123',
      turnstile_token: undefined,
      tencent_captcha_ticket: undefined,
      tencent_captcha_randstr: undefined
    })
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('#email').attributes('disabled')).toBeDefined()
    expect(wrapper.get('#password').attributes('disabled')).toBeDefined()
    expect(pushMock).not.toHaveBeenCalled()
    expect(clearAllAffiliateReferralCodesMock).not.toHaveBeenCalled()

    resolveLogin({})
    await flushPromises()

    expect(clearAllAffiliateReferralCodesMock).toHaveBeenCalledOnce()
    expect(showSuccessMock).toHaveBeenCalledWith('auth.loginSuccess')
    expect(pushMock).toHaveBeenCalledOnce()
    expect(pushMock).toHaveBeenCalledWith('/keys')
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeUndefined()
  })

  it('uses the dashboard when no redirect is supplied', async () => {
    const wrapper = mountLogin()
    await flushPromises()
    await wrapper.get('#email').setValue('user@example.com')
    await wrapper.get('#password').setValue('secret-123')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(pushMock).toHaveBeenCalledOnce()
    expect(pushMock).toHaveBeenCalledWith('/dashboard')
  })

  it('waits for TOTP verification before completing login and preserves its token contract', async () => {
    loginMock.mockResolvedValueOnce({
      requires_2fa: true,
      temp_token: 'pending-login-token',
      user_email_masked: 'u***@example.com'
    })
    const wrapper = mountLogin()
    await flushPromises()
    await wrapper.get('#email').setValue('user@example.com')
    await wrapper.get('#password').setValue('secret-123')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    const totp = wrapper.findComponent({ name: 'TotpLoginModal' })
    expect(totp.props('tempToken')).toBe('pending-login-token')
    expect(totp.props('userEmailMasked')).toBe('u***@example.com')
    expect(pushMock).not.toHaveBeenCalled()
    expect(clearAllAffiliateReferralCodesMock).not.toHaveBeenCalled()

    totp.vm.$emit('verify', '123456')
    await flushPromises()

    expect(login2FAMock).toHaveBeenCalledOnce()
    expect(login2FAMock).toHaveBeenCalledWith('pending-login-token', '123456')
    expect(clearAllAffiliateReferralCodesMock).toHaveBeenCalledOnce()
    expect(pushMock).toHaveBeenCalledOnce()
    expect(pushMock).toHaveBeenCalledWith('/dashboard')
    expect(wrapper.findComponent({ name: 'TotpLoginModal' }).exists()).toBe(false)
  })
})
