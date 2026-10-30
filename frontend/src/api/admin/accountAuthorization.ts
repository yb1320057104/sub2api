import { apiClient } from '../client'

export interface AuthorizationConfig {
  inspection_enabled: boolean; reauth_enabled: boolean
  inspection_concurrency: number; authorization_concurrency: number
  account_concurrency: number; account_priority: number; group_ids: number[]
  reauth_status_codes: number[]; revision: number
  next_inspection_at: string | null; next_reauth_at: string | null
  inspection_cron: string; reauth_cron: string; browser_available: boolean
  encryption_key_configured: boolean
}
export interface AuthorizationSubmission {
  accounts: string; authorization_concurrency: number
  account_concurrency: number; account_priority: number; group_ids: number[]
}
export interface AuthorizationSecret { email: string; password: string; twofa: string }
export type AuthorizationList = 'jobs' | 'history' | 'proxies' | 'accounts'
export interface AuthorizationRecord {
  id: number; email: string; name?: string; status: string; message?: string
  account_id?: number; account_status?: string; source?: string; stage?: string
  groups?: string[]; has_history?: boolean; has_twofa?: boolean
  endpoint?: string; job_message?: string; job_id?: number
  checked_at?: string; authorized_at?: string; last_checked_at?: string
  created_at?: string; finished_at?: string; http_status?: number
}
export interface AuthorizationPage { items: AuthorizationRecord[]; total: number; page: number; page_size: number }
const root = '/admin/account-authorization'
export const accountAuthorizationAPI = {
  async config(signal?: AbortSignal) { return (await apiClient.get<AuthorizationConfig>(root + '/config', { signal })).data },
  async saveConfig(value: AuthorizationConfig) { return (await apiClient.put<AuthorizationConfig>(root + '/config', value)).data },
  async submit(value: AuthorizationSubmission) { return (await apiClient.post<{ batch_id: string }>(root + '/jobs', value)).data },
  async list(kind: AuthorizationList, params: { page: number; page_size: number; search: string; status: string }, signal?: AbortSignal) {
    return (await apiClient.get<AuthorizationPage>(kind === 'accounts' ? '/admin/radar/authorization' : root + '/' + kind, { params, signal })).data
  },
  async addProxies(proxies: string) { await apiClient.post(root + '/proxies', { proxies }) },
  async disableProxy(id: number) { await apiClient.delete(root + '/proxies/' + id) },
  async checkProxy(id: number) { await apiClient.post(root + '/proxies/' + id + '/check', {}, { timeout: 90000 }) },
  async cancel(id: number) { await apiClient.post(root + '/jobs/' + id + '/cancel') },
  async reveal(id: number) { return (await apiClient.post<AuthorizationSecret>(root + '/history/' + id + '/reveal')).data },
  async supplement(id: number, value: AuthorizationSecret) { await apiClient.post(root + '/accounts/' + id + '/credentials', value) }
}
