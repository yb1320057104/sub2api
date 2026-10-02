export interface SecurityPolicyRankingRange {
  start_date: string
  end_date: string
  timezone: string
}

export interface SecurityPolicyRankingUser {
  rank: number
  user_id?: number
  display_name?: string
  email?: string
  username?: string
  balance?: string
  trigger_count: number
  deducted_count?: number
  pending_count?: number
  /** Exact decimal amount calculated by the server. */
  pending_amount?: string
  deleted?: boolean
  status?: string
  auto_disable_count?: number
}

export interface SecurityPolicyRanking extends SecurityPolicyRankingRange {
  items: SecurityPolicyRankingUser[]
  total: number
  total_triggers: number
  total_pending?: number
  multiplier?: string
  generated_at: string
}

export interface SecurityPolicyRankingSettings {
  multiplier: string
  auto_disable_enabled: boolean
  auto_disable_threshold: number
  reset_on_manual_enable: boolean
}

export interface SecurityPolicyDeductionPreview extends SecurityPolicyRankingRange {
  /** Persistent preview identifier also used for idempotent retries. */
  id: string
  user_id: number
  email: string
  username: string
  trigger_count: number
  multiplier: string
  amount: string
  balance: string
  expires_at: string
  status: 'pending' | 'completed'
  balance_after?: string
  completed_at?: string
}
