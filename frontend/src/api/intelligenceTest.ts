import { apiClient } from './client'

export type IntelligenceTestMode = 'svg' | 'candy'

export interface IntelligenceTestGroup {
  id: number
  name: string
  platform: string
  key_configured: boolean
  selected: boolean
}

export interface IntelligenceTestGroupOverride {
  group_id: number
  model: string
  effort: string
}

export interface IntelligenceTestTaskConfig {
  enabled: boolean
  prompt: string
  model: string
  effort: string
  interval_minutes: number
  expected_answer?: number
  group_overrides: IntelligenceTestGroupOverride[]
}

export interface IntelligenceTestConfig {
  enabled: boolean
  svg: IntelligenceTestTaskConfig
  candy: IntelligenceTestTaskConfig
}

export interface IntelligenceTestModelCatalog {
  models: string[]
  excluded_models: string[]
}

export interface IntelligenceTestResult {
  id: number
  group_id: number
  mode: IntelligenceTestMode
  status: 'running' | 'completed' | 'failed'
  empty_pool?: boolean
  model?: string | null
  effort?: string | null
  passed?: boolean
  has_artifact?: boolean
  started_at: string
  finished_at?: string
  duration_ms?: number
}

export interface IntelligenceTestDetails {
  prompt: string
  answer: string
  error_code?: string
  error_detail?: string
}

export const intelligenceTestAPI = {
  async status() {
    return (await apiClient.get<{ enabled: boolean }>('/intelligence-test/status')).data
  },
  async groups() {
    return (await apiClient.get<IntelligenceTestGroup[]>('/intelligence-test/groups')).data
  },
  async results(groupID: number, mode: IntelligenceTestMode, signal?: AbortSignal) {
    return (await apiClient.get<IntelligenceTestResult[]>('/intelligence-test/results', {
      params: { group_id: groupID, mode }, signal,
    })).data
  },
  async artifact(groupID: number, id: number, signal?: AbortSignal) {
    return (await apiClient.get<{ artifact: string }>('/intelligence-test/artifact', {
      params: { group_id: groupID, id }, signal,
    })).data
  },
  async details(groupID: number, id: number, signal?: AbortSignal) {
    return (await apiClient.get<IntelligenceTestDetails>('/intelligence-test/details', {
      params: { group_id: groupID, id }, signal,
    })).data
  },
  async config(signal?: AbortSignal) {
    return (await apiClient.get<IntelligenceTestConfig>('/admin/intelligence-test/config', { signal })).data
  },
  async models(signal?: AbortSignal) {
    return (await apiClient.get<IntelligenceTestModelCatalog>('/admin/intelligence-test/models', { signal })).data
  },
  async saveConfig(config: IntelligenceTestConfig) {
    return (await apiClient.put<IntelligenceTestConfig>('/admin/intelligence-test/config', config)).data
  },
  async syncKeys(groupIDs: number[]) {
    return (await apiClient.post<{
      created: number
      updated: number
      deleted: number
      failed: number
      groups: { group_id: number; group_name: string; configured: boolean; error?: string }[]
    }>(
      '/admin/intelligence-test/keys/sync', { group_ids: [...groupIDs] },
    )).data
  },
}
