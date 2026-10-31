import { apiClient } from '@/api/client'
import type { AccountHealthStats } from '@/types'
export interface AccountHealthConfig { enabled: boolean; interval_minutes: number; window_hours: number; revision: number; next_cycle_at: string }
export interface AccountHealthBatch { batch_no: number; total: number; completed: number; failed: number; skipped: number; pending: number; running: number; scheduled_at: string }
export interface AccountHealthJob { account_id: number; account_name: string; batch_no: number; status: string; scheduled_at: string; started_at: string | null; finished_at: string | null; error_message: string; result: AccountHealthStats | null }
export interface AccountHealthCycle { id: number; status: string; started_at: string; finished_at: string | null; window_start: string; window_end: string; batch_count: number; total_accounts: number; batches: AccountHealthBatch[] }
export interface AccountHealthProgress { config: AccountHealthConfig; cycle: AccountHealthCycle | null; current: AccountHealthJob[]; items: AccountHealthJob[]; total: number; page: number; page_size: number }
export const accountHealthAPI = {
 async progress(page: number, signal?: AbortSignal) { const { data } = await apiClient.get<AccountHealthProgress>('/admin/radar/account-statistics', { params: { page, page_size: 20 }, signal }); return data },
 async save(config: AccountHealthConfig) { const { data } = await apiClient.put<AccountHealthConfig>('/admin/radar/account-statistics/config', config); return data }
}
