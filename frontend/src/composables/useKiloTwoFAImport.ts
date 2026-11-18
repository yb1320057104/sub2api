import { computed, onBeforeUnmount, onMounted, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { parseTwoFALoginText, type TokenGuardReloginAccount } from '@/api/admin/accountTokenGuard'
import {
  getGroups, getOverview, getProxies, getProxyGroups, importOnboarding,
  isKiloRequestCanceled, kiloErrorMessage,
  type KiloBindingStrategy, type KiloGroup, type KiloOnboardingBatchResult,
  type KiloOnboardingImportInput, type KiloPrepared, type KiloProxy, type KiloProxyGroup, type KiloWorkspaceMode
} from '@/api/admin/kilo'
import { KILO_ALL_REQUEST_PROXIES, kiloRoutingOptions } from '@/components/admin/kilo/routing'
import { isReusableKiloPrepared } from '@/components/admin/kilo/pool'
import { KiloRequestRecoveryError, reserveKiloRequest } from '@/components/admin/kilo/requestIdentity'

export interface KiloTwoFADraft {
  login: TokenGuardReloginAccount
  name: string
  login_proxy_id: number | null
  workspace_mode: KiloWorkspaceMode
  organization_id: string
  models_input: string
  prepared_id: number | null
}

export interface KiloTwoFAImportProps { initialPrepared?: KiloPrepared | null }

export function useKiloTwoFAImport(props: KiloTwoFAImportProps, onCreated: (result: KiloOnboardingBatchResult) => void) {
  const { t } = useI18n()
  const raw = shallowRef('')
  const rows = shallowRef<KiloTwoFADraft[]>([])
  const strategy = shallowRef<KiloBindingStrategy>('prefer_idle')
  const groupIds = shallowRef<number[]>([])
  const routing = shallowRef(kiloRoutingOptions())
  const activateWhenReady = shallowRef(true)
  const encryptionReady = shallowRef(false)
  const prepared = shallowRef<KiloPrepared[]>([])
  const groups = shallowRef<KiloGroup[]>([])
  const proxyGroups = shallowRef<KiloProxyGroup[]>([])
  const proxies = shallowRef<KiloProxy[]>([])
  const choicesLoading = shallowRef(false)
  const choicesError = shallowRef('')
  const busy = shallowRef(false)
  const error = shallowRef('')
  const requestId = shallowRef('')
  const result = shallowRef<KiloOnboardingBatchResult | null>(null)
  let choicesController: AbortController | undefined
  let submitController: AbortController | undefined
  let disposed = false

  const reusable = computed(() => prepared.value.filter(isReusableKiloPrepared))
  const models = (input: string) => [...new Set(input.trim().split(/[\s,，]+/).filter(Boolean))].sort()
  const integer = (value: number, min: number, max: number) => Number.isInteger(value) && value >= min && value <= max
  const selectedProxyGroup = computed(() => proxyGroups.value.find(item => item.id === routing.value.proxy_group_id))
  const capacityWarning = computed(() => {
    const required = rows.value.length * routing.value.proxies_per_account
    return selectedProxyGroup.value && required > selectedProxyGroup.value.active_proxy_count
      ? t('kilo.batch.capacityWarning', { required, available: selectedProxyGroup.value.active_proxy_count }) : ''
  })
  const validation = computed(() => {
    if (!encryptionReady.value) return t('kilo.encryptionRequired')
    if (choicesError.value) return choicesError.value
    if (!rows.value.length || rows.value.length > 100) return t('kilo.twoFA.credentialsRequired')
    const selectedSlots = new Set<number>()
    const fallbackProxies = new Set<number>()
    for (const row of rows.value) {
      if (row.name.trim().length > 200) return t('kilo.twoFA.nameTooLong')
      if (!['auto', 'personal', 'business'].includes(row.workspace_mode)) return t('kilo.workspaceMode.invalid')
      if (row.organization_id.trim().length > 100) return t('kilo.twoFA.organizationInvalid')
      if (models(row.models_input).some(model => !model.startsWith('openai/') || model.length <= 7 || model.length > 200 || /[*?]/.test(model))) return t('kilo.onboarding.invalidModels')
      if (row.prepared_id) {
        const slot = reusable.value.find(item => item.id === row.prepared_id)
        if (strategy.value !== 'prefer_idle' || !slot || selectedSlots.has(slot.id) || !slot.login_proxy_id) return t('kilo.twoFA.slotUnavailable')
        selectedSlots.add(slot.id)
      } else {
        const proxy = proxies.value.find(item => item.id === row.login_proxy_id)
        if (!proxy || proxy.status !== 'active' || proxy.owned || fallbackProxies.has(proxy.id)) return t('kilo.twoFA.fallbackRequired')
        fallbackProxies.add(proxy.id)
      }
    }
    if (!groupIds.value.length) return t('kilo.batch.groupsRequired')
    if (routing.value.proxy_group_id !== KILO_ALL_REQUEST_PROXIES && !selectedProxyGroup.value) return t('kilo.batch.proxyRequired')
    const value = routing.value
    if (!integer(value.proxies_per_account, 1, 100) || !integer(value.proxy_concurrency, 1, 10000) || !integer(value.account_concurrency, 1, 10000) || !integer(value.priority, 0, 10000)) return t('kilo.batch.invalidNumbers')
    return ''
  })

  async function loadChoices() {
    if (choicesLoading.value || disposed) return
    const controller = new AbortController()
    choicesController = controller
    choicesLoading.value = true
    choicesError.value = ''
    try {
      const [overview, groupItems, proxyGroupItems, proxyItems] = await Promise.all([
        getOverview(controller.signal), getGroups(controller.signal), getProxyGroups(controller.signal), getProxies(controller.signal)
      ])
      if (controller.signal.aborted || disposed) return
      prepared.value = overview.prepared
      groups.value = groupItems
      proxyGroups.value = proxyGroupItems
      proxies.value = proxyItems
    } catch (cause) {
      if (!controller.signal.aborted && !disposed && !isKiloRequestCanceled(cause)) choicesError.value = kiloErrorMessage(cause, t('kilo.errors.choices'))
    } finally {
      if (choicesController === controller) choicesLoading.value = false
    }
  }

  function preview() {
    if (busy.value) return
    error.value = ''
    try {
      const entries = parseTwoFALoginText(raw.value)
      if (props.initialPrepared && entries.length !== 1) { error.value = t('kilo.twoFA.singleSlot'); return }
      rows.value = entries.map(login => ({
        login, name: `Kilo-${login.email}`.slice(0, 200), login_proxy_id: props.initialPrepared?.login_proxy_id ?? null,
        workspace_mode: 'auto', organization_id: props.initialPrepared?.organization_id ?? '', models_input: '', prepared_id: props.initialPrepared?.id ?? null
      }))
      raw.value = ''
    } catch { error.value = t('kilo.twoFA.invalidCredentials') }
  }
  function updateRow(email: string, value: KiloTwoFADraft) {
    if (!busy.value) rows.value = rows.value.map(row => row.login.email === email ? { ...value, login: row.login } : row)
  }
  function reset() {
    if (busy.value) return
    rows.value.forEach(row => { row.login.password = ''; row.login.mfa_secret = '' })
    rows.value = []
    raw.value = ''
    error.value = ''
  }
  watch(strategy, value => {
    if (value === 'new_identity') rows.value = rows.value.map(row => ({ ...row, prepared_id: null, login_proxy_id: null }))
  })
  watch([rows, strategy, groupIds, routing, activateWhenReady], () => { requestId.value = '' }, { deep: true })

  async function submit() {
    if (busy.value || choicesLoading.value || validation.value || result.value) return
    const controller = new AbortController()
    submitController = controller
    busy.value = true
    error.value = ''
    try {
      const payload: Omit<KiloOnboardingImportInput, 'request_id'> = {
        binding_strategy: strategy.value,
        items: rows.value.map(row => ({
          email: row.login.email, password: row.login.password, totp_secret: row.login.mfa_secret,
          ...(row.name.trim() ? { name: row.name.trim() } : {}),
          login_proxy_id: row.prepared_id ? reusable.value.find(item => item.id === row.prepared_id)!.login_proxy_id! : row.login_proxy_id!,
          workspace_mode: row.workspace_mode, organization_id: row.organization_id.trim(), verified_model_ids: models(row.models_input),
          ...(row.prepared_id ? { prepared_id: row.prepared_id } : {})
        })),
        group_ids: [...groupIds.value].sort((a, b) => a - b), routing: { ...routing.value }, activate_when_ready: activateWhenReady.value
      }
      const identity = await reserveKiloRequest('onboarding-import', payload)
      requestId.value = identity.requestId
      if (controller.signal.aborted || disposed) return
      const receipt = await importOnboarding({ request_id: identity.requestId, ...payload }, controller.signal)
      if (controller.signal.aborted || disposed) return
      identity.complete()
      rows.value.forEach(row => { row.login.password = ''; row.login.mfa_secret = '' })
      rows.value = []
      result.value = receipt
      busy.value = false
      onCreated(receipt)
    } catch (cause) {
      if (!controller.signal.aborted && !disposed && !isKiloRequestCanceled(cause)) error.value = cause instanceof KiloRequestRecoveryError ? t('kilo.errors.requestRecovery') : kiloErrorMessage(cause, t('kilo.errors.onboarding'))
    } finally {
      if (!disposed) busy.value = false
    }
  }
  onMounted(() => { void loadChoices() })
  onBeforeUnmount(() => {
    disposed = true
    choicesController?.abort()
    submitController?.abort()
    raw.value = ''
    rows.value.forEach(row => { row.login.password = ''; row.login.mfa_secret = '' })
    rows.value = []
  })

  return { raw, rows, strategy, groupIds, routing, activateWhenReady, encryptionReady, prepared, reusable, groups, proxyGroups, proxies, choicesLoading, choicesError, busy, error, requestId, result, validation, capacityWarning, loadChoices, preview, updateRow, reset, submit }
}
