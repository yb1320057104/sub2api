import { apiClient } from '@/api/client'

export interface RadarEmailAlertConditions {
  quota_below_100: boolean
  qualified: boolean
  not_401: boolean
}

export interface RadarEmailAlertConfig {
  enabled: boolean
  interval_minutes: number
  minimum_count: number
  conditions: RadarEmailAlertConditions
  recipients: string[]
  revision: number
  next_check_at: string | null
}

export interface RadarEmailAlertStatus {
  last_checked_at: string | null
  last_count: number | null
  total_accounts: number | null
  unknown_quota_count: number | null
  last_status: string
  last_error: string
  last_sent_at: string | null
  sent_count: number
  failed_count: number
}

export interface RadarEmailAlertView {
  config: RadarEmailAlertConfig
  status: RadarEmailAlertStatus
  smtp_configured: boolean
}

export const radarEmailAlertAPI = {
  async get(signal?: AbortSignal) {
    return (await apiClient.get<RadarEmailAlertView>('/admin/radar/email-alert', { signal })).data
  },
  async save(config: RadarEmailAlertConfig) {
    return (await apiClient.put<RadarEmailAlertView>('/admin/radar/email-alert/config', config)).data
  }
}
