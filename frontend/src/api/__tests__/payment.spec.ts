import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, post, put } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
}))

vi.mock('@/api/client', () => ({
  apiClient: {
    get,
    post,
    put,
  },
}))

import { paymentAPI } from '@/api/payment'
import { adminBinanceRechargeAPI, binanceRechargeAPI } from '@/api/binanceRecharge'
import { securityPolicyRankingAPI } from '@/api/admin/securityPolicyRanking'

describe('PKSecurityPolicyRanking API', () => {
  const range = { start_date: '2026-10-03', end_date: '2026-10-03', timezone: 'Asia/Shanghai' }
  beforeEach(() => {
    get.mockReset().mockResolvedValue({ data: {} })
    post.mockReset().mockResolvedValue({ data: {} })
    put.mockReset().mockResolvedValue({ data: {} })
  })

  it('keeps public and admin reads separate with identical explicit date/timezone pagination', async () => {
    const params = { ...range, page: 2, page_size: 50 }
    const signal = new AbortController().signal
    await securityPolicyRankingAPI.getPublicRanking(params, signal)
    expect(get).toHaveBeenLastCalledWith('/security-policy-ranking', { params, signal })
    await securityPolicyRankingAPI.getRanking(params, signal)
    expect(get).toHaveBeenLastCalledWith('/admin/security-policy-ranking', { params, signal })
  })

  it('uses admin settings routes and preserves decimal text and auto-disable flags', async () => {
    await securityPolicyRankingAPI.getSettings()
    expect(get).toHaveBeenCalledWith('/admin/security-policy-ranking/settings')
    const settings = { multiplier: '0.290001', auto_disable_enabled: true, auto_disable_threshold: 1000000000, reset_on_manual_enable: false }
    await securityPolicyRankingAPI.saveSettings(settings)
    expect(put).toHaveBeenCalledWith('/admin/security-policy-ranking/settings', settings)
  })

  it('uses the server preview ID for deduction without sending client amounts or counts', async () => {
    const signal = new AbortController().signal
    post.mockResolvedValueOnce({ data: { id: 'preview-uuid', trigger_count: 100, multiplier: '0.290001', amount: '29.00010000', balance: '40.00000000' } })
    const result = await securityPolicyRankingAPI.preview(7, range, signal)
    expect(post).toHaveBeenLastCalledWith('/admin/security-policy-ranking/users/7/preview', range, { signal })
    expect(result.data.amount).toBe('29.00010000')
    await securityPolicyRankingAPI.deduct(7, result.data.id)
    expect(post).toHaveBeenLastCalledWith('/admin/security-policy-ranking/users/7/deduct', { preview_id: 'preview-uuid' })
  })
})

describe('payment api', () => {
  beforeEach(() => {
    get.mockReset()
    post.mockReset()
    get.mockResolvedValue({ data: {} })
    post.mockResolvedValue({ data: {} })
  })

  it('keeps legacy public out_trade_no verification for upgrade compatibility', async () => {
    await paymentAPI.verifyOrderPublic('legacy-order-no')

    expect(post).toHaveBeenCalledWith('/payment/public/orders/verify', {
      out_trade_no: 'legacy-order-no',
    })
  })

  it('keeps signed public resume-token resolve endpoint', async () => {
    await paymentAPI.resolveOrderPublicByResumeToken('resume-token-123')

    expect(post).toHaveBeenCalledWith('/payment/public/orders/resolve', {
      resume_token: 'resume-token-123',
    })
  })
})

describe('Binance recharge API', () => {
  beforeEach(() => {
    get.mockReset().mockResolvedValue({ data: {} })
    post.mockReset().mockResolvedValue({ data: {} })
    put.mockReset().mockResolvedValue({ data: {} })
  })

  it('preserves the exact transfer, rate snapshot and eight-decimal system credit as strings', async () => {
    post.mockResolvedValue({ data: { id: 41, pay_amount: '10.01234', credit_multiplier: '6.700000', system_amount: '67.08267800', credited_amount: '0.00000000', status: 'PENDING' } })
    const result = await binanceRechargeAPI.createOrder(7, 10)
    expect(post).toHaveBeenCalledWith('/binance-recharge/orders', { account_id: 7, amount: 10 })
    expect(result.data.pay_amount).toBe('10.01234')
    expect(result.data.credit_multiplier).toBe('6.700000')
    expect(result.data.system_amount).toBe('67.08267800')
    expect(result.data.credited_amount).toBe('0.00000000')
  })

  it('keeps user and administrator order routes separate and forwards account filters', async () => {
    const filters = { page: 1, page_size: 20, account_id: 7, status: 'COMPLETED' as const }
    await binanceRechargeAPI.getOrders(filters)
    expect(get).toHaveBeenLastCalledWith('/binance-recharge/orders', { params: filters })
    await adminBinanceRechargeAPI.getOrders(filters)
    expect(get).toHaveBeenLastCalledWith('/admin/binance-recharge/orders', { params: filters })
    await binanceRechargeAPI.refreshOrder(41)
    expect(post).toHaveBeenLastCalledWith('/binance-recharge/orders/41/refresh')
    await adminBinanceRechargeAPI.refreshOrder(41)
    expect(post).toHaveBeenLastCalledWith('/admin/binance-recharge/orders/41/refresh')
  })

  it('keeps full precision in cumulative statistics and uses authenticated stats endpoints', async () => {
    get.mockResolvedValue({ data: { total_amount: '1000000.01234', total_credited_amount: '6700000.08267800', total_orders: 1, items: [{ user_id: 5, total_amount: '1000000.01234', total_credited_amount: '6700000.08267800', total_orders: 1 }] } })
    const result = await binanceRechargeAPI.getStats({ account_id: 7 })
    expect(get).toHaveBeenLastCalledWith('/binance-recharge/stats', { params: { account_id: 7 } })
    expect(result.data.total_amount).toBe('1000000.01234')
    expect(result.data.total_credited_amount).toBe('6700000.08267800')
    expect(result.data.items[0].total_credited_amount).toBe('6700000.08267800')
    await adminBinanceRechargeAPI.getStats({ account_id: 7, page: 2, page_size: 20 })
    expect(get).toHaveBeenLastCalledWith('/admin/binance-recharge/stats', { params: { account_id: 7, page: 2, page_size: 20 } })
  })

  it('preserves omitted credentials on account edits and never adds them to query strings', async () => {
    const payload = { name: 'Receiving account', recipient_uid: '123456', enabled: true, order_ttl_minutes: 15 }
    await adminBinanceRechargeAPI.updateAccount(7, payload)
    expect(put).toHaveBeenCalledWith('/admin/binance-recharge/accounts/7', payload)
    expect(put.mock.calls[0][1]).not.toHaveProperty('api_key')
    expect(put.mock.calls[0][1]).not.toHaveProperty('api_secret')
    expect(put.mock.calls[0][1]).not.toHaveProperty('credit_multiplier')
  })

  it('sends explicit account multipliers as decimal strings without rounding', async () => {
    const payload = { name: 'Receiving account', recipient_uid: '123456', enabled: true, order_ttl_minutes: 15, credit_multiplier: '6.700000' }
    await adminBinanceRechargeAPI.createAccount(payload)
    expect(post).toHaveBeenLastCalledWith('/admin/binance-recharge/accounts', payload)
    await adminBinanceRechargeAPI.updateAccount(7, { ...payload, credit_multiplier: '0.000001' })
    expect(put).toHaveBeenLastCalledWith('/admin/binance-recharge/accounts/7', { ...payload, credit_multiplier: '0.000001' })
  })

  it('propagates lookup failures without fabricating a completed order', async () => {
    post.mockRejectedValue(new Error('reconciliation unavailable'))
    await expect(binanceRechargeAPI.refreshOrder(41)).rejects.toThrow('reconciliation unavailable')
  })

  it('queries live bills only through the administrator read endpoint and preserves source precision', async () => {
    get.mockResolvedValue({ data: { limit: 100, possibly_more: true, items: [{ amount: '-0.0000000100', id: '9007199254740993123' }] } })
    const params = { start_time: '2026-10-01T00:00:00+08:00', end_time: '2026-10-01T12:00:00+08:00' }
    const result = await adminBinanceRechargeAPI.getBills(7, params)
    expect(get).toHaveBeenCalledWith('/admin/binance-recharge/accounts/7/bills', { params })
    expect(result.data.items[0].amount).toBe('-0.0000000100')
    expect(result.data.items[0].id).toBe('9007199254740993123')
    expect(result.data.possibly_more).toBe(true)
    expect(post).not.toHaveBeenCalled()
    expect(binanceRechargeAPI).not.toHaveProperty('getBills')
  })
})
