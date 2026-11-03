export type BinanceRechargeStatus = 'PENDING' | 'COMPLETED' | 'EXPIRED' | 'REVIEW'
export type BinanceRechargeCheckState = 'ready' | 'running' | 'interval_wait' | 'retry_wait' | 'rate_limited'

export interface BinanceRechargeAccountOption {
  id: number
  name: string
  recipient_uid: string
  order_ttl_minutes: number
  credit_multiplier: string
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
  proxy_configured: boolean
  proxy_preflight: boolean
  backup_proxy_configured?: boolean
  backup_proxy_preflight?: boolean
  query_status?: {
    route?: 'primary' | 'backup'
    attempt?: number
    state?: 'running' | 'waiting' | 'succeeded' | 'failed'
    retry_at?: string
    updated_at?: string
  }
  has_orders: boolean
  check_state: BinanceRechargeCheckState
  retry_after_seconds: number
  check_available_at?: string
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
  /** Write-only proxy URL. Blank preserves the saved encrypted value. */
  proxy_url?: string
  clear_proxy?: boolean
  proxy_preflight?: boolean
  backup_proxy_url?: string
  clear_backup_proxy?: boolean
  backup_proxy_preflight?: boolean
  enabled: boolean
  order_ttl_minutes: number
  credit_multiplier?: string
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
  /** Rate and expected balance credit captured when this order was created. */
  credit_multiplier: string
  system_amount: string
  /** Actual balance credit; zero until the order completes. */
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
  /** Received USDT and credited system balance are separate totals. */
  total_amount: string
  total_credited_amount: string
  total_orders: number
  items: { user_id: number; user_email?: string; total_amount: string; total_credited_amount: string; total_orders: number }[]
  total: number
  page: number
  page_size: number
  pages: number
  account_options: BinanceRechargeAccountOption[]
}

export interface BinanceBillField {
  key: string
  type: 'string' | 'number' | 'boolean' | 'null' | 'object' | 'array'
  value: string
  children?: BinanceBillField[]
}

export interface BinanceLiveBill {
  id: string
  channel: 'pay'
  kind: string
  currency: string
  /** Original Binance decimal text, including sign and all decimal places. */
  amount: string
  direction: 'incoming' | 'outgoing' | 'self' | 'unknown'
  status: 'recorded'
  time: string
  payer_uid?: string
  receiver_uid?: string
  fields: BinanceBillField[]
}

export interface BinanceLiveBills {
  account_id: number
  account_name: string
  recipient_uid: string
  start_time: string
  end_time: string
  limit: number
  possibly_more: boolean
  /** Number returned by this query; not an all-time total. */
  total: number
  items: BinanceLiveBill[]
}
