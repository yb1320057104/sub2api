import { computed, onBeforeUnmount, reactive, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  createOnboardingBatch, getProxies, isKiloRequestCanceled, kiloErrorMessage,
  type KiloCandidate, type KiloGroup, type KiloOnboardingBatchInput,
  type KiloOnboardingBatchResult, type KiloProxy, type KiloProxyGroup, type KiloWorkspaceMode
} from '@/api/admin/kilo'
import { KiloRequestRecoveryError, reserveKiloRequest } from './requestIdentity'
import { KILO_ALL_REQUEST_PROXIES, kiloRequestProxyOptions, kiloRoutingOptions, parseAutoReleaseStatuses } from './routing'

export interface KiloOnboardingFormProps {
  show: boolean
  candidates: KiloCandidate[]
  groups: KiloGroup[]
  proxyGroups: KiloProxyGroup[]
  choicesLoading: boolean
  choicesError: string
  encryptionReady: boolean
  initialSourceId?: number | null
}

export interface KiloOnboardingDraft {
  source_account_id: number
  name: string
  login_proxy_id: number | null
  workspace_mode: KiloWorkspaceMode
  organization_id: string
  model_ids_input: string
}

function modelIds(input: string): string[] {
  return [...new Set(input.trim().split(/[\s,，]+/).filter(Boolean))]
}

export function useKiloOnboardingForm(props: KiloOnboardingFormProps, onCreated: (result: KiloOnboardingBatchResult) => void) {
  const { t } = useI18n()
  const rows = shallowRef<KiloOnboardingDraft[]>([])
  const search = shallowRef('')
  const groupIds = shallowRef<number[]>([])
  const routing = reactive({ ...kiloRoutingOptions(), statuses: '401, 402, 403' })
  const activateWhenReady = shallowRef(true)
  const confirmed = shallowRef(false)
  const sharedModels = shallowRef('')
  const proxies = shallowRef<KiloProxy[]>([])
  const proxiesLoading = shallowRef(false)
  const proxiesError = shallowRef('')
  const busy = shallowRef(false)
  const error = shallowRef('')
  const requestId = shallowRef('')
  let submitController: AbortController | undefined
  let proxyController: AbortController | undefined
  let generation = 0

  const sourceById = computed(() => new Map(props.candidates.map(source => [source.id, source])))
  const filteredSources = computed(() => {
    const query = search.value.trim().toLowerCase()
    return props.candidates.filter(source => !query || `${source.name} ${source.email} ${source.id}`.toLowerCase().includes(query))
  })
  const selectedIds = computed(() => new Set(rows.value.map(row => row.source_account_id)))
  const groupOptions = computed(() => props.groups.map(group => ({ value: group.id, label: group.name })))
  const proxyGroupOptions = computed(() => kiloRequestProxyOptions(props.proxyGroups, t))
  const selectedProxyGroup = computed(() => props.proxyGroups.find(group => group.id === routing.proxy_group_id))
  const statuses = computed(() => parseAutoReleaseStatuses(routing.statuses))
  const requiredProxies = computed(() => rows.value.length * Number(routing.proxies_per_account))
  const capacityWarning = computed(() => selectedProxyGroup.value && requiredProxies.value > selectedProxyGroup.value.active_proxy_count
    ? t('kilo.batch.capacityWarning', { required: requiredProxies.value, available: selectedProxyGroup.value.active_proxy_count }) : '')
  const validNumber = (value: number, min: number, max: number) => Number.isInteger(value) && value >= min && value <= max

  function sourceReason(source?: KiloCandidate): string {
    if (!source) return t('kilo.onboarding.sourceUnavailable')
    if (source.onboarding_reason?.trim()) return source.onboarding_reason
    if (!source.email.trim()) return t('kilo.batch.sourceMissingEmail')
    if (source.has_password === false || source.has_totp === false) return t('kilo.onboarding.materialMissing')
    return ''
  }
  function newRow(id: number): KiloOnboardingDraft {
    const source = sourceById.value.get(id)
    return { source_account_id: id, name: source ? `Kilo-${source.name || id}`.slice(0, 200) : '', login_proxy_id: source?.login_proxy_id ?? null, workspace_mode: 'auto', organization_id: '', model_ids_input: sharedModels.value }
  }
  function toggleSource(source: KiloCandidate) {
    if (busy.value) return
    if (selectedIds.value.has(source.id)) rows.value = rows.value.filter(row => row.source_account_id !== source.id)
    else if (rows.value.length < 100 && !sourceReason(source)) rows.value = [...rows.value, newRow(source.id)]
  }
  function selectVisible() {
    if (busy.value) return
    const added = filteredSources.value.filter(source => !selectedIds.value.has(source.id) && !sourceReason(source)).slice(0, 100 - rows.value.length)
    rows.value = [...rows.value, ...added.map(source => newRow(source.id))]
  }
  function updateRow(id: number, row: KiloOnboardingDraft) {
    if (!busy.value) rows.value = rows.value.map(item => item.source_account_id === id ? { ...row, source_account_id: id } : item)
  }
  function removeRow(id: number) {
    if (!busy.value) rows.value = rows.value.filter(row => row.source_account_id !== id)
  }
  function applyModels() {
    if (!busy.value) rows.value = rows.value.map(row => ({ ...row, model_ids_input: sharedModels.value }))
  }
  const validation = computed(() => {
    if (!props.encryptionReady) return t('kilo.encryptionRequired')
    if (props.choicesError) return props.choicesError
    if (proxiesError.value) return proxiesError.value
    if (!rows.value.length || rows.value.length > 100) return t('kilo.onboarding.limit')
    for (const row of rows.value) {
      const source = sourceById.value.get(row.source_account_id)
      const reason = sourceReason(source)
      if (reason) return t('kilo.onboarding.rowIssue', { id: row.source_account_id, reason })
      if (!row.name.trim() || row.name.trim().length > 200) return t('kilo.onboarding.nameRequired')
      if (!['auto', 'personal', 'business'].includes(row.workspace_mode)) return t('kilo.workspaceMode.invalid')
      if (row.organization_id.trim().length > 100) return t('kilo.twoFA.organizationInvalid')
      if (!row.login_proxy_id) return t('kilo.onboarding.loginRequired')
      if (source?.login_proxy_id && source.login_proxy_id !== row.login_proxy_id) return t('kilo.onboarding.proxyConflict')
      if (!proxiesLoading.value && !proxies.value.some(proxy => proxy.id === row.login_proxy_id && proxy.status === 'active')) return t('kilo.onboarding.proxyUnavailable')
      const models = modelIds(row.model_ids_input)
      if (models.some(model => !model.startsWith('openai/') || model.length <= 7 || model.length > 200 || /[*?]/.test(model))) return t('kilo.onboarding.invalidModels')
      // Empty keeps Kilo's authenticated BYOK catalog; explicit IDs narrow it.
    }
    const loginIds = rows.value.map(row => row.login_proxy_id)
    if (new Set(loginIds).size !== loginIds.length) return t('kilo.onboarding.proxyDuplicate')
    if (statuses.value === null) return t('kilo.routing.invalidStatuses')
    if (activateWhenReady.value && !groupIds.value.length) return t('kilo.batch.groupsRequired')
    if (activateWhenReady.value && routing.proxy_group_id !== KILO_ALL_REQUEST_PROXIES && !selectedProxyGroup.value) return t('kilo.batch.proxyRequired')
    if (!validNumber(routing.proxies_per_account, 1, 100) || !validNumber(routing.proxy_concurrency, 1, 10000) || !validNumber(routing.account_concurrency, 1, 10000) || !validNumber(routing.priority, 0, 10000)) return t('kilo.batch.invalidNumbers')
    return ''
  })

  async function loadProxies() {
    if (!props.show) return
    proxyController?.abort()
    const controller = new AbortController()
    proxyController = controller
    proxiesLoading.value = true
    proxiesError.value = ''
    try {
      const values = await getProxies(controller.signal)
      if (!controller.signal.aborted) proxies.value = values
    } catch (cause) {
      if (!controller.signal.aborted && !isKiloRequestCanceled(cause)) proxiesError.value = kiloErrorMessage(cause, t('kilo.errors.proxies'))
    } finally {
      if (proxyController === controller) proxiesLoading.value = false
    }
  }
  watch([() => props.show, () => props.initialSourceId], ([show]) => {
    generation++
    submitController?.abort()
    proxyController?.abort()
    busy.value = false
    proxiesLoading.value = false
    if (!show) return
    sharedModels.value = ''
    rows.value = props.initialSourceId ? [newRow(props.initialSourceId)] : []
    search.value = ''
    groupIds.value = []
    Object.assign(routing, kiloRoutingOptions(), { statuses: '401, 402, 403' })
    activateWhenReady.value = true
    confirmed.value = false
    error.value = ''
    requestId.value = ''
    proxies.value = []
    void loadProxies()
  }, { immediate: true })
  watch(() => props.candidates, () => {
    if (!props.show || busy.value) return
    rows.value = rows.value.map(row => {
      const source = sourceById.value.get(row.source_account_id)
      if (!source) return row
      const name = row.name || `Kilo-${source.name || source.id}`.slice(0, 200)
      return { ...row, name, login_proxy_id: source.login_proxy_id || row.login_proxy_id }
    })
  })
  watch([rows, groupIds, routing, activateWhenReady], () => { confirmed.value = false; requestId.value = '' }, { deep: true })

  async function submit() {
    if (busy.value || props.choicesLoading || proxiesLoading.value || validation.value || !confirmed.value || statuses.value === null) return
    const currentGeneration = generation
    const controller = new AbortController()
    submitController = controller
    busy.value = true
    error.value = ''
    try {
      const payload: Omit<KiloOnboardingBatchInput, 'request_id'> = {
        items: rows.value.map(row => ({ source_account_id: row.source_account_id, name: row.name.trim(), login_proxy_id: row.login_proxy_id!, workspace_mode: row.workspace_mode, organization_id: row.organization_id.trim(), verified_model_ids: modelIds(row.model_ids_input).sort() })),
        group_ids: [...groupIds.value].sort((a, b) => a - b),
        routing: { proxy_group_id: routing.proxy_group_id || 0, proxies_per_account: routing.proxies_per_account, proxy_concurrency: routing.proxy_concurrency, account_concurrency: routing.account_concurrency, priority: routing.priority, auto_release_statuses: statuses.value },
        activate_when_ready: activateWhenReady.value
      }
      const identity = await reserveKiloRequest('onboarding', payload)
      requestId.value = identity.requestId
      if (controller.signal.aborted) return
      const result = await createOnboardingBatch({ request_id: identity.requestId, ...payload }, controller.signal)
      if (controller.signal.aborted || currentGeneration !== generation) return
      identity.complete()
      onCreated(result)
    } catch (cause) {
      if (!controller.signal.aborted && currentGeneration === generation && !isKiloRequestCanceled(cause)) error.value = cause instanceof KiloRequestRecoveryError ? t('kilo.errors.requestRecovery') : kiloErrorMessage(cause, t('kilo.errors.onboarding'))
    } finally {
      if (currentGeneration === generation) busy.value = false
    }
  }
  onBeforeUnmount(() => { generation++; submitController?.abort(); proxyController?.abort() })

  return { rows, search, groupIds, routing, activateWhenReady, confirmed, sharedModels, proxies, proxiesLoading, proxiesError, busy, error, requestId, sourceById, filteredSources, selectedIds, groupOptions, proxyGroupOptions, capacityWarning, validation, sourceReason, toggleSource, selectVisible, updateRow, removeRow, applyModels, loadProxies, submit }
}
