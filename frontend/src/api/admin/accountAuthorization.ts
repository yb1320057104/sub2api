import { apiClient } from '../client'

export interface AuthorizationIntelligenceModel { model: string; effort: string; enabled: boolean }
export interface AuthorizationIntelligenceResult {
  model: string; effort: string; status: 'running' | 'completed' | 'failed' | 'cancelled'
  passed: boolean | null; answer: number | null; error_message?: string; duration_ms: number
}

export interface AuthorizationConfig {
 account_proxy_mode?: 'direct' | 'managed'; max_accounts_per_proxy?: number;
  intelligence_models?: AuthorizationIntelligenceModel[]
  pass_group_ids: number[]; fail_group_ids: number[]
  intelligence_model: string; intelligence_effort: string
  inspection_enabled: boolean; reauth_enabled: boolean
  inspection_concurrency: number; authorization_concurrency: number
  account_concurrency: number; account_priority: number; group_ids: number[]
  reauth_status_codes: number[]; revision: number
  next_inspection_at: string | null; next_reauth_at: string | null
  inspection_cron: string; reauth_cron: string; browser_available: boolean
  encryption_key_configured: boolean
}
export interface AuthorizationSubmission {
 account_proxy_mode?: 'direct' | 'managed'; max_accounts_per_proxy?: number;
  pass_group_ids: number[]
  accounts: string; authorization_concurrency: number
  account_concurrency: number; account_priority: number; group_ids: number[]
}
export interface AuthorizationSecret { email: string; password: string; twofa: string }
export type AuthorizationList = 'jobs' | 'history' | 'proxies' | 'accounts'
export interface AuthorizationRecord {
  retry_of_job_id?: number; immediate_authorization?: boolean
  authorization_proxy_id?: number; authorization_proxy_scanned?: number
  authorization_proxy_passed?: number; authorization_proxy_limit?: number
  intelligence_results?: AuthorizationIntelligenceResult[]
  intelligence_passed?: boolean | null; intelligence_answer?: number | null; authorization_completed?: boolean
  id: number; email: string; name?: string; status: string; message?: string; revision?: string
  account_id?: number; account_status?: string; source?: string; stage?: string
  groups?: string[]; has_history?: boolean; has_twofa?: boolean
  endpoint?: string; job_message?: string; job_id?: number
  checked_at?: string; authorized_at?: string; last_checked_at?: string
  created_at?: string; finished_at?: string; http_status?: number
}
export interface AuthorizationPage { items: AuthorizationRecord[]; total: number; page: number; page_size: number }
export interface AuthorizationActionResult { job_id: number; batch_id: string }
const root = '/admin/account-authorization'
export const accountAuthorizationAPI = {
  async config(signal?: AbortSignal) { return (await apiClient.get<AuthorizationConfig>(root + '/config', { signal })).data },
  async saveConfig(value: AuthorizationConfig) { return (await apiClient.put<AuthorizationConfig>(root + '/config', value)).data },
  async submit(value: AuthorizationSubmission) { return (await apiClient.post<{ batch_id: string }>(root + '/jobs', value)).data },
  async list(kind: AuthorizationList, params: { page: number; page_size: number; search: string; status: string }, signal?: AbortSignal) {
    return (await apiClient.get<AuthorizationPage>(kind === 'accounts' ? '/admin/radar/authorization' : root + '/' + kind, { params, signal })).data
  },
  async addProxies(proxies: string) { await apiClient.post(root + '/proxies', { proxies }) },
  async deleteProxies(ids: number[]) { return (await apiClient.post<{ deleted: number }>(root + '/proxies/batch-delete', { ids })).data },
  async disableProxy(id: number) { await apiClient.delete(root + '/proxies/' + id) },
  async checkProxy(id: number) { await apiClient.post(root + '/proxies/' + id + '/check', {}, { timeout: 90000 }) },
  async cancel(id: number) { await apiClient.post(root + '/jobs/' + id + '/cancel') },
  async retryJob(id: number) { return (await apiClient.post<AuthorizationActionResult>(root + '/jobs/' + id + '/retry')).data },
  async authorizeNow(id: number) { return (await apiClient.post<AuthorizationActionResult>(root + '/accounts/' + id + '/authorize')).data },
  async reveal(id: number) { return (await apiClient.post<AuthorizationSecret>(root + '/history/' + id + '/reveal')).data },
  async updateCredential(id: number, value: { password: string; twofa: string; revision: string }) { await apiClient.put(root + '/history/' + id, value) },
  async deleteCredential(id: number, revision: string) { await apiClient.delete(root + '/history/' + id, { params: { revision } }) },
  async supplement(id: number, value: AuthorizationSecret) { await apiClient.post(root + '/accounts/' + id + '/credentials', value) }
}
