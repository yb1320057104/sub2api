import { describe, expect, it } from 'vitest'
import en from '@/i18n/locales/en'
import zh from '@/i18n/locales/zh'

function flattenKeys(obj: Record<string, any>, prefix = ''): string[] {
  const keys: string[] = []
  for (const [k, v] of Object.entries(obj)) {
    const fullKey = prefix ? `${prefix}.${k}` : k
    if (typeof v === 'object' && v !== null && !Array.isArray(v)) {
      keys.push(...flattenKeys(v, fullKey))
    } else {
      keys.push(fullKey)
    }
  }
  return keys
}

describe('ops locale key completeness', () => {
  const requiredKeys = [
    'admin.ops.result',
    'admin.ops.timeRange.custom',
    'admin.ops.customTimeRange.startTime',
    'admin.ops.customTimeRange.endTime',
    'admin.ops.errorDetail.upstreamStatus',
    'admin.ops.errorDetail.rootCause',
    'admin.ops.errorDetail.diagnosticPayloads',
    'admin.ops.errorDetail.payloads.client',
    'admin.ops.errorDetail.payloads.upstream_message',
    'admin.ops.errorDetail.payloads.upstream_detail',
    'admin.ops.errorDetail.payloads.upstream_events',
  ]

  for (const key of requiredKeys) {
    it(`en locale has ${key}`, () => {
      const enKeys = flattenKeys(en)
      expect(enKeys).toContain(key)
    })
  }

  for (const key of requiredKeys) {
    it(`zh locale has ${key}`, () => {
      const zhKeys = flattenKeys(zh)
      expect(zhKeys).toContain(key)
    })
  }
})

describe('smart operations instructions', () => {
  const tools = ['radar', 'credentials', 'notifications', 'quality', 'auto-config', 'priority', 'channels', 'profit', 'experiments', 'external-guard', 'pelican', 'cache-tests', 'captures', 'ai', 'harvest', 'lottery']

  it.each([['en', en], ['zh', zh]] as const)('%s provides usable instructions for every workspace tool', (_locale, messages) => {
    const guides = messages.smartOps.tools as Record<string, Record<string, string>>
    expect(Object.keys(guides).sort()).toEqual([...tools].sort())
    for (const tool of tools) {
      for (const field of ['title', 'purpose', 'when', 'step1', 'step2', 'step3', 'step4', 'result', 'effects']) {
        expect(guides[tool][field], `${tool}.${field}`).toBeTypeOf('string')
        expect(guides[tool][field].trim().length, `${tool}.${field}`).toBeGreaterThan(0)
      }
    }
    for (const label of Object.values(messages.smartOps.notifications)) expect(label.trim().length).toBeGreaterThan(0)
  })
})

describe('groups locale key completeness', () => {
  it('en locale has admin.groups.failedToSave', () => {
    const enKeys = flattenKeys(en)
    expect(enKeys).toContain('admin.groups.failedToSave')
  })

  const webSearchPricingKeys = [
    'admin.groups.webSearchPricing.title',
    'admin.groups.webSearchPricing.pricePerCall',
    'admin.groups.webSearchPricing.pricePerCallHint',
    'admin.groups.webSearchPricing.finalPricePreview',
  ]

  for (const key of webSearchPricingKeys) {
    it(`en and zh locales both have ${key}`, () => {
      expect(flattenKeys(en)).toContain(key)
      expect(flattenKeys(zh)).toContain(key)
    })
  }
})
