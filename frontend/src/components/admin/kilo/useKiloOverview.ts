import { computed, onBeforeUnmount, onMounted, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import * as api from '@/api/admin/kilo'
import type { KiloCandidate, KiloGroup, KiloJob, KiloOverview, KiloProxyGroup } from '@/api/admin/kilo'

export function useKiloOverview(batchId: () => string = () => '') {
  const { t } = useI18n()
  const overview = shallowRef<KiloOverview | null>(null)
  const candidates = shallowRef<KiloCandidate[]>([])
  const groups = shallowRef<KiloGroup[]>([])
  const proxyGroups = shallowRef<KiloProxyGroup[]>([])
  const loading = shallowRef(false)
  const choicesLoading = shallowRef(false)
  const error = shallowRef('')
  const choicesError = shallowRef('')
  const batchJobs = shallowRef<KiloJob[]>([])
  const batchLoading = shallowRef(false)
  const batchError = shallowRef('')
  const autoRefresh = shallowRef(true)
  const updatedAt = shallowRef<Date | null>(null)
  let timer: ReturnType<typeof setTimeout> | undefined
  let controller: AbortController | undefined
  let choicesController: AbortController | undefined
  let batchController: AbortController | undefined
  let batchGeneration = 0
  let batchInFlight: Promise<void> | undefined
  let disposed = false
  let inFlight: Promise<void> | undefined

  const prepared = computed(() => overview.value?.prepared ?? [])
  const jobs = computed(() => {
    const combined = new Map((overview.value?.jobs ?? []).map(job => [job.id, job]))
    for (const job of batchJobs.value) combined.set(job.id, job)
    return [...combined.values()].sort((a, b) => b.id - a.id)
  })

  function stopTimer() {
    if (timer) clearTimeout(timer)
    timer = undefined
  }
  function schedule() {
    stopTimer()
    if (!disposed && autoRefresh.value && document.visibilityState === 'visible') {
      timer = setTimeout(() => { void refresh() }, 5000)
    }
  }
  function refreshBatch(): Promise<void> {
    const id = batchId()
    if (!id || disposed || document.visibilityState !== 'visible') return Promise.resolve()
    if (batchInFlight) return batchInFlight
    const generation = batchGeneration
    const request = new AbortController()
    batchController = request
    batchLoading.value = true
    const promise = (async () => {
      try {
        const result = await api.getOnboardingBatch(id, request.signal)
        if (disposed || request.signal.aborted || generation !== batchGeneration || id !== batchId()) return
        batchJobs.value = result.items
        batchError.value = ''
      } catch (cause) {
        if (!disposed && !request.signal.aborted && generation === batchGeneration) batchError.value = api.kiloErrorMessage(cause, t('kilo.errors.onboardingBatch'))
      } finally {
        if (generation === batchGeneration) { batchLoading.value = false; batchInFlight = undefined }
      }
    })()
    batchInFlight = promise
    return promise
  }
  watch(batchId, () => {
    batchGeneration++
    batchController?.abort()
    batchInFlight = undefined
    batchJobs.value = []
    batchError.value = ''
    batchLoading.value = false
    void refreshBatch()
  })
  function refresh(): Promise<void> {
    if (disposed || document.visibilityState !== 'visible') return Promise.resolve()
    if (inFlight) return inFlight
    stopTimer()
    controller = new AbortController()
    const signal = controller.signal
    loading.value = true
    const batchRefresh = refreshBatch()
    inFlight = (async () => {
      try {
        const result = await api.getOverview(signal)
        if (signal.aborted || disposed) return
        overview.value = result
        error.value = ''
        updatedAt.value = new Date()
      } catch (cause) {
        if (!signal.aborted && !disposed && !api.isKiloRequestCanceled(cause)) error.value = api.kiloErrorMessage(cause, t('kilo.errors.load'))
      } finally {
        await batchRefresh
        loading.value = false
        inFlight = undefined
        schedule()
      }
    })()
    return inFlight
  }
  async function loadChoices() {
    if (disposed || choicesLoading.value) return
    choicesController = new AbortController()
    const signal = choicesController.signal
    choicesLoading.value = true
    choicesError.value = ''
    try {
      const [sourceItems, groupItems, proxyItems] = await Promise.all([api.getCandidates(signal), api.getGroups(signal), api.getProxyGroups(signal)])
      if (disposed || signal.aborted) return
      candidates.value = sourceItems
      groups.value = groupItems
      proxyGroups.value = proxyItems
    } catch (cause) {
      if (!disposed && !signal.aborted && !api.isKiloRequestCanceled(cause)) choicesError.value = api.kiloErrorMessage(cause, t('kilo.errors.choices'))
    } finally {
      choicesLoading.value = false
    }
  }
  function setAutoRefresh(value: boolean) {
    autoRefresh.value = value
    if (value) schedule()
    else { stopTimer(); controller?.abort(); batchController?.abort() }
  }
  function visibilityChanged() {
    if (document.visibilityState === 'hidden') {
      stopTimer()
      controller?.abort()
      batchController?.abort()
    } else {
      void refresh()
    }
  }
  onMounted(() => {
    disposed = false
    document.addEventListener('visibilitychange', visibilityChanged)
    void refresh()
    void loadChoices()
  })
  onBeforeUnmount(() => {
    disposed = true
    stopTimer()
    controller?.abort()
    choicesController?.abort()
    batchController?.abort()
    document.removeEventListener('visibilitychange', visibilityChanged)
  })
  return { overview, prepared, jobs, candidates, groups, proxyGroups, loading, choicesLoading, error, choicesError, batchLoading, batchError, autoRefresh, updatedAt, refresh, loadChoices, setAutoRefresh }
}
