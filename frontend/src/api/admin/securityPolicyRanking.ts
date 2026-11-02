import { apiClient } from '@/api/client'
import type {
  SecurityPolicyDeductionPreview,
  SecurityPolicyRanking,
  SecurityPolicyRankingRange,
  SecurityPolicyRankingSettings,
  SecurityPolicyTriggerRecords,
} from '@/types/securityPolicyRanking'

const base = '/admin/security-policy-ranking'

export const securityPolicyRankingAPI = {
  getRanking: (params: SecurityPolicyRankingRange & { page: number; page_size: number }, signal?: AbortSignal) =>
    apiClient.get<SecurityPolicyRanking>(base, { params, signal }),
  getPublicRanking: (params: SecurityPolicyRankingRange & { page: number; page_size: number }, signal?: AbortSignal) =>
    apiClient.get<SecurityPolicyRanking>('/security-policy-ranking', { params, signal }),
  getSettings: () => apiClient.get<SecurityPolicyRankingSettings>(`${base}/settings`),
  saveSettings: (settings: SecurityPolicyRankingSettings) => apiClient.put<SecurityPolicyRankingSettings>(`${base}/settings`, settings),
  getRecords: (userID: number, params: SecurityPolicyRankingRange & { page: number; page_size: number }, signal?: AbortSignal) =>
    apiClient.get<SecurityPolicyTriggerRecords>(`${base}/users/${userID}/records`, { params, signal }),
  preview: (userID: number, range: SecurityPolicyRankingRange, signal?: AbortSignal) =>
    apiClient.post<SecurityPolicyDeductionPreview>(`${base}/users/${userID}/preview`, range, { signal }),
  deduct: (userID: number, previewID: string) =>
    apiClient.post<SecurityPolicyDeductionPreview>(`${base}/users/${userID}/deduct`, { preview_id: previewID }),
}
