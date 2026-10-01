import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import PaymentView from '../PaymentView.vue'
import { PAYMENT_RECOVERY_STORAGE_KEY } from '@/components/payment/paymentFlow'
import { formatPaymentAmount } from '@/components/payment/currency'
import AmountInput from '@/components/payment/AmountInput.vue'
import SubscriptionPlanCard from '@/components/payment/SubscriptionPlanCard.vue'
import en from '@/i18n/locales/en'
import zh from '@/i18n/locales/zh'
import type { CheckoutInfoResponse, MethodLimit, SubscriptionPlan } from '@/types/payment'
import BinanceRechargeView from '../BinanceRechargeView.vue'
import BinanceOrderDetails from '@/components/payment/BinanceOrderDetails.vue'
import AdminBinanceRechargeView from '@/views/admin/orders/AdminBinanceRechargeView.vue'
import BinanceLiveBills from '@/components/payment/BinanceLiveBills.vue'
import BinanceBillFields from '@/components/payment/BinanceBillFields.vue'
import type { BinanceRechargeOrder } from '@/types/binanceRecharge'

const binanceHarness = vi.hoisted(() => ({
  isAdmin: false,
  createOrder: vi.fn(),
  getOrders: vi.fn(),
  getOrder: vi.fn(),
  refreshOrder: vi.fn(),
  getStats: vi.fn(),
  fetchConfig: vi.fn(),
  copyToClipboard: vi.fn(),
  showSuccess: vi.fn(),
  getAccounts: vi.fn(),
  updateAccount: vi.fn(),
  getBills: vi.fn(),
}))

vi.mock('@/api/binanceRecharge', () => ({
  binanceRechargeAPI: {
    createOrder: binanceHarness.createOrder,
    getOrders: binanceHarness.getOrders,
    getOrder: binanceHarness.getOrder,
    refreshOrder: binanceHarness.refreshOrder,
    getStats: binanceHarness.getStats,
  },
  adminBinanceRechargeAPI: { getAccounts: binanceHarness.getAccounts, updateAccount: binanceHarness.updateAccount, getBills: binanceHarness.getBills },
}))

vi.mock('@/stores/binanceRecharge', () => ({
  useBinanceRechargeStore: () => ({
    config: {
      enabled: true, min_amount: 1, max_amount: 1000000, decimal_places: 5, poll_interval_seconds: 30,
      accounts: [{ id: 7, name: 'Receiving account', recipient_uid: '123456', order_ttl_minutes: 15 }],
    },
    fetchConfig: binanceHarness.fetchConfig,
  }),
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError, showSuccess: binanceHarness.showSuccess }),
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard: binanceHarness.copyToClipboard }),
}))

const routeState = vi.hoisted(() => ({
  path: '/purchase',
  query: {} as Record<string, unknown>,
}))

const routerReplace = vi.hoisted(() => vi.fn())
const routerPush = vi.hoisted(() => vi.fn())
const routerResolve = vi.hoisted(() => vi.fn(() => ({ href: '/payment/stripe?mock=1' })))
const createOrder = vi.hoisted(() => vi.fn())
const refreshUser = vi.hoisted(() => vi.fn())
const fetchActiveSubscriptions = vi.hoisted(() => vi.fn().mockResolvedValue(undefined))
const showError = vi.hoisted(() => vi.fn())
const showInfo = vi.hoisted(() => vi.fn())
const showWarning = vi.hoisted(() => vi.fn())
const getCheckoutInfo = vi.hoisted(() => vi.fn())
const bridgeInvoke = vi.hoisted(() => vi.fn())
const translate = vi.hoisted(() => vi.fn((key: string) => key))
// Public settings live in a reactive holder so tests can flip feature flags after mount
// and exercise the watchers that react to them.
const appStoreState = vi.hoisted(() => ({
  setPublicSettings: (_value: Record<string, unknown> | undefined) => {},
}))

vi.mock('vue-router', async () => {
  const actual = await vi.importActual<typeof import('vue-router')>('vue-router')
  return {
    ...actual,
    useRoute: () => routeState,
    useRouter: () => ({
      replace: routerReplace,
      push: routerPush,
      resolve: routerResolve,
    }),
  }
})

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: translate,
    }),
  }
})

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    get isAdmin() { return binanceHarness.isAdmin },
    user: {
      username: 'demo-user',
      balance: 0,
    },
    refreshUser,
  }),
}))

vi.mock('@/stores/payment', () => ({
  usePaymentStore: () => ({
    createOrder,
  }),
}))

vi.mock('@/stores/subscriptions', () => ({
  useSubscriptionStore: () => ({
    activeSubscriptions: [],
    fetchActiveSubscriptions,
  }),
}))

vi.mock('@/stores', async () => {
  const { reactive } = await import('vue')
  const state = reactive({ cachedPublicSettings: undefined as Record<string, unknown> | undefined })
  appStoreState.setPublicSettings = (value) => {
    state.cachedPublicSettings = value
  }
  return {
    useAppStore: () => ({
      showError,
      showInfo,
      showWarning,
      get cachedPublicSettings() {
        return state.cachedPublicSettings
      },
    }),
  }
})

vi.mock('@/api/payment', () => ({
  paymentAPI: {
    getCheckoutInfo,
  },
}))

vi.mock('@/utils/device', () => ({
  isMobileDevice: () => true,
}))

function checkoutInfoFixture(overrides: Partial<CheckoutInfoResponse> = {}) {
  const wxpayMethod: MethodLimit = {
    daily_limit: 0,
    daily_used: 0,
    daily_remaining: 0,
    single_min: 0,
    single_max: 0,
    fee_rate: 0,
    available: true,
  }
  const data: CheckoutInfoResponse = {
    methods: {
      wxpay: wxpayMethod,
    },
    global_min: 0,
    global_max: 0,
    plans: [],
    balance_disabled: false,
    balance_recharge_multiplier: 1,
    subscription_usd_to_cny_rate: 0,
    recharge_fee_rate: 0,
    help_text: '',
    help_image_url: '',
    stripe_publishable_key: '',
  }

  return {
    data: { ...data, ...overrides },
  }
}

function checkoutInfoWithPlansFixture(options: {
  checkout?: Partial<CheckoutInfoResponse>
  method?: Partial<MethodLimit>
  plan?: Partial<SubscriptionPlan>
} = {}) {
  const base = checkoutInfoFixture(options.checkout).data
  const plan: SubscriptionPlan = {
    id: 7,
    group_id: 3,
    name: 'Starter',
    description: '',
    price: 128,
    original_price: 0,
    validity_days: 30,
    validity_unit: 'day',
    rate_multiplier: 1,
    daily_limit_usd: null,
    weekly_limit_usd: null,
    monthly_limit_usd: null,
    features: [],
    group_platform: 'openai',
    sort_order: 1,
    for_sale: true,
    group_name: 'OpenAI',
    ...options.plan,
  }

  return {
    data: {
      ...base,
      methods: {
        ...base.methods,
        wxpay: {
          ...base.methods.wxpay,
          ...options.method,
        },
      },
      plans: [plan],
    },
  }
}

function jsapiOrderFixture(resumeToken: string) {
  return {
    order_id: 123,
    amount: 88,
    pay_amount: 88,
    fee_rate: 0,
    expires_at: '2099-01-01T00:10:00.000Z',
    payment_type: 'wxpay',
    out_trade_no: 'sub2_jsapi_123',
    result_type: 'jsapi_ready' as const,
    resume_token: resumeToken,
    jsapi: {
      appId: 'wx123',
      timeStamp: '1712345678',
      nonceStr: 'nonce',
      package: 'prepay_id=wx123',
      signType: 'RSA',
      paySign: 'signed',
    },
  }
}

function oauthOrderFixture() {
  return {
    order_id: 456,
    amount: 128,
    pay_amount: 128,
    fee_rate: 0,
    expires_at: '2099-01-01T00:10:00.000Z',
    payment_type: 'wxpay',
    result_type: 'oauth_required' as const,
    oauth: {
      authorize_url: '/api/v1/auth/oauth/wechat/payment/start?payment_type=wxpay&redirect=%2Fpurchase%3Ffrom%3Dwechat',
      appid: 'wx123',
      scope: 'snsapi_base',
      redirect_url: '/auth/wechat/payment/callback',
    },
  }
}

async function mountSubscriptionConfirm(options: Parameters<typeof checkoutInfoWithPlansFixture>[0] = {}) {
  vi.useRealTimers()
  routeState.path = '/purchase'
  routeState.query = {
    tab: 'subscription',
    group: '3',
  }
  routerReplace.mockReset().mockResolvedValue(undefined)
  routerPush.mockReset().mockResolvedValue(undefined)
  routerResolve.mockClear()
  createOrder.mockReset()
  refreshUser.mockReset()
  fetchActiveSubscriptions.mockReset().mockResolvedValue(undefined)
  showError.mockReset()
  showInfo.mockReset()
  showWarning.mockReset()
  getCheckoutInfo.mockReset().mockResolvedValue(checkoutInfoWithPlansFixture(options))
  bridgeInvoke.mockReset()
  window.localStorage.clear()
  ;(window as Window & { WeixinJSBridge?: { invoke: typeof bridgeInvoke } }).WeixinJSBridge = undefined

  const wrapper = shallowMount(PaymentView, {
    global: {
      stubs: {
        AppLayout: {
          template: '<div><slot /></div>',
        },
        Teleport: true,
        Transition: false,
      },
    },
  })
  await flushPromises()
  await flushPromises()
  return wrapper
}

async function mountSubscriptionPlanList(planCount: number) {
  vi.useRealTimers()
  routeState.path = '/purchase'
  routeState.query = { tab: 'subscription' }
  routerReplace.mockReset().mockResolvedValue(undefined)
  routerPush.mockReset().mockResolvedValue(undefined)
  routerResolve.mockClear()
  createOrder.mockReset()
  refreshUser.mockReset()
  fetchActiveSubscriptions.mockReset().mockResolvedValue(undefined)
  showError.mockReset()
  showInfo.mockReset()
  showWarning.mockReset()
  const basePlan = checkoutInfoWithPlansFixture().data.plans[0]
  const plans = Array.from({ length: planCount }, (_, index) => ({
    ...basePlan,
    id: index + 1,
    name: `Plan ${index + 1}`,
  }))
  getCheckoutInfo.mockReset().mockResolvedValue(checkoutInfoFixture({ plans }))
  bridgeInvoke.mockReset()
  window.localStorage.clear()
  ;(window as Window & { WeixinJSBridge?: { invoke: typeof bridgeInvoke } }).WeixinJSBridge = undefined

  const wrapper = shallowMount(PaymentView, {
    global: {
      stubs: {
        AppLayout: {
          template: '<div><slot /></div>',
        },
        Teleport: true,
        Transition: false,
      },
    },
  })
  await flushPromises()
  await flushPromises()
  return wrapper
}

describe('PaymentView help text', () => {
  beforeEach(() => {
    vi.useRealTimers()
    routeState.path = '/purchase'
    routeState.query = {}
    createOrder.mockReset()
    window.localStorage.clear()
  })

  async function mountHelp(help_text: string, help_image_url = '') {
    getCheckoutInfo.mockReset().mockResolvedValue(checkoutInfoFixture({ help_text, help_image_url }))
    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    return wrapper
  }

  it('renders headings, emphasis, links, and lists in payment help without starting checkout', async () => {
    const wrapper = await mountHelp('## Recharge help\n\n**Read first**\n\n- [Contact support](https://example.com/help)')
    const help = wrapper.get('.markdown-body')
    expect(help.get('h2').text()).toBe('Recharge help')
    expect(help.get('strong').text()).toBe('Read first')
    expect(help.get('li a').attributes('href')).toBe('https://example.com/help')
    expect(createOrder).not.toHaveBeenCalled()
  })

  it('removes scripts, event handlers, and unsafe URLs from rendered help', async () => {
    const wrapper = await mountHelp([
      '<script>alert(1)</script>',
      '<img src="https://example.com/help.png" onerror="alert(1)">',
      '[Unsafe](javascript:alert%281%29)',
      '[Support](https://example.com/help)',
    ].join('\n\n'))
    const help = wrapper.get('.markdown-body')
    expect(help.find('script').exists()).toBe(false)
    expect(help.get('img').attributes('onerror')).toBeUndefined()
    expect(help.findAll('a').map(link => link.attributes('href'))).toEqual([undefined, 'https://example.com/help'])
  })

  it('keeps plain-text soft line breaks and the separate help image preview', async () => {
    const wrapper = await mountHelp('First line\nSecond line', 'https://example.com/help.png')
    const help = wrapper.get('.markdown-body')
    expect(help.get('p').text()).toBe('First line\nSecond line')
    expect(help.find('br').exists()).toBe(false)
    await wrapper.get('img').trigger('click')
    expect(wrapper.findAll('img')).toHaveLength(2)
    expect(wrapper.findAll('img')[1].attributes('src')).toBe('https://example.com/help.png')
  })

  it('keeps image-only help without an empty Markdown container', async () => {
    const wrapper = await mountHelp('', 'https://example.com/help.png')
    expect(wrapper.find('.markdown-body').exists()).toBe(false)
    expect(wrapper.get('img').attributes('src')).toBe('https://example.com/help.png')
  })
})

describe('PaymentView subscription plan grid', () => {
  it.each([3, 4, 6])('keeps %i plans on the existing mobile/tablet/desktop grid', async (planCount) => {
    const wrapper = await mountSubscriptionPlanList(planCount)
    const cards = wrapper.findAllComponents(SubscriptionPlanCard)

    expect(cards).toHaveLength(planCount)
    expect([...(cards[0].element.parentElement?.classList ?? [])]).toEqual(expect.arrayContaining([
      'grid',
      'grid-cols-1',
      'sm:grid-cols-2',
      'lg:grid-cols-3',
    ]))
  })
})

describe('PaymentView recharge rate preview', () => {
  it('uses the selected payment method currency in both locale templates', async () => {
    translate.mockClear()
    routeState.path = '/purchase'
    routeState.query = {}
    getCheckoutInfo.mockReset().mockResolvedValue(checkoutInfoFixture({
      balance_recharge_multiplier: 0.5,
      methods: {
        stripe: {
          ...checkoutInfoFixture().data.methods.wxpay,
          currency: 'USD',
        },
      },
    }))

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    wrapper.getComponent(AmountInput).vm.$emit('update:modelValue', 10)
    await flushPromises()

    expect(translate).toHaveBeenCalledWith('payment.rechargeRatePreview', {
      currency: 'USD',
      usd: '0.50',
    })
    expect(en.payment.rechargeRatePreview).toBe('Current rate: 1 {currency} = {usd} USD')
    expect(zh.payment.rechargeRatePreview).toBe('当前倍率：1 {currency} = {usd} USD')
  })
})

describe('PaymentView subscription confirmation amounts', () => {
  it('shows converted CNY pay amount using the subscription rate, not the balance multiplier', async () => {
    const wrapper = await mountSubscriptionConfirm({
      checkout: {
        balance_recharge_multiplier: 0.14,
        subscription_usd_to_cny_rate: 7.15,
      },
      method: {
        currency: 'CNY',
      },
      plan: {
        price: 9.99,
        original_price: 12.99,
      },
    })

    const text = wrapper.text()
    const convertedPrice = formatPaymentAmount(71.43, 'CNY')
    const convertedOriginalPrice = formatPaymentAmount(92.88, 'CNY')

    expect(text).toContain(convertedPrice)
    expect(text).toContain(convertedOriginalPrice)
    expect(text).not.toContain(formatPaymentAmount(9.99, 'CNY'))
    // 换算必须使用订阅汇率（×7.15），而不是余额倍率（÷0.14 = 71.36）
    expect(text).not.toContain(formatPaymentAmount(71.36, 'CNY'))
    expect(wrapper.findAll('button').some(button => button.text().includes(convertedPrice))).toBe(true)
  })

  it('keeps plan price when the subscription rate is not configured or payment currency is not CNY', async () => {
    // opt-in 回归锁：即使余额倍率已配置，未配置订阅汇率时 CNY 订阅仍按 price 直付
    const cnyWrapper = await mountSubscriptionConfirm({
      checkout: {
        balance_recharge_multiplier: 0.14,
        subscription_usd_to_cny_rate: 0,
      },
      method: {
        currency: 'CNY',
      },
      plan: {
        price: 7.99,
      },
    })

    expect(cnyWrapper.text()).toContain(formatPaymentAmount(7.99, 'CNY'))
    expect(cnyWrapper.text()).not.toContain(formatPaymentAmount(57.07, 'CNY'))
    expect(cnyWrapper.text()).not.toContain(formatPaymentAmount(57.13, 'CNY'))

    const usdWrapper = await mountSubscriptionConfirm({
      checkout: {
        subscription_usd_to_cny_rate: 7.15,
      },
      method: {
        currency: 'USD',
      },
      plan: {
        price: 7.99,
        original_price: 9.99,
      },
    })

    expect(usdWrapper.text()).toContain(formatPaymentAmount(7.99, 'USD'))
    expect(usdWrapper.text()).toContain(formatPaymentAmount(9.99, 'USD'))
  })

  it('adds fee rate after CNY rate conversion to match backend pay_amount', async () => {
    const wrapper = await mountSubscriptionConfirm({
      checkout: {
        subscription_usd_to_cny_rate: 7.15,
        recharge_fee_rate: 2.5,
      },
      method: {
        currency: 'CNY',
      },
      plan: {
        price: 9.99,
      },
    })

    const text = wrapper.text()
    const convertedPrice = formatPaymentAmount(71.43, 'CNY')
    const fee = formatPaymentAmount(1.79, 'CNY')
    const total = formatPaymentAmount(73.22, 'CNY')

    expect(text).toContain(convertedPrice)
    expect(text).toContain(fee)
    expect(text).toContain(total)
    expect(wrapper.findAll('button').some(button => button.text().includes(total))).toBe(true)
  })
})

describe('PaymentView payment recovery', () => {
  beforeEach(() => {
    vi.useRealTimers()
    routeState.path = '/purchase'
    routeState.query = {}
    routerReplace.mockReset().mockResolvedValue(undefined)
    routerPush.mockReset().mockResolvedValue(undefined)
    routerResolve.mockClear()
    createOrder.mockReset()
    refreshUser.mockReset()
    fetchActiveSubscriptions.mockReset().mockResolvedValue(undefined)
    showError.mockReset()
    showInfo.mockReset()
    showWarning.mockReset()
    bridgeInvoke.mockReset()
    window.localStorage.clear()
    ;(window as Window & { WeixinJSBridge?: { invoke: typeof bridgeInvoke } }).WeixinJSBridge = undefined
  })

  it('restores a custom EasyPay method as the selected payment method', async () => {
    getCheckoutInfo.mockResolvedValue(checkoutInfoFixture({
      methods: {
        wxpay: checkoutInfoFixture().data.methods.wxpay,
        ldc: {
          daily_limit: 0,
          daily_used: 0,
          daily_remaining: 0,
          single_min: 0,
          single_max: 0,
          fee_rate: 0,
          available: true,
          display_name: 'LDC Pay',
        },
      },
    }))
    window.localStorage.setItem(PAYMENT_RECOVERY_STORAGE_KEY, JSON.stringify({
      orderId: 888,
      amount: 66,
      qrCode: 'ldc-qr',
      expiresAt: '2099-01-01T00:10:00.000Z',
      paymentType: 'ldc',
      payUrl: 'https://pay.example.com/ldc',
      outTradeNo: 'sub2_ldc_888',
      clientSecret: '',
      intentId: '',
      currency: '',
      countryCode: '',
      paymentEnv: '',
      payAmount: 66,
      orderType: 'balance',
      paymentMode: 'popup',
      resumeToken: '',
      createdAt: Date.now(),
    }))

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          AppLayout: {
            template: '<div><slot /></div>',
          },
          PaymentStatusPanel: {
            template: '<button data-test="payment-done" @click="$emit(\'done\')" />',
          },
          PaymentMethodSelector: {
            props: ['selected'],
            template: '<div data-test="method-selector">{{ selected }}</div>',
          },
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()
    await wrapper.find('[data-test="payment-done"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="method-selector"]').text()).toBe('ldc')
  })
})

describe('PaymentView WeChat JSAPI flow', () => {
  beforeEach(() => {
    routeState.path = '/purchase'
    routeState.query = {
      wechat_resume: '1',
      wechat_resume_token: 'resume-token-123',
    }
    routerReplace.mockReset().mockResolvedValue(undefined)
    routerPush.mockReset().mockResolvedValue(undefined)
    routerResolve.mockClear()
    createOrder.mockReset()
    refreshUser.mockReset()
    fetchActiveSubscriptions.mockReset().mockResolvedValue(undefined)
    showError.mockReset()
    showInfo.mockReset()
    showWarning.mockReset()
    getCheckoutInfo.mockReset().mockResolvedValue(checkoutInfoFixture())
    bridgeInvoke.mockReset()
    window.localStorage.clear()
    ;(window as Window & { WeixinJSBridge?: { invoke: typeof bridgeInvoke } }).WeixinJSBridge = {
      invoke: bridgeInvoke,
    }
  })

  it('resets payment state and redirects to /payment/result after JSAPI reports success', async () => {
    createOrder.mockResolvedValue(jsapiOrderFixture('resume-token-123'))
    bridgeInvoke.mockImplementation((_action, _payload, callback) => {
      callback({ err_msg: 'get_brand_wcpay_request:ok' })
    })

    shallowMount(PaymentView, {
      global: {
        stubs: {
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()

    expect(routerReplace).toHaveBeenCalledWith({ path: '/purchase', query: {} })
    expect(routerPush).toHaveBeenCalledWith({
      path: '/payment/result',
      query: {
        order_id: '123',
        out_trade_no: 'sub2_jsapi_123',
        resume_token: 'resume-token-123',
      },
    })
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toBeNull()
  })

  it('resets payment state when JSAPI reports cancellation', async () => {
    createOrder.mockResolvedValue(jsapiOrderFixture('resume-token-cancel'))
    bridgeInvoke.mockImplementation((_action, _payload, callback) => {
      callback({ err_msg: 'get_brand_wcpay_request:cancel' })
    })

    shallowMount(PaymentView, {
      global: {
        stubs: {
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()

    expect(showInfo).toHaveBeenCalledWith('payment.qr.cancelled')
    expect(routerPush).not.toHaveBeenCalled()
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toBeNull()
  })

  it('clears stale recovery state when JSAPI never becomes available', async () => {
    vi.useFakeTimers()
    createOrder.mockResolvedValue(jsapiOrderFixture('resume-token-missing-bridge'))
    ;(window as Window & { WeixinJSBridge?: { invoke: typeof bridgeInvoke } }).WeixinJSBridge = undefined

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          Teleport: true,
          Transition: false,
        },
      },
    })

    await flushPromises()
    await vi.advanceTimersByTimeAsync(4000)
    await flushPromises()
    await flushPromises()

    expect(showError).toHaveBeenCalledWith(
      'payment.errors.wechatJsapiUnavailable payment.errors.wechatOpenInWeChatHint',
    )
    expect(routerPush).not.toHaveBeenCalled()
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toBeNull()
    expect(wrapper.html()).not.toContain('payment-status-panel-stub')
  })

  it('clears a stale recovery snapshot before handling wechat resume callback params', async () => {
    createOrder.mockRejectedValueOnce(new Error('resume failed'))
    window.localStorage.setItem(PAYMENT_RECOVERY_STORAGE_KEY, JSON.stringify({
      orderId: 999,
      amount: 66,
      qrCode: 'stale-qr',
      expiresAt: '2099-01-01T00:10:00.000Z',
      paymentType: 'alipay',
      payUrl: 'https://pay.example.com/stale',
      outTradeNo: 'stale-out-trade-no',
      clientSecret: '',
      intentId: '',
      currency: '',
      countryCode: '',
      paymentEnv: '',
      payAmount: 66,
      orderType: 'balance',
      paymentMode: 'popup',
      resumeToken: '',
      createdAt: Date.UTC(2099, 0, 1, 0, 0, 0),
    }))

    shallowMount(PaymentView, {
      global: {
        stubs: {
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()

    expect(createOrder).toHaveBeenCalledWith(expect.objectContaining({
      wechat_resume_token: 'resume-token-123',
    }))
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toBeNull()
  })

  it('keeps subscription resume context for token-only WeChat callbacks', async () => {
    routeState.query = {
      wechat_resume: '1',
      wechat_resume_token: 'resume-subscription-7',
      payment_type: 'wxpay_direct',
      order_type: 'subscription',
      plan_id: '7',
    }
    getCheckoutInfo.mockResolvedValue(checkoutInfoWithPlansFixture())
    createOrder.mockResolvedValue(oauthOrderFixture())

    const originalLocation = window.location
    const locationState = {
      href: 'http://localhost/purchase',
      origin: 'http://localhost',
    }
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: locationState,
    })

    shallowMount(PaymentView, {
      global: {
        stubs: {
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()

    expect(routerReplace).toHaveBeenCalledWith({ path: '/purchase', query: {} })
    expect(createOrder).toHaveBeenCalledWith(expect.objectContaining({
      payment_type: 'wxpay',
      order_type: 'subscription',
      plan_id: 7,
      wechat_resume_token: 'resume-subscription-7',
    }))
    expect(locationState.href).toContain('/api/v1/auth/oauth/wechat/payment/start?')
    expect(new URL(locationState.href, 'http://localhost').searchParams.get('redirect')).toBe(
      '/purchase?from=wechat&payment_type=wxpay&order_type=subscription&plan_id=7',
    )

    Object.defineProperty(window, 'location', {
      configurable: true,
      value: originalLocation,
    })
  })

  it('falls back to QR flow when mobile WeChat payment is unavailable', async () => {
    routeState.query = {
      wechat_resume: '1',
      wechat_resume_token: 'resume-token-h5',
      payment_type: 'wxpay_direct',
    }
    createOrder
      .mockRejectedValueOnce({ reason: 'WECHAT_H5_NOT_AUTHORIZED' })
      .mockResolvedValueOnce({
        order_id: 778,
        amount: 88,
        pay_amount: 88,
        fee_rate: 0,
        expires_at: '2099-01-01T00:10:00.000Z',
        payment_type: 'wxpay',
        qr_code: 'weixin://wxpay/bizpayurl?pr=fallback-native',
        out_trade_no: 'sub2_qr_778',
      })

    shallowMount(PaymentView, {
      global: {
        stubs: {
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()

    expect(createOrder).toHaveBeenNthCalledWith(1, expect.objectContaining({
      payment_type: 'wxpay',
      is_mobile: true,
      wechat_resume_token: 'resume-token-h5',
    }))
    expect(createOrder).toHaveBeenNthCalledWith(2, expect.objectContaining({
      payment_type: 'wxpay',
      is_mobile: false,
      payment_source: 'hosted_redirect',
    }))
    expect(showWarning).toHaveBeenCalledWith('payment.errors.mobilePaymentFallbackToQr')
    expect(showError).not.toHaveBeenCalled()
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toContain('weixin://wxpay/bizpayurl?pr=fallback-native')
  })
})

describe('PaymentView subscription feature flag', () => {
  afterEach(() => {
    appStoreState.setPublicSettings(undefined)
  })

  function tabLabels(wrapper: Awaited<ReturnType<typeof mountSubscriptionPlanList>>) {
    return wrapper
      .findAll('button')
      .map((button) => button.text())
      .filter((text) => text === 'payment.tabTopUp' || text === 'payment.tabSubscribe')
  }

  it('keeps the top-up / subscribe switcher when subscription_enabled is absent (opt-out default)', async () => {
    const wrapper = await mountSubscriptionPlanList(2)

    expect(tabLabels(wrapper)).toEqual(['payment.tabTopUp', 'payment.tabSubscribe'])
    expect(wrapper.findAllComponents(SubscriptionPlanCard)).toHaveLength(2)
  })

  it('drops the subscribe tab, hides the switcher and ignores ?tab=subscription when subscriptions are disabled', async () => {
    appStoreState.setPublicSettings({ subscription_enabled: false })
    const wrapper = await mountSubscriptionPlanList(2)

    expect(tabLabels(wrapper)).toEqual([])
    expect(wrapper.findAllComponents(SubscriptionPlanCard)).toHaveLength(0)
    expect(wrapper.text()).toContain('payment.rechargeAccount')
  })

  it('shows an unavailable notice instead of a doomed top-up form when balance recharge is disabled too', async () => {
    appStoreState.setPublicSettings({ subscription_enabled: false })
    const wrapper = await mountSubscriptionConfirm({ checkout: { balance_disabled: true } })

    expect(tabLabels(wrapper)).toEqual([])
    expect(wrapper.findAllComponents(SubscriptionPlanCard)).toHaveLength(0)
    expect(wrapper.text()).not.toContain('payment.confirmSubscription')
    expect(wrapper.text()).not.toContain('payment.rechargeAccount')
    expect(wrapper.text()).toContain('payment.billingUnavailable')
    wrapper.unmount()
  })

  it('falls back from the subscribe tab to top-up when the flag flips off after mount', async () => {
    const wrapper = await mountSubscriptionPlanList(2)
    expect(wrapper.findAllComponents(SubscriptionPlanCard)).toHaveLength(2)

    appStoreState.setPublicSettings({ subscription_enabled: false })
    await flushPromises()

    expect(tabLabels(wrapper)).toEqual([])
    expect(wrapper.findAllComponents(SubscriptionPlanCard)).toHaveLength(0)
    expect(wrapper.text()).toContain('payment.rechargeAccount')
    wrapper.unmount()
  })

  it('enters the subscribe tab when a subscription-only site turns subscriptions back on', async () => {
    appStoreState.setPublicSettings({ subscription_enabled: false })
    const wrapper = await mountSubscriptionConfirm({ checkout: { balance_disabled: true } })
    expect(wrapper.text()).toContain('payment.billingUnavailable')

    appStoreState.setPublicSettings({ subscription_enabled: true })
    await flushPromises()

    expect(wrapper.text()).not.toContain('payment.billingUnavailable')
    expect(wrapper.text()).not.toContain('payment.rechargeAccount')
    expect(wrapper.findAllComponents(SubscriptionPlanCard).length).toBeGreaterThan(0)
    wrapper.unmount()
  })
})

describe('Binance UID top-up page', () => {
  const wrappers: ReturnType<typeof shallowMount>[] = []
  function pendingOrder(): BinanceRechargeOrder {
    return {
      id: 41, order_no: 'BN-41', user_id: 5, account_id: 7,
      account_name: 'Receiving account', recipient_uid: '123456', requested_amount: 10,
      pay_amount: '10.01234', credited_amount: '0.00000', status: 'PENDING',
      created_at: '2026-10-01T00:00:00Z', expires_at: '2099-10-01T00:15:00Z',
    }
  }

  beforeEach(() => {
    vi.useRealTimers()
    binanceHarness.isAdmin = false
    binanceHarness.createOrder.mockReset().mockResolvedValue({ data: pendingOrder() })
    binanceHarness.getOrders.mockReset().mockResolvedValue({ data: { items: [], total: 0, page: 1, page_size: 20, pages: 0 } })
    binanceHarness.getOrder.mockReset().mockResolvedValue({ data: pendingOrder() })
    binanceHarness.refreshOrder.mockReset().mockResolvedValue({ data: pendingOrder() })
    binanceHarness.getStats.mockReset().mockResolvedValue({ data: { total_amount: '0.00000', total_orders: 0, items: [], total: 0, page: 1, page_size: 20, pages: 0, account_options: [] } })
    binanceHarness.fetchConfig.mockReset().mockResolvedValue(undefined)
    binanceHarness.copyToClipboard.mockReset().mockResolvedValue(true)
    binanceHarness.showSuccess.mockReset()
    binanceHarness.getAccounts.mockReset().mockResolvedValue({ data: [] })
    binanceHarness.updateAccount.mockReset().mockResolvedValue({ data: {} })
    binanceHarness.getBills.mockReset().mockResolvedValue({ data: {
      account_id: 7, account_name: 'Receiving account', recipient_uid: '123456',
      start_time: '2026-10-01T00:00:00+08:00', end_time: '2026-10-01T12:00:00+08:00',
      limit: 100, possibly_more: true, total: 1,
      items: [{ id: '9007199254740993123', amount: '-0.0000000100', currency: 'USDT', channel: 'pay', kind: 'PAY', direction: 'outgoing', status: 'recorded', time: '2026-10-01T10:30:00+08:00', fields: [] }],
    } })
    refreshUser.mockReset().mockResolvedValue(undefined)
    showError.mockReset()
    translate.mockImplementation((key: string, params?: Record<string, unknown>) => key === 'binanceRecharge.completed' ? `Credited ${params?.amount} USD` : key)
  })

  afterEach(() => {
    for (const wrapper of wrappers.splice(0)) wrapper.unmount()
    binanceHarness.isAdmin = false
    translate.mockImplementation((key: string) => key)
  })

  async function mountBinance() {
    const wrapper = shallowMount(BinanceRechargeView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          BinanceOrderDetails: false,
          Teleport: true,
        },
      },
    })
    wrappers.push(wrapper)
    await flushPromises()
    return wrapper
  }

  it('creates a whole-USDT order and displays and copies the full server amount', async () => {
    const wrapper = await mountBinance()
    await wrapper.get('#binance-amount').setValue('10')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(binanceHarness.createOrder).toHaveBeenCalledWith(7, 10)
    expect(wrapper.get('[data-testid="binance-exact-amount"]').text()).toBe('10.01234 USDT')
    await wrapper.get('[data-testid="binance-copy-amount"]').trigger('click')
    expect(binanceHarness.copyToClipboard).toHaveBeenCalledWith('10.01234')
    expect(refreshUser).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="binance-credited"]').exists()).toBe(false)
  })

  it('refreshes the balance only after the server confirms exact-value credit', async () => {
    const wrapper = await mountBinance()
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    binanceHarness.refreshOrder.mockResolvedValue({ data: { ...pendingOrder(), status: 'COMPLETED', credited_amount: '10.01234' } })
    wrapper.getComponent(BinanceOrderDetails).vm.$emit('refresh')
    await flushPromises()
    expect(binanceHarness.refreshOrder).toHaveBeenCalledWith(41)
    expect(refreshUser).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-testid="binance-credited"]').text()).toContain('10.01234 USD')
    expect(wrapper.find('[data-testid="binance-copy-amount"]').exists()).toBe(false)
  })

  it('keeps a pending order pending after a failed lookup', async () => {
    const wrapper = await mountBinance()
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    binanceHarness.refreshOrder.mockRejectedValue(new Error('Binance query unavailable'))
    wrapper.getComponent(BinanceOrderDetails).vm.$emit('refresh')
    await flushPromises()
    expect(wrapper.getComponent(BinanceOrderDetails).props('order').status).toBe('PENDING')
    expect(wrapper.find('[data-testid="binance-credited"]').exists()).toBe(false)
    expect(refreshUser).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith('Binance query unavailable')
  })

  it('rejects fractional requested amounts before sending an order', async () => {
    const wrapper = await mountBinance()
    await wrapper.get('#binance-amount').setValue('10.01')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(binanceHarness.createOrder).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith('binanceRecharge.invalidAmount')
  })

  it('shows user order and totals tabs without the account configuration or daily usage tabs', async () => {
    const wrapper = await mountBinance()
    expect(wrapper.text()).toContain('binanceRecharge.orders')
    expect(wrapper.text()).toContain('binanceRecharge.totals')
    expect(wrapper.text()).not.toContain('binanceRecharge.accounts')
    expect(wrapper.text()).not.toContain('binanceRecharge.dailyUsage.title')
    expect(wrapper.text()).not.toContain('binanceRecharge.liveBills.title')
    expect(wrapper.findComponent(AdminBinanceRechargeView).exists()).toBe(false)
  })

  it('routes an administrator to the management component without loading personal orders', async () => {
    binanceHarness.isAdmin = true
    const wrapper = await mountBinance()
    expect(wrapper.findComponent(AdminBinanceRechargeView).exists()).toBe(true)
    expect(binanceHarness.getOrders).not.toHaveBeenCalled()
    expect(wrapper.find('#binance-amount').exists()).toBe(false)
  })

  it('shows all five administrator tabs and never copies returned credential fields into the edit form', async () => {
    binanceHarness.getAccounts.mockResolvedValue({ data: [{
      id: 7, name: 'Receiving account', recipient_uid: '123456', enabled: true, order_ttl_minutes: 15,
      has_orders: true, api_key_configured: true, api_secret_configured: true,
      api_key: 'should-never-be-read', api_secret: 'should-never-be-read',
    }] })
    const wrapper = shallowMount(AdminBinanceRechargeView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' },
        },
      },
    })
    wrappers.push(wrapper)
    await flushPromises()
    for (const label of ['accounts', 'orders', 'totals', 'dailyUsage.title', 'liveBills.title']) {
      expect(wrapper.text()).toContain(`binanceRecharge.${label}`)
    }
    await wrapper.findAll('button').find(button => button.text() === 'common.edit')!.trigger('click')
    expect((wrapper.get('#binance-api-key').element as HTMLInputElement).value).toBe('')
    expect((wrapper.get('#binance-api-secret').element as HTMLInputElement).value).toBe('')
    expect(wrapper.get('#binance-uid').attributes('disabled')).toBeDefined()
    await wrapper.get('#binance-account-form').trigger('submit')
    await flushPromises()
    expect(binanceHarness.updateAccount).toHaveBeenCalledWith(7, {
      name: 'Receiving account', recipient_uid: '123456', enabled: true, order_ttl_minutes: 15,
    })
  })

  async function mountLiveBills() {
    const wrapper = shallowMount(BinanceLiveBills, {
      props: { accounts: [{ id: 7, name: 'Receiving account', recipient_uid: '123456', order_ttl_minutes: 15 }] },
      global: { stubs: {
        DataTable: { props: ['data'], template: '<div><div v-for="row in data" :key="row.id"><slot name="cell-amount" :row="row" /><slot name="cell-actions" :row="row" /></div></div>' },
        BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' },
      } },
    })
    wrappers.push(wrapper)
    await flushPromises()
    return wrapper
  }

  it('queries live Pay bills with explicit dates and preserves raw amount precision and truncation warnings', async () => {
    const wrapper = await mountLiveBills()
    const initialStart = (wrapper.get('#binance-bills-start').element as HTMLInputElement).value
    expect(new Date(initialStart).getHours()).toBe(0)
    expect(new Date(initialStart).getDate()).toBe(new Date().getDate())
    await wrapper.get('#binance-bills-start').setValue('2026-10-01T00:00:00')
    await wrapper.get('#binance-bills-end').setValue('2026-10-01T12:00:00')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(binanceHarness.getBills).toHaveBeenCalledWith(7, {
      start_time: new Date('2026-10-01T00:00:00').toISOString(),
      end_time: new Date('2026-10-01T12:00:00').toISOString(),
    })
    expect(wrapper.get('[data-testid="binance-bill-amount"]').text()).toBe('-0.0000000100 USDT')
    expect(wrapper.get('[data-testid="binance-bills-more"]').text()).toBe('binanceRecharge.liveBills.possiblyMore')
    expect(binanceHarness.refreshOrder).not.toHaveBeenCalled()
    expect(binanceHarness.createOrder).not.toHaveBeenCalled()
  })

  it('clears live results after a date change and does not show an empty successful query on API failure', async () => {
    const wrapper = await mountLiveBills()
    await wrapper.get('#binance-bills-start').setValue('2026-10-01T00:00:00')
    await wrapper.get('#binance-bills-end').setValue('2026-10-01T12:00:00')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(wrapper.find('[data-testid="binance-bill-amount"]').exists()).toBe(true)
    await wrapper.get('#binance-bills-end').setValue('2026-10-01T11:00:00')
    expect(wrapper.find('[data-testid="binance-bill-amount"]').exists()).toBe(false)
    await wrapper.get('#binance-bills-start').setValue('2026-10-01T00:00:00')
    binanceHarness.getBills.mockRejectedValue(new Error('Query rate limited'))
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('Query rate limited')
    expect(wrapper.find('[data-testid="binance-bills-more"]').exists()).toBe(false)
  })

  it('rejects live bill ranges longer than 89 days before calling Binance', async () => {
    const wrapper = await mountLiveBills()
    await wrapper.get('#binance-bills-start').setValue('2026-01-01T00:00:00')
    await wrapper.get('#binance-bills-end').setValue('2026-10-01T00:00:00')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(binanceHarness.getBills).not.toHaveBeenCalled()
    expect(wrapper.get('[role="alert"]').text()).toBe('binanceRecharge.liveBills.invalidRange')
  })

  it('renders bill field text without rounding long IDs or exposing credential-like fields', () => {
    const wrapper = shallowMount(BinanceBillFields, { props: { fields: [
      { key: 'transactionId', type: 'number', value: '9007199254740993123' },
      { key: 'amount', type: 'string', value: '-0.0000000100' },
      { key: 'api_secret', type: 'string', value: 'must-not-render' },
    ] } })
    wrappers.push(wrapper)
    expect(wrapper.text()).toContain('9007199254740993123')
    expect(wrapper.text()).toContain('-0.0000000100')
    expect(wrapper.text()).not.toContain('must-not-render')
  })
})
