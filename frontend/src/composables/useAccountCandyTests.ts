import { computed, onUnmounted, ref, watch, type Ref } from 'vue'
import { useIntervalFn } from '@vueuse/core'
import { accountCandyAPI, getCandyMovedGroupIDs, type AccountCandyResult } from '@/api/admin/accountCandy'
import type { AccountListItem } from '@/types'

export function supportsAccountCandyTest(account: AccountListItem): boolean {
  return account.platform === 'openai' && account.extra?.synthetic_ui_test !== true
}

export function useAccountCandyTests(
  accounts: Ref<AccountListItem[]>,
  onAccountsChanged: () => void,
) {
  const results = ref<Record<number, AccountCandyResult>>({})
  const startingIDs = ref<Set<number>>(new Set())
  const loading = ref(false)
  const loadError = ref(false)
  const loaded = ref(false)
  const accountIDs = computed(() => accounts.value.filter(supportsAccountCandyTest).map(account => account.id))
  let controller: AbortController | null = null
  let generation = 0
  let disposed = false
  const terminalRuns = new Map<number, string>()

  async function refresh() {
    if (disposed || loading.value) return
    const ids = [...accountIDs.value]
    if (!ids.length) {
      results.value = {}
      loaded.value = true
      return
    }
    const currentGeneration = ++generation
    const request = new AbortController()
    controller = request
    loading.value = true
    try {
      const latest: AccountCandyResult[] = []
      // Bound query length independently of the selected table page size.
      for (let offset = 0; offset < ids.length; offset += 200) {
        latest.push(...await accountCandyAPI.latest(ids.slice(offset, offset + 200), request.signal))
      }
      if (disposed || currentGeneration !== generation) return
      const moved = loaded.value && latest.some(result => {
        const previous = results.value[result.account_id]
        const targets = getCandyMovedGroupIDs(result)
        return targets.length > 0 && (previous?.id !== result.id || previous?.origin !== result.origin ||
          getCandyMovedGroupIDs(previous).join(',') !== targets.join(','))
      })
      let finished = false
      for (const result of latest) {
        if (result.status === 'running') continue
        const identity = `${result.origin ?? 'radar'}:${result.id}`
        if (loaded.value && terminalRuns.get(result.account_id) !== identity) finished = true
        terminalRuns.set(result.account_id, identity)
      }
      results.value = Object.fromEntries(latest.map(result => [result.account_id, result]))
      loadError.value = false
      loaded.value = true
      // Failed tests may update account status without moving it to another group.
      if (moved || finished) onAccountsChanged()
    } catch {
      if (!request.signal.aborted && currentGeneration === generation) loadError.value = true
    } finally {
      if (currentGeneration === generation) {
        loading.value = false
        controller = null
      }
    }
  }

  watch(() => accountIDs.value.join(','), () => {
    generation++
    controller?.abort()
    controller = null
    loading.value = false
    loadError.value = false
    loaded.value = false
    const visibleIDs = new Set(accountIDs.value)
    for (const id of terminalRuns.keys()) if (!visibleIDs.has(id)) terminalRuns.delete(id)
    results.value = Object.fromEntries(Object.entries(results.value).filter(([id]) => visibleIDs.has(Number(id))))
    void refresh()
  }, { immediate: true })

  async function start(accountID: number) {
    if (startingIDs.value.has(accountID) || results.value[accountID]?.status === 'running') return false
    startingIDs.value = new Set([...startingIDs.value, accountID])
    try {
      const result = await accountCandyAPI.start(accountID)
      if (disposed) return false
      // A list request started before the mutation must not replace the new task.
      generation++
      controller?.abort()
      controller = null
      loading.value = false
      if (accountIDs.value.includes(accountID)) {
        results.value = { ...results.value, [accountID]: result }
      }
      return true
    } finally {
      const pending = new Set(startingIDs.value)
      pending.delete(accountID)
      startingIDs.value = pending
    }
  }

  useIntervalFn(() => {
    if (!document.hidden) void refresh()
  }, 5000)

  onUnmounted(() => {
    disposed = true
    generation++
    controller?.abort()
  })

  return { results, startingIDs, loading, loadError, loaded, refresh, start }
}
