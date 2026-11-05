import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, ref } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { useAccountCandyTests } from '../useAccountCandyTests'
import type { AccountCandyResult } from '@/api/admin/accountCandy'
import type { AccountListItem } from '@/types'

const api = vi.hoisted(() => ({ latest: vi.fn(), start: vi.fn() }))
vi.mock('@/api/admin/accountCandy', async () => ({ ...await vi.importActual<typeof import('@/api/admin/accountCandy')>('@/api/admin/accountCandy'), accountCandyAPI: api }))
vi.mock('@vueuse/core', () => ({ useIntervalFn: vi.fn() }))
const wrappers: ReturnType<typeof mount>[] = []
const result = (overrides: Partial<AccountCandyResult> = {}) => ({ id: 1, account_id: 11, status: 'running', origin: 'radar', moved_to_group_id: null, ...overrides }) as AccountCandyResult

async function open(initial: AccountCandyResult[]) {
  api.latest.mockResolvedValue(initial)
  const changed = vi.fn()
  const accounts = ref([{ id: 11, platform: 'openai' }] as AccountListItem[])
  let candy!: ReturnType<typeof useAccountCandyTests>
  wrappers.push(mount(defineComponent({ setup() { candy = useAccountCandyTests(accounts, changed); return () => h('div') } })))
  await flushPromises()
  return { candy, changed, accounts }
}
beforeEach(() => { vi.clearAllMocks() })
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()) })

describe('account refresh after candy results', () => {
  it('does not refresh historical terminal results on initial load or repeated polls', async () => {
    const { candy, changed } = await open([result({ status: 'failed' })])
    await candy.refresh(); await candy.refresh()
    expect(changed).not.toHaveBeenCalled()
  })

  it.each(['completed', 'failed', 'partial_failed', 'cancelled'] as const)('refreshes once when a running task becomes %s without moving groups', async status => {
    const { candy, changed } = await open([result()])
    api.latest.mockResolvedValue([result({ status })])
    await candy.refresh(); await candy.refresh()
    expect(changed).toHaveBeenCalledTimes(1)
  })

  it('refreshes for a new fast terminal result even if running was not observed', async () => {
    const { candy, changed } = await open([])
    api.latest.mockResolvedValue([result({ status: 'failed' })])
    await candy.refresh(); await candy.refresh()
    api.latest.mockResolvedValue([result({ id: 2, status: 'failed' })])
    await candy.refresh()
    expect(changed).toHaveBeenCalledTimes(2)
  })

  it('distinguishes equal IDs from different origins and deduplicates missing-result gaps', async () => {
    const { candy, changed } = await open([result({ status: 'completed' })])
    api.latest.mockResolvedValue([result({ origin: 'authorization', status: 'failed' })])
    await candy.refresh()
    api.latest.mockResolvedValue([]); await candy.refresh()
    api.latest.mockResolvedValue([result({ origin: 'authorization', status: 'failed' })]); await candy.refresh()
    expect(changed).toHaveBeenCalledTimes(1)
  })

  it('preserves transfer refresh and coalesces it with task completion', async () => {
    const { candy, changed } = await open([result()])
    api.latest.mockResolvedValue([result({ status: 'completed', moved_to_group_ids: [2] })]); await candy.refresh()
    expect(changed).toHaveBeenCalledTimes(1)
    api.latest.mockResolvedValue([result({ status: 'completed', moved_to_group_ids: [3] })]); await candy.refresh()
    expect(changed).toHaveBeenCalledTimes(2)
  })

  it('does not mark the account stopped from an error message while a test is running', async () => {
    const { candy, changed } = await open([result()])
    api.latest.mockResolvedValue([result({ error_message: 'HTTP 401', status: 'running' })]); await candy.refresh()
    expect(changed).not.toHaveBeenCalled()
  })
})
