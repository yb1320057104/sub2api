export type AuditPolicyAction = 'allow' | 'block' | 'charge_allow'
export type AuditRiskLevel = 'low' | 'medium' | 'high'

export interface AuditRiskPolicy {
  low_threshold: number
  medium_threshold: number
  high_threshold: number
  low_action: AuditPolicyAction
  medium_action: AuditPolicyAction
  high_action: AuditPolicyAction
}

export function defaultAuditRiskPolicy(): AuditRiskPolicy {
  return {
    low_threshold: 20, medium_threshold: 40, high_threshold: 60,
    low_action: 'allow', medium_action: 'allow', high_action: 'block',
  }
}

export function validAuditScore(value: unknown): value is number {
  return typeof value === 'number' && Number.isInteger(value) && value >= 0 && value <= 100
}

export function validAuditRiskPolicy(policy: AuditRiskPolicy | null | undefined): boolean {
  if (policy == null) return true
  const actions: AuditPolicyAction[] = ['allow', 'block', 'charge_allow']
  return validAuditScore(policy.low_threshold) && validAuditScore(policy.medium_threshold) && validAuditScore(policy.high_threshold)
    && policy.low_threshold < policy.medium_threshold && policy.medium_threshold < policy.high_threshold
    && actions.includes(policy.low_action) && actions.includes(policy.medium_action) && actions.includes(policy.high_action)
}

export function auditKeywordKey(keyword: string): string { return keyword.trim().toLowerCase() }

export function auditKeywordScore(keyword: string, scores: Record<string, number>): number {
  const key = auditKeywordKey(keyword)
  return Object.prototype.hasOwnProperty.call(scores, key) ? scores[key] : 100
}

// Only current keywords are authoritative. Removed entries cannot return when
// the draft is saved, and an explicit score of zero is never replaced by 100.
export function auditKeywordScoresPayload(keywords: string[], scores: Record<string, number>): Record<string, number> {
  return Object.fromEntries(keywords.map(keyword => [auditKeywordKey(keyword), auditKeywordScore(keyword, scores)]))
}
