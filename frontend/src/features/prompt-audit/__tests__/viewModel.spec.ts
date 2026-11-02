import { describe, expect, it } from 'vitest'
import type { PromptAuditConfig, PromptLocalRule } from '../types'
import {
  buildUpdateRequest,
  configToDraft,
  draftFingerprint,
  emptyEventFilters,
  eventFilterPayload,
  hasExplicitDeleteRange,
  SCANNER_CATALOG,
} from '../viewModel'

const config = (): PromptAuditConfig => ({
  enabled: true,
  blocking_enabled: false,
  blocking_latest_turn_only: false,
  store_pass_events: false,
  effective_mode: 'async_audit',
  strategy: 'priority',
  worker_count: 4,
  queue_capacity: 100,
  scanners: SCANNER_CATALOG.map((item) => item.id),
  all_groups: true,
  group_ids: [],
  endpoints: [{
    id: 'guard-1', name: 'Guard One', protocol: 'openai_compatible', base_url: 'http://127.0.0.1:8000',
    model: 'sileader/qwen3guard:0.6b', timeout_ms: 3000, input_limit: 4000, enabled: true,
    has_token: true, token_status: 'configured',
  }],
  config_version: 7,
  updated_at: '2026-07-16T00:00:00Z',
  updated_by: 1,
  change_summary: '{}',
})

describe('Prompt Audit view model', () => {
  it('uses local rules only for an unconfigured installation and preserves a saved engine choice', () => {
    expect(configToDraft({ ...config(), enabled: false, updated_at: '', endpoints: [] }).engine).toBe('local_rules')
    expect(configToDraft({ ...config(), engine: 'remote', enabled: false, endpoints: [] }).engine).toBe('remote')
    expect(configToDraft({ ...config(), engine: 'remote', enabled: true, updated_at: '', endpoints: [] }).engine).toBe('remote')
    expect(configToDraft({ ...config(), engine: 'remote', enabled: false, updated_at: '' }).engine).toBe('remote')
    expect(configToDraft({ ...config(), engine: 'local_rules' }).engine).toBe('local_rules')
  })

  it('keeps explicit empty rules and instructions after save instead of reviving defaults', () => {
    const defaults: PromptLocalRule[] = [{ id: 'template', name: 'Template', kind: 'keyword', pattern: 'template pattern', category: 'jailbreak', action: 'warn', enabled: true, case_sensitive: false }]
    const cleared = configToDraft({ ...config(), local_rules: [], audit_instructions: '', default_local_rules: defaults, default_audit_instructions: 'Template instructions' })
    expect(cleared.local_rules).toEqual([])
    expect(cleared.audit_instructions).toBe('')
    expect(buildUpdateRequest(cleared)).toMatchObject({ local_rules: [], audit_instructions: '' })
    const fresh = configToDraft({ ...config(), default_local_rules: defaults, default_audit_instructions: 'Template instructions' })
    expect(fresh.local_rules).toEqual(defaults)
    fresh.local_rules[0].pattern = 'custom'
    expect(defaults[0].pattern).toBe('template pattern')
  })

  it('forces explicit groups in legacy drafts and save payloads without changing stored endpoint credentials', () => {
    const draft = configToDraft({ ...config(), all_groups: true, group_ids: [9, 2, 9] })
    expect(draft.all_groups).toBe(false)
    draft.all_groups = true
    draft.engine = 'local_rules'
    expect(buildUpdateRequest(draft)).toMatchObject({ all_groups: false, group_ids: [2, 9], endpoints: [expect.objectContaining({ token: undefined, clear_token: false })] })
    draft.group_ids = []
    expect(buildUpdateRequest(draft)).toMatchObject({ all_groups: false, group_ids: [] })
  })

  it('normalizes legacy null collections from the public config', () => {
    const legacy = { ...config(), group_ids: null, scanners: null, endpoints: null } as unknown as PromptAuditConfig
    expect(configToDraft(legacy)).toMatchObject({ group_ids: [], scanners: [], endpoints: [] })
  })

  it('models all nine official input scanners', () => {
    expect(SCANNER_CATALOG).toHaveLength(9)
    expect(SCANNER_CATALOG.map((item) => item.id)).toContain('suicide_and_self_harm')
  })

  it('keeps, replaces, or explicitly clears a saved token without copying plaintext from the server', () => {
    const draft = configToDraft(config())
    expect(draft.endpoints[0].token).toBe('')
    expect(buildUpdateRequest(draft).endpoints[0]).toMatchObject({ token: undefined, clear_token: false })

    draft.endpoints[0].token = 'temporary-canary-token'
    expect(buildUpdateRequest(draft).endpoints[0]).toMatchObject({ token: 'temporary-canary-token', clear_token: false })

    draft.endpoints[0].token = ''
    draft.endpoints[0].clear_token = true
    expect(buildUpdateRequest(draft).endpoints[0]).toMatchObject({ token: undefined, clear_token: true })
  })

  it('includes the optional narrow blocking scope in the update payload', () => {
    const draft = configToDraft(config())
    draft.blocking_latest_turn_only = true
    expect(buildUpdateRequest(draft)).toMatchObject({ blocking_latest_turn_only: true })
  })

  it('tracks dirty state from the full normalized save payload', () => {
    const original = configToDraft(config())
    const changed = configToDraft(config())
    expect(draftFingerprint(changed)).toBe(draftFingerprint(original))
    changed.queue_capacity += 1
    expect(draftFingerprint(changed)).not.toBe(draftFingerprint(original))
  })

  it('requires a valid explicit range and sends canonical ISO timestamps for filter deletion', () => {
    const filters = emptyEventFilters()
    expect(hasExplicitDeleteRange(filters)).toBe(false)
    filters.start_at = '2026-07-15T10:00'
    filters.end_at = '2026-07-16T10:00'
    filters.group_id = '9'
    expect(hasExplicitDeleteRange(filters)).toBe(true)
    expect(eventFilterPayload(filters)).toMatchObject({
      group_id: 9,
      start_at: new Date(filters.start_at).toISOString(),
      end_at: new Date(filters.end_at).toISOString(),
    })
  })
})
