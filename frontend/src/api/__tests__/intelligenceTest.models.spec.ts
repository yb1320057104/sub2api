import { beforeEach, describe, expect, it, vi } from 'vitest'
import { intelligenceTestAPI } from '../intelligenceTest'

const get = vi.hoisted(() => vi.fn())
vi.mock('../client', () => ({ apiClient: { get } }))

beforeEach(() => vi.clearAllMocks())

describe('intelligence model catalog API', () => {
  it('uses the administrator read-only endpoint with cancellation', async () => {
    const controller = new AbortController()
    const catalog = { models: ['local-model'], excluded_models: ['media-alias'] }
    get.mockResolvedValueOnce({ data: catalog })
    expect(await intelligenceTestAPI.models(controller.signal)).toEqual(catalog)
    expect(get).toHaveBeenCalledWith('/admin/intelligence-test/models', { signal: controller.signal })
  })

  it('propagates failures so the caller can preserve draft and previous choices', async () => {
    get.mockRejectedValueOnce(new Error('catalog unavailable'))
    await expect(intelligenceTestAPI.models()).rejects.toThrow('catalog unavailable')
  })
})

describe('user quality history API', () => {
  it('reads only the authenticated display endpoints and forwards cancellation', async () => {
    const controller = new AbortController()
    get.mockResolvedValueOnce({ data: { enabled: true, groups: [{ id: 7, name: 'Visible', platform: 'openai' }] } })
    expect((await intelligenceTestAPI.qualityGroups(controller.signal)).groups).toHaveLength(1)
    expect(get).toHaveBeenLastCalledWith('/intelligence-test/quality/groups', { signal: controller.signal })
    get.mockResolvedValueOnce({ data: { enabled: true, group_id: 7, results: [] } })
    expect((await intelligenceTestAPI.qualityResults(7, controller.signal)).results).toEqual([])
    expect(get).toHaveBeenLastCalledWith('/intelligence-test/quality/results', { params: { group_id: 7 }, signal: controller.signal })
  })

  it('propagates unavailable scope without falling back to administrator or legacy history', async () => {
    get.mockRejectedValueOnce(new Error('not shared'))
    await expect(intelligenceTestAPI.qualityResults(7)).rejects.toThrow('not shared')
    expect(get).toHaveBeenCalledOnce()
  })
})
