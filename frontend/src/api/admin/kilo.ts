import { apiClient } from '../client'
import type { KiloAccountView } from '@/types'

export interface KiloRoutingOptions {
  proxy_group_id: number
  proxies_per_account: number
  proxy_concurrency: number
  account_concurrency: number
  priority: number
  auto_release_statuses: number[]
}

export interface KiloRoutingInput extends Partial<KiloRoutingOptions> {
  login_proxy_id?: number
}

export type KiloWorkspaceMode = 'auto' | 'personal' | 'business'
export type KiloBindingStrategy = 'new_identity' | 'prefer_idle'
export type KiloIdentityMode = 'unknown' | 'gpt_coupled' | 'independent'
export type KiloIdentityStatus = 'active' | 'unknown' | 'banned' | 'retired'
export type KiloBindingState = 'empty' | 'bound' | 'recovering' | 'unbinding' | 'binding' | 'blocked'

export interface KiloPrepared {
  account_view?: KiloAccountView
  identity_status?: KiloIdentityStatus
  identity_mode: KiloIdentityMode
  identity_source_account_id?: number | null
  identity_checked_at?: string | null
  binding_state?: KiloBindingState
  binding_revision?: number
  reusable?: boolean
  recovery_task_id?: number
  binding_error?: string
  id: number
  name: string
  email: string
  auth_method: 'api_key' | 'device'
  organization_id: string
  status: 'ready' | 'binding' | 'bound' | 'disabled'
  account_id: number | null
  source_account_id: number | null
  login_proxy_id?: number | null
  login_proxy_name?: string
  source_name?: string
  gateway_name?: string
  routing_options?: KiloRoutingOptions
  has_credential: boolean
  last_error: string
  created_at: string
}

export type KiloSubscriptionRecoveryAction = 'reconcile_subscription' | 'reauthorize_subscription'
export type KiloJobRecoveryAction = 'retry' | KiloSubscriptionRecoveryAction

export interface KiloSubscriptionRecoveryInput {
  expected_revision: number
}

export interface KiloJob {
  revision: number
  recovery_actions: KiloJobRecoveryAction[]
  id: number
  batch_id: string
  prepared_id: number | null
  prepared_name: string
  job_kind?: 'activate_prepared' | 'onboard_source' | 'unbind_source'
  workspace_mode?: KiloWorkspaceMode
  organization_id?: string
  login_proxy_id?: number | null
  error_code?: string
  failed_stage?: string
  next_action?: string
  next_attempt_at?: string | null
  credential_ready?: boolean
  subscription_connected?: boolean
  quota_oauth_ready?: boolean
  source_account_id: number | null
  source_name: string
  account_id: number | null
  stage: string
  status: string
  last_error: string
  verification_url?: string
  user_code?: string
  created_at: string
  finished_at: string | null
}

export interface KiloOverview {
  prepared: KiloPrepared[]
  jobs: KiloJob[]
  encryption_ready: boolean
}

export interface KiloImportItem {
  name: string
  api_key: string
  email?: string
  organization_id?: string
}

export interface KiloImportResult {
  items: Array<{ line: number; id?: number; status: 'imported' | 'duplicate' | 'failed'; message?: string }>
  imported: number
  duplicates: number
  failed: number
}

export interface KiloBatchInput {
  request_id: string
  prepared_ids: number[]
  source_account_ids: number[]
  byok_enabled_confirmed: boolean
  group_ids: number[]
  proxy_group_id: number
  proxies_per_account: number
  proxy_concurrency: number
  account_concurrency: number
  priority: number
  auto_release_statuses?: number[]
  login_proxy_ids?: Record<number, number>
}

export interface KiloOnboardingItemInput {
  source_account_id: number
  name: string
  login_proxy_id: number
  workspace_mode?: KiloWorkspaceMode
  organization_id: string
  verified_model_ids: string[]
}

export interface KiloOnboardingBatchInput {
  request_id: string
  items: KiloOnboardingItemInput[]
  group_ids: number[]
  routing: KiloRoutingOptions
  activate_when_ready: boolean
}

export interface KiloOnboardingImportItem {
  email: string
  password: string
  totp_secret: string
  name?: string
  login_proxy_id: number
  workspace_mode?: KiloWorkspaceMode
  organization_id?: string
  verified_model_ids?: string[]
  prepared_id?: number
}

export interface KiloOnboardingImportInput {
  request_id: string
  binding_strategy: KiloBindingStrategy
  items: KiloOnboardingImportItem[]
  group_ids: number[]
  routing: KiloRoutingOptions
  activate_when_ready: boolean
}

export interface KiloOnboardingBatchResult {
  batch_id: string
  items: Array<{ line: number; source_account_id: number; job_id: number; status: string }>
}

export interface KiloOnboardingBatch {
  batch_id: string
  items: KiloJob[]
}

export interface KiloCandidate {
  id: number
  name: string
  email: string
  status: string
  has_password?: boolean
  has_totp?: boolean
  login_proxy_id?: number | null
  onboarding_reason?: string
}
export interface KiloGroup { id: number; name: string }
export interface KiloProxyGroup { id: number; name: string; active_proxy_count: number }
export interface KiloProxy {
  id: number
  name: string
  status: string
  owned: boolean
  owner_account_id: number | null
  owner_account_name: string
  owner_deleted: boolean
}
export interface KiloOrganization { id: string; name: string }

export interface KiloProxyCheck {
  target: 'openai' | 'kilo_api' | 'kilo_login'
  reachable: boolean
  status?: number
  latency_ms: number
  challenge?: boolean
  error?: string
}

export interface KiloProxyPreflight {
  proxy_id: number
  reachable: boolean
  checks: KiloProxyCheck[]
}

export interface KiloDeviceStatus {
  id: string
  status: 'pending' | 'approved' | 'denied' | 'expired' | 'cancelled'
  verification_url?: string
  user_code?: string
  expires_at: string
  prepared_id?: number
  email?: string
}

export interface KiloEgress {
  id: number
  account_id: number
  proxy_id: number
  egress_ip: string
  state: string
  verified_at: string | null
  max_concurrency: number
  in_flight: number
  last_error: string
}

const base = '/admin/kilo'

export async function getOverview(signal?: AbortSignal): Promise<KiloOverview> {
  return (await apiClient.get<KiloOverview>(`${base}/overview`, { signal })).data
}
export async function importPrepared(input: { items: KiloImportItem[]; request_id: string }, signal?: AbortSignal): Promise<KiloImportResult> {
  return (await apiClient.post<KiloImportResult>(`${base}/prepared/import`, input, { signal, timeout: 65000 })).data
}
export async function createBatch(input: KiloBatchInput, signal?: AbortSignal): Promise<{ batch_id: string }> {
  return (await apiClient.post<{ batch_id: string }>(`${base}/batches`, input, { signal })).data
}
export async function createOnboardingBatch(input: KiloOnboardingBatchInput, signal?: AbortSignal): Promise<KiloOnboardingBatchResult> {
  return (await apiClient.post<KiloOnboardingBatchResult>(`${base}/onboarding/batches`, input, { signal, timeout: 35000 })).data
}
export async function importOnboarding(input: KiloOnboardingImportInput, signal?: AbortSignal): Promise<KiloOnboardingBatchResult> {
  return (await apiClient.post<KiloOnboardingBatchResult>(`${base}/onboarding/import`, input, { signal, timeout: 35000 })).data
}
export async function unbindPrepared(id: number, requestId: string, signal?: AbortSignal): Promise<{ status: 'queued' }> {
  return (await apiClient.post<{ status: 'queued' }>(`${base}/prepared/${id}/unbind`, { request_id: requestId }, { signal, timeout: 35000 })).data
}
export async function getOnboardingBatch(id: string, signal?: AbortSignal): Promise<KiloOnboardingBatch> {
  return (await apiClient.get<KiloOnboardingBatch>(`${base}/onboarding/batches/${encodeURIComponent(id)}`, { signal })).data
}
export async function getCandidates(signal?: AbortSignal): Promise<KiloCandidate[]> {
  return (await apiClient.get<{ items: KiloCandidate[] }>(`${base}/accounts/candidates`, { signal })).data.items
}
export async function getGroups(signal?: AbortSignal): Promise<KiloGroup[]> {
  return (await apiClient.get<{ items: KiloGroup[] }>(`${base}/groups`, { signal })).data.items
}
export async function getProxyGroups(signal?: AbortSignal): Promise<KiloProxyGroup[]> {
  return (await apiClient.get<{ items: KiloProxyGroup[] }>(`${base}/proxy-groups`, { signal })).data.items
}
export async function startDevice(input: { name: string; proxy_id: number }, signal?: AbortSignal): Promise<KiloDeviceStatus> {
  return (await apiClient.post<KiloDeviceStatus>(`${base}/device`, input, { signal, timeout: 95000 })).data
}
export async function getProxies(signal?: AbortSignal): Promise<KiloProxy[]> {
  return (await apiClient.get<{ items: KiloProxy[] }>(`${base}/proxies`, { signal })).data.items
}
export async function preflightProxy(id: number, signal?: AbortSignal): Promise<KiloProxyPreflight> {
  return (await apiClient.post<KiloProxyPreflight>(`${base}/proxies/${id}/preflight`, {}, { signal, timeout: 65000 })).data
}
export async function cancelDevice(id: string, signal?: AbortSignal): Promise<void> {
  await apiClient.delete(`${base}/device/${encodeURIComponent(id)}`, { signal })
}
export async function pollDevice(id: string, signal?: AbortSignal): Promise<KiloDeviceStatus> {
  return (await apiClient.post<KiloDeviceStatus>(`${base}/device/${encodeURIComponent(id)}/poll`, {}, { signal })).data
}
export async function getOrganizations(id: number, signal?: AbortSignal): Promise<KiloOrganization[]> {
  return (await apiClient.post<{ items: KiloOrganization[] }>(`${base}/prepared/${id}/organizations`, {}, { signal })).data.items
}
export async function setOrganization(id: number, organizationId: string, signal?: AbortSignal): Promise<void> {
  await apiClient.put(`${base}/prepared/${id}/organization`, { organization_id: organizationId }, { signal })
}
export async function setRouting(id: number, input: KiloRoutingInput, signal?: AbortSignal): Promise<void> {
  await apiClient.put(`${base}/prepared/${id}/routing`, input, { signal })
}
export interface KiloModelScopeResult {
  account_id: number
  models: string[]
  source: 'kilo_authenticated_byok_catalog'
  organization_id: string
  verified_at: string
  applied: boolean
}
export async function refreshModels(id: number, signal?: AbortSignal): Promise<KiloModelScopeResult> {
  return (await apiClient.post<KiloModelScopeResult>(`${base}/prepared/${id}/models/refresh`, {}, { signal, timeout: 60000 })).data
}
export async function refreshIdentityPolicy(id: number, signal?: AbortSignal): Promise<void> {
  await apiClient.post(`${base}/prepared/${id}/identity/refresh`, {}, { signal, timeout: 60000 })
}
export async function releasePrepared(id: number, signal?: AbortSignal): Promise<void> {
  await apiClient.post(`${base}/prepared/${id}/release`, {}, { signal })
}
export async function retryJob(id: number, signal?: AbortSignal): Promise<void> {
  await apiClient.post(`${base}/jobs/${id}/retry`, {}, { signal })
}
export async function reconcileSubscription(id: number, input: KiloSubscriptionRecoveryInput, signal?: AbortSignal): Promise<void> {
  await apiClient.post(`${base}/jobs/${id}/reconcile-subscription`, input, { signal, timeout: 35000 })
}
export async function reauthorizeSubscription(id: number, input: KiloSubscriptionRecoveryInput, signal?: AbortSignal): Promise<void> {
  await apiClient.post(`${base}/jobs/${id}/reauthorize-subscription`, input, { signal, timeout: 35000 })
}
export async function cancelJob(id: number, signal?: AbortSignal): Promise<void> {
  await apiClient.post(`${base}/jobs/${id}/cancel`, {}, { signal })
}
export async function deleteJob(id: number, signal?: AbortSignal): Promise<void> {
  await apiClient.delete(`${base}/jobs/${id}`, { signal, timeout: 35000 })
}
export async function deletePrepared(id: number, signal?: AbortSignal): Promise<void> {
  await apiClient.delete(`${base}/prepared/${id}`, { signal, timeout: 35000 })
}
export async function changeJobLoginProxy(id: number, proxyId: number, signal?: AbortSignal): Promise<void> {
  await apiClient.post(`${base}/jobs/${id}/login-proxy`, { login_proxy_id: proxyId }, { signal, timeout: 65000 })
}
export async function getEgress(accountId: number, signal?: AbortSignal): Promise<KiloEgress[]> {
  return (await apiClient.get<{ items: KiloEgress[] }>(`${base}/accounts/${accountId}/egress`, { signal })).data.items
}
export async function addEgress(accountId: number, proxyId: number, signal?: AbortSignal): Promise<void> {
  await apiClient.post(`${base}/accounts/${accountId}/egress`, { proxy_id: proxyId }, { signal, timeout: 65000 })
}
export async function releaseEgress(id: number, signal?: AbortSignal): Promise<void> {
  await apiClient.delete(`${base}/egress/${id}`, { signal })
}

export function isKiloRequestCanceled(error: unknown): boolean {
  return !!error && typeof error === 'object' && ('code' in error && error.code === 'ERR_CANCELED' || 'name' in error && error.name === 'AbortError')
}

export interface KiloAPIError {
  status: number
  code?: string | number
  message: string
}

export function isKiloAPIError(error: unknown): error is KiloAPIError {
  if (!error || typeof error !== 'object' || error instanceof Error) return false
  const value = error as Record<string, unknown>
  return typeof value.status === 'number' && typeof value.message === 'string'
}

export function kiloErrorMessage(error: unknown, fallback: string): string {
  if (!isKiloAPIError(error) || ![400, 404, 409, 422, 429, 503].includes(error.status)) return fallback
  return error.message.trim().slice(0, 500) || fallback
}

export function kiloVerificationUrl(value?: string): string | null {
  if (!value) return null
  try {
    const url = new URL(value)
    return url.protocol === 'https:' && !url.username && !url.password ? url.href : null
  } catch {
    return null
  }
}
