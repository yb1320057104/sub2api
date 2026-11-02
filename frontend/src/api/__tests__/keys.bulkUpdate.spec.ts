import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { bulkUpdate, create } from '../keys'

const { put, post } = vi.hoisted(() => ({ put: vi.fn(), post: vi.fn() }))
vi.mock('../client', () => ({ apiClient: { put, post } }))

describe('API key bulk updates', () => {
  beforeEach(() => {
    put.mockReset()
    post.mockReset().mockResolvedValue({ data: { id: 1 } })
  })

  it('updates each selected key once and preserves partial failures', async () => {
    const failure = { response: { status: 403, data: { detail: 'Forbidden' } } }
    put.mockImplementation((url: string) => url === '/keys/2'
      ? Promise.reject(failure)
      : Promise.resolve({ data: { id: Number(url.split('/').pop()) } }))

    const updates = { group_id: 12, quota: 0, expires_at: '', ip_whitelist: [] }
    const result = await bulkUpdate([1, 2, 1, 3], updates)

    expect(put.mock.calls).toEqual([
      ['/keys/1', updates], ['/keys/2', updates], ['/keys/3', updates]
    ])
    expect(result).toEqual({ succeededIds: [1, 3], failures: [{ id: 2, error: failure }] })
  })

  it('limits in-flight requests and continues after a failed batch member', async () => {
    const settle: Array<() => void> = []
    let inFlight = 0
    let peak = 0
    put.mockImplementation(() => new Promise((resolve, reject) => {
      inFlight++
      peak = Math.max(peak, inFlight)
      const index = settle.length
      settle.push(() => {
        inFlight--
        if (index === 0) reject(new Error('network error'))
        else resolve({ data: {} })
      })
    }))

    const pending = bulkUpdate([1, 2, 3, 4, 5, 6, 7], { status: 'inactive' })
    expect(put).toHaveBeenCalledTimes(5)
    settle.slice(0, 4).forEach((finish) => finish())
    await flushPromises()
    expect(put).toHaveBeenCalledTimes(5)
    settle[4]()
    await flushPromises()
    expect(put).toHaveBeenCalledTimes(7)
    settle.slice(5).forEach((finish) => finish())
    const result = await pending

    expect(peak).toBe(5)
    expect(result.succeededIds).toEqual([2, 3, 4, 5, 6, 7])
    expect(result.failures.map(({ id }) => id)).toEqual([1])
  })

  it('does not send requests when no keys are selected', async () => {
    expect(await bulkUpdate([], { quota: 0 })).toEqual({ succeededIds: [], failures: [] })
    expect(put).not.toHaveBeenCalled()
  })

  it('creates ordered groups without reordering and preserves legacy singular create calls', async () => {
    await create('Ordered', 9, undefined, undefined, undefined, undefined, undefined, undefined, [9, 2, 9])
    expect(post).toHaveBeenLastCalledWith('/keys', { name: 'Ordered', group_id: 9, group_ids: [9, 2] })
    await create('Legacy', 7)
    expect(post).toHaveBeenLastCalledWith('/keys', { name: 'Legacy', group_id: 7 })
    await create('Ungrouped', 7, undefined, undefined, undefined, undefined, undefined, undefined, [])
    expect(post).toHaveBeenLastCalledWith('/keys', { name: 'Ungrouped', group_id: null, group_ids: [] })
  })
})
