import type { ApiKey } from '@/types'

/** An explicit empty list means ungrouped; older responses use the singular field. */
export function apiKeyGroupIds(key: Pick<ApiKey, 'group_id' | 'group_ids'>): number[] {
  return [...new Set(Array.isArray(key.group_ids) ? key.group_ids : key.group_id ? [key.group_id] : [])]
}
