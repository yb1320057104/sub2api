import type { KiloImportItem } from '@/api/admin/kilo'

export type KiloImportIssue = 'invalidJson' | 'invalidFormat' | 'tooMany' | 'missingName' | 'missingKey' | 'invalidField' | 'duplicateKey'
export interface KiloImportRow {
  line: number
  name: string
  email: string
  organizationId: string
  issue?: KiloImportIssue
  item?: KiloImportItem
}
export interface KiloImportPreview {
  rows: KiloImportRow[]
  issue?: KiloImportIssue
}

function record(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : null
}

export function parseKiloImport(input: string): KiloImportPreview {
  const text = input.trim().replace(/^\uFEFF/, '')
  if (!text) return { rows: [] }
  let values: Array<{ line: number; value: unknown }>
  if (text.startsWith('[') || text.startsWith('{')) {
    let parsed: unknown
    try { parsed = JSON.parse(text) } catch { return { rows: [], issue: 'invalidJson' } }
    const object = record(parsed)
    const accounts = Array.isArray(parsed) ? parsed : object?.accounts ?? object?.items
    if (!Array.isArray(accounts)) return { rows: [], issue: 'invalidFormat' }
    if (accounts.length > 1000) return { rows: [], issue: 'tooMany' }
    values = accounts.map((value, index) => ({ line: index + 1, value }))
  } else {
    const lines = input.split(/\r?\n/).map((value, index) => ({ line: index + 1, value: value.trim() })).filter(({ value }) => value)
    if (lines.length > 1000) return { rows: [], issue: 'tooMany' }
    values = lines.map(({ line, value }) => {
      const divider = value.indexOf('----')
      return { line, value: divider < 0 ? null : { name: value.slice(0, divider), api_key: value.slice(divider + 4) } }
    })
  }
  const seenKeys = new Set<string>()
  const rows = values.map(({ line, value }): KiloImportRow => {
    const account = record(value)
    if (!account) return { line, name: '', email: '', organizationId: '', issue: 'invalidFormat' }
    const credentials = record(account.credentials) ?? record(account.credential)
    const key = account.api_key ?? credentials?.api_key
    const name = typeof account.name === 'string' ? account.name.trim() : ''
    const email = typeof account.email === 'string' ? account.email.trim() : ''
    const organizationId = typeof account.organization_id === 'string' ? account.organization_id.trim() : ''
    const row: KiloImportRow = { line, name, email, organizationId }
    if (!name) return { ...row, issue: 'missingName' }
    if (typeof key !== 'string' || !key.trim()) return { ...row, issue: 'missingKey' }
    if (account.email != null && typeof account.email !== 'string' || account.organization_id != null && typeof account.organization_id !== 'string') return { ...row, issue: 'invalidField' }
    const apiKey = key.trim()
    if (seenKeys.has(apiKey)) return { ...row, issue: 'duplicateKey' }
    seenKeys.add(apiKey)
    return { ...row, item: { name, api_key: apiKey, ...(email ? { email } : {}), ...(organizationId ? { organization_id: organizationId } : {}) } }
  })
  return { rows }
}
