export class KiloRequestRecoveryError extends Error {
  readonly code = 'KILO_REQUEST_RECOVERY_UNAVAILABLE'
}

export interface KiloRequestIdentity {
  requestId: string
  recovered: boolean
  complete: () => void
}

// Only a payload digest and random request ID survive a reload. Import keys,
// names and account identifiers never enter the browser recovery record.
export async function reserveKiloRequest(operation: 'import' | 'batch' | 'onboarding' | 'onboarding-import' | 'unbind', payload: unknown): Promise<KiloRequestIdentity> {
  try {
    const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(JSON.stringify(payload)))
    const fingerprint = Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, '0')).join('')
    const key = `kilo-pending-${operation}-${fingerprint}`
    const previous = sessionStorage.getItem(key)
    const requestId = previous || crypto.randomUUID()
    sessionStorage.setItem(key, requestId)
    return {
      requestId,
      recovered: !!previous,
      complete() {
        // Keeping an acknowledged ID when storage is unavailable is safer than
        // losing an unacknowledged submission and issuing a second request.
        try { if (sessionStorage.getItem(key) === requestId) sessionStorage.removeItem(key) } catch { /* Retain the recovery record. */ }
      }
    }
  } catch {
    throw new KiloRequestRecoveryError('Request recovery storage is unavailable')
  }
}
