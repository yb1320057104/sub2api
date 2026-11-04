import { beforeEach, describe, expect, it, vi } from 'vitest'
const client = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: client }))
import { assignAccountProxyGroup, assignProxyGroupMembers, createProxyGroup, deleteProxyGroup, listGroupProxyPolicies, listProxyGroups, proxyGroupErrorMessage, updateGroupProxyPolicy, updateProxyGroup } from '@/api/admin/proxyGroups'
import { list } from '@/api/admin/proxies'

beforeEach(() => { vi.clearAllMocks(); Object.values(client).forEach(method => method.mockResolvedValue({ data: [] })) })
describe('IP groups admin API', () => {
  it('shows structured business failures but hides server and transport details', () => {
    expect(proxyGroupErrorMessage({ status: 409, message: 'IP分组正被严格优先级策略使用' }, 'Failed')).toBe('IP分组正被严格优先级策略使用')
    for (const cause of [{ status: 500, message: 'SQL internal detail' }, { status: 0, message: 'transport detail' }, new Error('stack detail')]) {
      expect(proxyGroupErrorMessage(cause, 'Failed')).toBe('Failed')
    }
  })
  it('uses the separate group catalog and policy endpoints', async () => {
    await listProxyGroups(); await listGroupProxyPolicies()
    expect(client.get).toHaveBeenNthCalledWith(1, '/admin/proxy-groups')
    expect(client.get).toHaveBeenNthCalledWith(2, '/admin/groups/proxy-policies')
  })
  it('creates, updates and deletes the named group only', async () => {
    const payload = { name: 'Static', description: 'Dedicated IPs' }
    await createProxyGroup(payload); await updateProxyGroup(7, payload); await deleteProxyGroup(7)
    expect(client.post).toHaveBeenCalledWith('/admin/proxy-groups', payload)
    expect(client.put).toHaveBeenCalledWith('/admin/proxy-groups/7', payload)
    expect(client.delete).toHaveBeenCalledWith('/admin/proxy-groups/7')
  })
  it('preserves null membership clearing and sends explicit account IDs in one write', async () => {
    await assignProxyGroupMembers([1, 2], null)
    await assignAccountProxyGroup({ account_ids: [11, 12], proxy_group_id: 7, proxy_concurrency: 10 })
    expect(client.post).toHaveBeenNthCalledWith(1, '/admin/proxies/batch-group', { proxy_ids: [1, 2], proxy_group_id: null })
    expect(client.post).toHaveBeenNthCalledWith(2, '/admin/accounts/assign-proxy-group', { account_ids: [11, 12], proxy_group_id: 7, proxy_concurrency: 10 })
  })
  it('disables a policy without clearing the saved group association', async () => {
    await updateGroupProxyPolicy({ group_ids: [3], strict_priority: false, assign_proxies: false })
    expect(client.post).toHaveBeenCalledWith('/admin/groups/proxy-policy', { group_ids: [3], strict_priority: false, assign_proxies: false })
  })
  it('preserves the ungrouped zero filter in server pagination', async () => {
    client.get.mockResolvedValue({ data: { items: [], total: 0 } })
    await list(2, 20, { proxy_group_id: 0 })
    expect(client.get).toHaveBeenCalledWith('/admin/proxies', { params: { page: 2, page_size: 20, proxy_group_id: 0 }, signal: undefined })
  })
})
