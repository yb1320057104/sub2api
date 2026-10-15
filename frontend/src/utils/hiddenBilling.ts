// PK-029: keep account pricing extensions in one adapter for all account forms.
export function isHiddenBillingMultiplierValid(value: unknown): value is number {
  return typeof value === 'number'
    && Number.isFinite(value)
    && value >= 0
    && value <= 1000
    && Number(value.toFixed(6)) === value
}

export function formatBillingMultiplier(value: number): string {
  if (value === 0) return '0.0'
  return Number.isFinite(value)
    ? value.toLocaleString('en-US', { useGrouping: false, minimumFractionDigits: 2, maximumFractionDigits: 12 })
    : '-'
}

export function hiddenBillingMultiplierFromExtra(extra?: Record<string, unknown> | null): number {
  const value = extra?.hidden_rate_multiplier
  return isHiddenBillingMultiplierValid(value) ? value : 1
}

export function withHiddenBillingExtra(extra: Record<string, unknown> | undefined, multiplier: number | string): Record<string, unknown> {
  return { ...extra, hidden_rate_multiplier: multiplier }
}
