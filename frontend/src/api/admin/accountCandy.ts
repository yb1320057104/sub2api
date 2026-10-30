import { apiClient } from '../client'

export interface AccountCandyConfig {
  enabled: boolean
  interval_minutes: number
  model: string
  effort: string
  target_group_id: number | null
}

export interface AccountCandyResult {
  id: number
  account_id: number
  status: 'running' | 'completed' | 'failed' | 'cancelled'
  passed: boolean | null
  answer: number | null
  model: string
  effort: string
  started_at: string
  finished_at: string | null
  duration_ms: number | null
  moved_to_group_id: number | null
  error_message: string
  move_error: string
  source: 'manual' | 'scheduled'
  original_group_ids: number[]
}

export interface AccountCandyDetails extends AccountCandyResult {
  prompt: string
  response_text?: string
}

export const accountCandyAPI = {
  async config(signal?: AbortSignal) {
    return (await apiClient.get<AccountCandyConfig>('/admin/account-candy-test/config', { signal })).data
  },
  async saveConfig(config: AccountCandyConfig) {
    return (await apiClient.put<AccountCandyConfig>('/admin/account-candy-test/config', config)).data
  },
  async latest(accountIDs: number[], signal?: AbortSignal) {
    if (!accountIDs.length) return []
    return (await apiClient.get<AccountCandyResult[]>('/admin/account-candy-test/results', {
      params: { account_ids: accountIDs.join(',') }, signal,
    })).data
  },
  async start(accountID: number) {
    return (await apiClient.post<AccountCandyResult>(`/admin/accounts/${accountID}/candy-test`)).data
  },
  async details(accountID: number, resultID: number, signal?: AbortSignal) {
    return (await apiClient.get<AccountCandyDetails>(
      `/admin/accounts/${accountID}/candy-test/results/${resultID}`, { signal },
    )).data
  },
}
