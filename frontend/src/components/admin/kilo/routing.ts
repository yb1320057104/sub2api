import type { KiloProxyGroup, KiloRoutingOptions } from '@/api/admin/kilo'

// The Kilo API reserves -1 for the unfiltered request pool and 0 for no selection.
export const KILO_ALL_REQUEST_PROXIES = -1

export function kiloRequestProxyOptions(
  groups: readonly KiloProxyGroup[],
  t: (key: string, named?: Record<string, string | number>) => string,
  disableEmptyGroups = true,
) {
  return [
    { value: KILO_ALL_REQUEST_PROXIES, label: t('kilo.batch.allRequestProxies') },
    ...groups.map(group => ({
      value: group.id,
      label: t('kilo.batch.proxyOption', { name: group.name, count: group.active_proxy_count }),
      disabled: disableEmptyGroups && group.active_proxy_count === 0,
    })),
  ]
}

export function kiloRoutingOptions(value?: Partial<KiloRoutingOptions> | null): KiloRoutingOptions {
  return {
    proxy_group_id: value?.proxy_group_id ?? 0,
    proxies_per_account: value?.proxies_per_account ?? 5,
    proxy_concurrency: value?.proxy_concurrency ?? 20,
    account_concurrency: value?.account_concurrency ?? 100,
    priority: value?.priority ?? 1,
    auto_release_statuses: [...(value?.auto_release_statuses ?? [401, 402, 403])]
  }
}

export function parseAutoReleaseStatuses(value: string): number[] | null {
  const parts = value.trim() ? value.trim().split(/[\s,，]+/) : []
  const statuses = parts.map(Number)
  if (statuses.some(status => !Number.isInteger(status) || status < 400 || status > 599)) return null
  return [...new Set(statuses)]
}
