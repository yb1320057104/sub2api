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
