import { apiClient } from './client'

// This read-only query may try both saved routes and three bounded backoffs.
// Leave ordinary application requests on their existing timeout.
const binanceQueryTimeout = 520000
import type {
  BinanceRechargeAccount,
  BinanceRechargeAccountInput,
  BinanceRechargeConfig,
  BinanceRechargeOrder,
  BinanceRechargeOrderFilters,
  BinanceRechargeOrders,
  BinanceRechargeStats,
  BinanceLiveBills,
} from '@/types/binanceRecharge'

export const binanceRechargeAPI = {
  getConfig: () => apiClient.get<BinanceRechargeConfig>('/binance-recharge/config'),
  createOrder: (account_id: number, amount: number) =>
    apiClient.post<BinanceRechargeOrder>('/binance-recharge/orders', { account_id, amount }),
  getOrders: (params: BinanceRechargeOrderFilters) =>
    apiClient.get<BinanceRechargeOrders>('/binance-recharge/orders', { params }),
  getOrder: (id: number) => apiClient.get<BinanceRechargeOrder>(`/binance-recharge/orders/${id}`),
  refreshOrder: (id: number) => apiClient.post<BinanceRechargeOrder>(`/binance-recharge/orders/${id}/refresh`, undefined, { timeout: binanceQueryTimeout }),
  getStats: (params: { account_id?: number; page?: number; page_size?: number }) =>
    apiClient.get<BinanceRechargeStats>('/binance-recharge/stats', { params }),
}

export const adminBinanceRechargeAPI = {
  getAccounts: () => apiClient.get<BinanceRechargeAccount[]>('/admin/binance-recharge/accounts'),
  createAccount: (data: BinanceRechargeAccountInput) =>
    apiClient.post<BinanceRechargeAccount>('/admin/binance-recharge/accounts', data),
  updateAccount: (id: number, data: BinanceRechargeAccountInput) =>
    apiClient.put<BinanceRechargeAccount>(`/admin/binance-recharge/accounts/${id}`, data),
  checkAccount: (id: number) =>
    apiClient.post<{ matched: number; review: number }>(`/admin/binance-recharge/accounts/${id}/check`, undefined, { timeout: binanceQueryTimeout }),
  getBills: (id: number, params: { start_time: string; end_time: string }) =>
    apiClient.get<BinanceLiveBills>(`/admin/binance-recharge/accounts/${id}/bills`, { params, timeout: 240000 }),
  getOrders: (params: BinanceRechargeOrderFilters) =>
    apiClient.get<BinanceRechargeOrders>('/admin/binance-recharge/orders', { params }),
  refreshOrder: (id: number) =>
    apiClient.post<BinanceRechargeOrder>(`/admin/binance-recharge/orders/${id}/refresh`, undefined, { timeout: binanceQueryTimeout }),
  getStats: (params: { account_id?: number; page?: number; page_size?: number }) =>
    apiClient.get<BinanceRechargeStats>('/admin/binance-recharge/stats', { params }),
}
