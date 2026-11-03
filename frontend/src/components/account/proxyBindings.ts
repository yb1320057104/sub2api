import type { Account, AccountProxyBinding, AccountProxyBindingInput } from '@/types'

export function accountProxyBindings(account: Pick<Account, 'proxy_bindings' | 'proxy_id' | 'proxy'>): AccountProxyBinding[] {
  if (Array.isArray(account.proxy_bindings)) return account.proxy_bindings
  if (!account.proxy_id) return []
  return [{ proxy_id: account.proxy_id, proxy: account.proxy, max_concurrency: account.proxy?.max_concurrency ?? 0, max_accounts: account.proxy?.max_accounts ?? 0, current_concurrency: null }]
}

export function proxyBindingInputs(account: Pick<Account, 'proxy_bindings' | 'proxy_id' | 'proxy'>): AccountProxyBindingInput[] {
  return accountProxyBindings(account).map(({ proxy_id, max_concurrency, max_accounts }) => ({ proxy_id, max_concurrency, max_accounts }))
}

export function validProxyBindings(bindings: AccountProxyBindingInput[]): boolean {
  return bindings.length <= 100 && new Set(bindings.map(row => row.proxy_id)).size === bindings.length && bindings.every(row =>
    Number.isSafeInteger(row.proxy_id) && row.proxy_id > 0 && [row.max_concurrency, row.max_accounts].every(value => Number.isInteger(value) && value >= 0 && value <= 10000))
}
