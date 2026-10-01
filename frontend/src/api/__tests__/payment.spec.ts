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

  it('creates an authenticated order and preserves all five payment decimals as strings', async () => {
    post.mockResolvedValue({ data: { id: 41, pay_amount: '10.01234', credited_amount: '0.00000', status: 'PENDING' } })
    const result = await binanceRechargeAPI.createOrder(7, 10)
    expect(post).toHaveBeenCalledWith('/binance-recharge/orders', { account_id: 7, amount: 10 })
    expect(result.data.pay_amount).toBe('10.01234')
    expect(result.data.credited_amount).toBe('0.00000')
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
    get.mockResolvedValue({ data: { total_amount: '1000000.01234', total_orders: 1, items: [] } })
    const result = await binanceRechargeAPI.getStats({ account_id: 7 })
    expect(get).toHaveBeenLastCalledWith('/binance-recharge/stats', { params: { account_id: 7 } })
    expect(result.data.total_amount).toBe('1000000.01234')
    await adminBinanceRechargeAPI.getStats({ account_id: 7, page: 2, page_size: 20 })
    expect(get).toHaveBeenLastCalledWith('/admin/binance-recharge/stats', { params: { account_id: 7, page: 2, page_size: 20 } })
  })

  it('preserves omitted credentials on account edits and never adds them to query strings', async () => {
    const payload = { name: 'Receiving account', recipient_uid: '123456', enabled: true, order_ttl_minutes: 15 }
    await adminBinanceRechargeAPI.updateAccount(7, payload)
    expect(put).toHaveBeenCalledWith('/admin/binance-recharge/accounts/7', payload)
    expect(put.mock.calls[0][1]).not.toHaveProperty('api_key')
    expect(put.mock.calls[0][1]).not.toHaveProperty('api_secret')
  })

  it('propagates lookup failures without fabricating a completed order', async () => {
    post.mockRejectedValue(new Error('reconciliation unavailable'))
    await expect(binanceRechargeAPI.refreshOrder(41)).rejects.toThrow('reconciliation unavailable')
  })
})
