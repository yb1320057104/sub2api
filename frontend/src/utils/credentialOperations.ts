import type { LocationQuery } from 'vue-router'

export const credentialOperationsSections = ['accounts', 'inspection', 'workbench', 'proxies', 'history', 'rules'] as const
export type CredentialOperationsSection = typeof credentialOperationsSections[number]

export function credentialOperationsSection(value: unknown): CredentialOperationsSection {
  return credentialOperationsSections.find(section => section === value) ?? 'accounts'
}

export function legacyAuthorizationLocation(query: LocationQuery) {
  const tab = query.tab === 'proxies' || query.tab === 'history' ? query.tab : 'workbench'
  return { path: '/admin/token-guard-v2', query: { ...query, tab } }
}
