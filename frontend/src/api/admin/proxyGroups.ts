import { apiClient } from '../client'

// Only the API's expected business rejections are suitable for inline feedback.
export function proxyGroupErrorMessage(cause: unknown, fallback: string): string {
  if (!cause || typeof cause !== 'object' || cause instanceof Error) return fallback
  const error = cause as { status?: unknown; message?: unknown }
  if (![400, 404, 409, 422].includes(Number(error.status)) || typeof error.message !== 'string') return fallback
  const message = error.message.trim()
  return message && message.length <= 500 ? message : fallback
}

export interface ProxyGroup {
  id: number
  name: string
  description: string
  proxy_ids: number[]
  proxy_count: number
  active_proxy_count: number
  created_at: string
  updated_at: string
}

export interface GroupProxyPolicy {
  group_id: number
  strict_priority: boolean
  proxy_group_id: number | null
}

export interface ProxyGroupAssignmentResult {
  account_count: number
  binding_count: number
  proxy_count: number
}

export interface UpdateGroupProxyPolicyRequest {
  group_ids: number[]
  strict_priority: boolean
  proxy_group_id?: number | null
  proxy_concurrency?: number
  assign_proxies?: boolean
}

export async function listProxyGroups(): Promise<ProxyGroup[]> {
  const { data } = await apiClient.get<ProxyGroup[]>('/admin/proxy-groups')
  return data
}

export async function createProxyGroup(payload: { name: string; description: string }): Promise<ProxyGroup> {
  const { data } = await apiClient.post<ProxyGroup>('/admin/proxy-groups', payload)
  return data
}

export async function updateProxyGroup(id: number, payload: { name: string; description: string }): Promise<ProxyGroup> {
  const { data } = await apiClient.put<ProxyGroup>(`/admin/proxy-groups/${id}`, payload)
  return data
}

export async function deleteProxyGroup(id: number): Promise<void> {
  await apiClient.delete(`/admin/proxy-groups/${id}`)
}

export async function assignProxyGroupMembers(proxyIDs: number[], groupID: number | null): Promise<{ proxy_count: number }> {
  const { data } = await apiClient.post<{ proxy_count: number }>('/admin/proxies/batch-group', {
    proxy_ids: proxyIDs,
    proxy_group_id: groupID
  })
  return data
}

export async function assignAccountProxyGroup(payload: {
  account_ids: number[]
  proxy_group_id: number
  proxy_concurrency: number
}): Promise<ProxyGroupAssignmentResult> {
  const { data } = await apiClient.post<ProxyGroupAssignmentResult>('/admin/accounts/assign-proxy-group', payload)
  return data
}

export async function listGroupProxyPolicies(): Promise<GroupProxyPolicy[]> {
  const { data } = await apiClient.get<GroupProxyPolicy[]>('/admin/groups/proxy-policies')
  return data
}

export async function updateGroupProxyPolicy(payload: UpdateGroupProxyPolicyRequest): Promise<ProxyGroupAssignmentResult & { group_count: number }> {
  const { data } = await apiClient.post<ProxyGroupAssignmentResult & { group_count: number }>('/admin/groups/proxy-policy', payload)
  return data
}
