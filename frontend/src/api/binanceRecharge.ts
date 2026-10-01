import { apiClient } from './client'
import type {
  BinanceRechargeAccount,
  BinanceRechargeAccountInput,
  BinanceRechargeConfig,
  BinanceRechargeOrder,
  BinanceRechargeOrderFilters,
  BinanceRechargeOrders,
  BinanceRechargeStats,
} from '@/types/binanceRecharge'

export const binanceRechargeAPI = {
  getConfig: () => apiClient.get<BinanceRechargeConfig>('/binance-recharge/config'),
  createOrder: (account_id: number, amount: number) =>
    apiClient.post<BinanceRechargeOrder>('/binance-recharge/orders', { account_id, amount }),
  getOrders: (params: BinanceRechargeOrderFilters) =>
    apiClient.get<BinanceRechargeOrders>('/binance-recharge/orders', { params }),
  getOrder: (id: number) => apiClient.get<BinanceRechargeOrder>(`/binance-recharge/orders/${id}`),
  refreshOrder: (id: number) => apiClient.post<BinanceRechargeOrder>(`/binance-recharge/orders/${id}/refresh`),
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
    apiClient.post<{ matched: number; review: number }>(`/admin/binance-recharge/accounts/${id}/check`),
  getOrders: (params: BinanceRechargeOrderFilters) =>
    apiClient.get<BinanceRechargeOrders>('/admin/binance-recharge/orders', { params }),
  refreshOrder: (id: number) =>
    apiClient.post<BinanceRechargeOrder>(`/admin/binance-recharge/orders/${id}/refresh`),
  getStats: (params: { account_id?: number; page?: number; page_size?: number }) =>
    apiClient.get<BinanceRechargeStats>('/admin/binance-recharge/stats', { params }),
}
