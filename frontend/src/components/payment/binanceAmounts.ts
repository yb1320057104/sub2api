/** Format a server decimal without converting it to a floating-point number. */
export function formatBinanceMultiplier(value: string): string {
  const [whole, fraction = ''] = value.split('.')
  const trimmedFraction = fraction.replace(/0+$/, '')
  const trimmedWhole = whole.replace(/^0+(?=\d)/, '')
  return trimmedFraction ? `${trimmedWhole}.${trimmedFraction}` : trimmedWhole
}

/** Validate the rate in millionths; balance amounts are always supplied by the server. */
export function isValidBinanceMultiplier(value: string): boolean {
  if (!/^\d{1,4}(?:\.\d{1,6})?$/.test(value)) return false
  const [whole, fraction = ''] = value.split('.')
  const units = BigInt(whole) * 1000000n + BigInt(fraction.padEnd(6, '0'))
  return units >= 1n && units <= 1000000000n
}
