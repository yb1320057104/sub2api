export type BinanceRechargeStatus = 'PENDING' | 'COMPLETED' | 'EXPIRED' | 'REVIEW'

export interface BinanceRechargeAccountOption {
  id: number
  name: string
  recipient_uid: string
  order_ttl_minutes: number
}

export interface BinanceRechargeConfig {
  enabled: boolean
  min_amount: number
  max_amount: number
  decimal_places: number
  poll_interval_seconds: number
  accounts: BinanceRechargeAccountOption[]
}

export interface BinanceRechargeAccount extends BinanceRechargeAccountOption {
  enabled: boolean
  api_key_configured: boolean
  api_secret_configured: boolean
  has_orders: boolean
  last_checked_at?: string
  last_error?: string
  created_at: string
  updated_at: string
}

export interface BinanceRechargeAccountInput {
  name: string
  recipient_uid: string
  api_key?: string
  api_secret?: string
  enabled: boolean
  order_ttl_minutes: number
}

export interface BinanceRechargeOrder {
  id: number
  order_no: string
  user_id: number
  user_email?: string
  account_id: number
  account_name: string
  recipient_uid: string
  requested_amount: number
  /** Exact decimal strings from the server; never round or convert for payment. */
  pay_amount: string
  credited_amount: string
  status: BinanceRechargeStatus
  transaction_id?: string
  created_at: string
  expires_at: string
  paid_at?: string
  completed_at?: string
  review_reason?: string
}

export interface BinanceRechargeOrders {
  items: BinanceRechargeOrder[]
  total: number
  page: number
  page_size: number
  pages: number
}

export interface BinanceRechargeOrderFilters {
  page: number
  page_size: number
  status?: BinanceRechargeStatus | ''
  account_id?: number
  keyword?: string
}

export interface BinanceRechargeStats {
  total_amount: string
  total_orders: number
  items: { user_id: number; user_email?: string; total_amount: string; total_orders: number }[]
  total: number
  page: number
  page_size: number
  pages: number
  account_options: BinanceRechargeAccountOption[]
}
