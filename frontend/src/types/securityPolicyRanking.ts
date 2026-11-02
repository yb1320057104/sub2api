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
  counted_count?: number
  skipped_count?: number
  unknown_count?: number
  audit_block_count?: number
}

export interface SecurityPolicyRanking extends SecurityPolicyRankingRange {
  items: SecurityPolicyRankingUser[]
  total: number
  total_triggers: number
  total_pending?: number
  total_counted?: number
  total_skipped?: number
  total_unknown?: number
  total_audit_blocks?: number
  multiplier?: string
  generated_at: string
}

export interface SecurityPolicyGroupRule {
  group_id: number
  multiplier: string
  auto_deduct_enabled: boolean
}

export interface SecurityPolicyRankingSettings {
  multiplier: string
  auto_disable_enabled: boolean
  auto_disable_threshold: number
  reset_on_manual_enable: boolean
  group_ids: number[]
  user_allowlist_ids?: number[]
  group_rules?: SecurityPolicyGroupRule[]
  settings_revision?: number
}

export interface SecurityPolicyTriggerRecord {
  usage_id: number
  record_key?: string
  source?: 'upstream_policy' | 'content_audit' | 'prompt_audit' | 'combined_audit'
  audit_id?: number | null
  occurred_at: string
  group_id: number | null
  group_name: string
  request_id: string
  model: string
  count_status: 'counted' | 'skipped' | 'unknown'
  count_reason: string
  settings_revision: number | null
  evaluated_at: string | null
  scope_matched?: boolean | null
  auto_charge_status?: 'deducted' | 'zero_cost' | 'insufficient_balance' | 'skipped' | 'previously_charged' | 'failed' | 'historical_unknown'
  auto_charge_reason?: string
  actual_cost?: string | null
  audit_multiplier?: string | null
  auto_charge_amount?: string | null
}

export interface SecurityPolicyTriggerRecords extends SecurityPolicyRankingRange {
  items: SecurityPolicyTriggerRecord[]
  total: number
  page: number
  page_size: number
  pages: number
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
