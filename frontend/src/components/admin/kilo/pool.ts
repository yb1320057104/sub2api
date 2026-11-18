import type { KiloPrepared } from '@/api/admin/kilo'

export function canActivateKiloPrepared(item: KiloPrepared): boolean {
  return item.status === 'ready' && item.has_credential && item.identity_status !== 'banned' && item.identity_status !== 'retired' &&
    (!item.binding_state || !['recovering', 'unbinding', 'binding', 'blocked'].includes(item.binding_state))
}

export function canUnbindKiloPrepared(item: KiloPrepared): boolean {
  return !!item.source_account_id && item.status !== 'binding' &&
    (!item.binding_state || !['recovering', 'unbinding', 'binding'].includes(item.binding_state))
}

export function isReusableKiloPrepared(item: KiloPrepared): boolean {
  // Only verified independent identities may cross GPT lifecycles; the server also checks recovery and ownership.
  return item.identity_mode === 'independent' && item.reusable === true && item.identity_status === 'active'
}
