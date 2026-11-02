import { apiClient } from '../client'
import type { Account } from '@/types'

export interface RadarModel { model: string; effort: string; enabled: boolean }
export interface RadarGroupRule { source_group_id: number; pass_group_ids: number[]; fail_group_id: number }
export interface RadarModelResult {
  id: number; model: string; mapped_model: string; effort: string
  status: 'pending' | 'running' | 'completed' | 'failed' | 'cancelled'
  passed: boolean | null; answer: number | null; response_text?: string; error_message: string
  egress_ip?: string; egress_checked_at?: string | null; egress_error?: string
  started_at: string | null; finished_at: string | null; duration_ms: number | null
}

export interface AccountCandyConfig {
  enabled: boolean
  interval_minutes: number
  models: RadarModel[]
  group_rules: RadarGroupRule[]
  generation: string
  target_group_id: number | null
}

export interface AccountCandyResult {
  id: number
  account_id: number
  status: 'running' | 'completed' | 'partial_failed' | 'failed' | 'cancelled'
  passed: boolean | null
  answer: number | null
  model: string
  effort: string
  started_at: string
  finished_at: string | null
  duration_ms: number | null
  moved_to_group_id: number | null
  moved_to_group_ids?: number[]
  error_message: string
  move_error: string
  source: 'manual' | 'scheduled'
  original_group_ids: number[]
  origin?: 'radar' | 'legacy' | 'authorization'
  started_at_estimated?: boolean
  generation?: string
  verdict?: 'unknown' | 'pass' | 'fail'
  model_results?: RadarModelResult[]
  failed_models?: RadarModelResult[]
  egress_ip?: string
  egress_mode?: 'proxy' | 'direct'
  egress_checked_at?: string | null
  egress_error?: string
}

export interface AccountCandyDetails extends AccountCandyResult {
  prompt: string
  response_text?: string
}

export function getCandyMovedGroupIDs(result?: AccountCandyResult | null): number[] {
  if (result?.moved_to_group_ids?.length) return result.moved_to_group_ids
  return result?.moved_to_group_id != null ? [result.moved_to_group_id] : []
}

export interface RadarObservation {
  account: Account
  run: AccountCandyResult | null
  verdict: 'unknown' | 'pass' | 'fail'
  next_run_at: string | null
  schedule_reason: string
}

export interface RadarAccountPage {
  items: RadarObservation[]; total: number; page: number; page_size: number
  generation: string; interval_minutes: number
}

export const accountCandyAPI = {
  async models(signal?: AbortSignal) {
    return (await apiClient.get<{ models: string[] }>('/admin/radar/account-candy/models', { signal })).data.models
  },
  async config(signal?: AbortSignal) {
    return (await apiClient.get<AccountCandyConfig>('/admin/radar/account-candy/config', { signal })).data
  },
  async saveConfig(config: AccountCandyConfig) {
    return (await apiClient.put<AccountCandyConfig>('/admin/radar/account-candy/config', config)).data
  },
  async latest(accountIDs: number[], signal?: AbortSignal) {
    if (!accountIDs.length) return []
    return (await apiClient.get<AccountCandyResult[]>('/admin/radar/account-candy/results', {
      params: { account_ids: accountIDs.join(',') }, signal,
    })).data
  },
  async start(accountID: number) {
    return (await apiClient.post<AccountCandyResult>(`/admin/radar/accounts/${accountID}/candy-runs`)).data
  },
  async details(accountID: number, resultID: number, signal?: AbortSignal, origin: 'radar' | 'legacy' | 'authorization' = 'radar') {
    return (await apiClient.get<AccountCandyDetails>(
      origin === 'legacy' ? `/admin/accounts/${accountID}/candy-test/results/${resultID}` : `/admin/radar/accounts/${accountID}/candy-runs/${resultID}`, { signal, params: origin === 'authorization' ? { origin } : undefined },
    )).data
  },
  async accounts(params: { page: number; page_size: number; filter: string; search: string }, signal?: AbortSignal) {
    return (await apiClient.get<RadarAccountPage>('/admin/radar/accounts', { params: { ...params, verdict: params.filter }, signal })).data
  },
  async history(accountID: number, signal?: AbortSignal) {
    return (await apiClient.get<AccountCandyResult[]>(`/admin/radar/accounts/${accountID}/candy-runs`, { signal })).data
  },
}
