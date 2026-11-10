import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, put } = vi.hoisted(() => ({
  get: vi.fn(),
  put: vi.fn(),
}))

vi.mock('@/api/client', () => ({
  apiClient: { get, put },
}))

import { getAntiDistillation, getUsageSummary, saveAntiDistillation } from '@/api/admin/groups'

describe('admin group usage summary API', () => {
  beforeEach(() => {
    get.mockReset()
    get.mockResolvedValue({ data: [] })
  })

  it('does not send browser timezone parameters', async () => {
    const summary = [
      { group_id: 1, today_cost: 1.25, yesterday_cost: 2.5, total_cost: 9.75 },
    ]
    get.mockResolvedValue({ data: summary })

    await expect(getUsageSummary()).resolves.toEqual(summary)

    expect(get).toHaveBeenCalledWith('/admin/groups/usage-summary')
  })
})

describe('admin group anti-distillation API allowlist contract', () => {
  beforeEach(() => { get.mockReset(); put.mockReset() })
  it('normalizes a legacy missing allowlist to an empty list', async () => {
    get.mockResolvedValue({ data: { enabled: false, pause_minutes: 5 } })
    await expect(getAntiDistillation(7)).resolves.toEqual({ enabled: false, pause_minutes: 5, user_allowlist_ids: [] })
    expect(get).toHaveBeenCalledWith('/admin/groups/7/anti-distillation')
  })
  it('keeps the complete allowlist and sends an explicit empty array when cleared', async () => {
    const saved = { enabled: true, pause_minutes: 9, user_allowlist_ids: [7, 42] }
    put.mockResolvedValue({ data: saved })
    await expect(saveAntiDistillation(4, saved)).resolves.toEqual(saved)
    expect(put).toHaveBeenLastCalledWith('/admin/groups/4/anti-distillation', saved)
    const cleared = { ...saved, user_allowlist_ids: [] }
    put.mockResolvedValue({ data: cleared })
    await expect(saveAntiDistillation(4, cleared)).resolves.toEqual(cleared)
    expect(put).toHaveBeenLastCalledWith('/admin/groups/4/anti-distillation', cleared)
    expect(saved.user_allowlist_ids).toEqual([7, 42])
  })
})
