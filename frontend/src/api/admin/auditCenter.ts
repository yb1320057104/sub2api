import { apiClient } from '@/api/client'
import type { ContentModerationConfig, UpdateContentModerationConfig } from './riskControl'
import type { PromptAuditConfig, PromptAuditUpdateRequest } from '@/features/prompt-audit/types'
import type { AuditRiskPolicy } from '@/types/auditRiskPolicy'
import type { SecurityPolicyRankingSettings } from '@/types/securityPolicyRanking'

export interface AuditCenterShared {
  group_ids?: number[]
  user_allowlist_ids?: number[]
  risk_policy?: AuditRiskPolicy | null
}
export interface AuditCenterGroupDiagnostic {
  group_id: number
  content_state: string
  prompt_state: string
  ranking_state: string
  charge_state: string
  reasons: string[]
}
export interface AuditCenterConfig {
  revision: string
  content: ContentModerationConfig
  prompt: PromptAuditConfig
  ranking: SecurityPolicyRankingSettings
  differences: string[]
  risk_control_enabled: boolean
  groups: AuditCenterGroupDiagnostic[]
  charge_summary: Array<{ status: string; reason: string; count: number; amount: string }>
  import_summary?: Array<{ status: string; reason: string; count: number; amount: string }>
  observation_summary?: Array<{ status: string; reason: string; source: 'content_observation' | 'prompt_observation'; count: number; amount: string }>
  charge_summary_from?: string
  charge_summary_to?: string
  charge_summary_sample_limit?: number
  charge_summary_sampled?: boolean
}
export interface AuditCenterUpdate {
  expected_revision: string
  risk_control_enabled?: boolean
  shared?: AuditCenterShared
  content?: UpdateContentModerationConfig
  prompt?: PromptAuditUpdateRequest
  ranking?: SecurityPolicyRankingSettings
}
export const auditCenterAPI = {
  async getConfig() {
    return (await apiClient.get<AuditCenterConfig>('/admin/audit-center/config')).data
  },
  async updateConfig(payload: AuditCenterUpdate) {
    return (await apiClient.put<AuditCenterConfig>('/admin/audit-center/config', payload)).data
  },
}
