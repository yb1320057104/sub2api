<script setup lang="ts">
import { computed, onBeforeUnmount, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import Toggle from '@/components/common/Toggle.vue'
import KiloTwoFAImport from '@/components/account/KiloTwoFAImport.vue'
import { canActivateKiloPrepared, canUnbindKiloPrepared, isReusableKiloPrepared } from './pool'
import { KiloRequestRecoveryError, reserveKiloRequest } from './requestIdentity'
import BaseDialog from '@/components/common/BaseDialog.vue'
import KiloPreparedTable from './KiloPreparedTable.vue'
import KiloSourceAccountManager from './KiloSourceAccountManager.vue'
import KiloStatusBadge from './KiloStatusBadge.vue'
import KiloJobsPanel from './KiloJobsPanel.vue'
import KiloJobProxyDialog from './KiloJobProxyDialog.vue'
import KiloImportDialog from './KiloImportDialog.vue'
import KiloBatchDialog from './KiloBatchDialog.vue'
import KiloOnboardingDialog from './KiloOnboardingDialog.vue'
import KiloDeviceDialog from './KiloDeviceDialog.vue'
import KiloOrganizationDialog from './KiloOrganizationDialog.vue'
import KiloEgressDialog from './KiloEgressDialog.vue'
import KiloRoutingDialog from './KiloRoutingDialog.vue'
import { useKiloOverview } from './useKiloOverview'
import * as api from '@/api/admin/kilo'
import type { KiloJob, KiloOnboardingBatchResult, KiloPrepared, KiloSubscriptionRecoveryAction } from '@/api/admin/kilo'

const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const onboardingBatchId = computed(() => typeof route.query.onboarding_batch_id === 'string' && /^[A-Za-z0-9-]{1,100}$/.test(route.query.onboarding_batch_id) ? route.query.onboarding_batch_id : '')
const { overview, prepared, jobs, candidates, groups, proxyGroups, loading, choicesLoading, error, choicesError, batchLoading, batchError, autoRefresh, updatedAt, refresh, loadChoices, setAutoRefresh } = useKiloOverview(() => onboardingBatchId.value)
const selectedIds = shallowRef<number[]>([])
const importOpen = shallowRef(false)
const onboardingOpen = shallowRef(false)
const twoFAOpen = shallowRef(false)
const twoFABusy = shallowRef(false)
const initialSlot = shallowRef<KiloPrepared | null>(null)
const unbindTarget = shallowRef<KiloPrepared | null>(null)
const unbindRequestId = shallowRef('')
const batchOpen = shallowRef(false)
const batchBindSources = shallowRef(false)
const deviceOpen = shallowRef(false)
const sourceManagerId = shallowRef<number | null>(null)
const organizationPrepared = shallowRef<KiloPrepared | null>(null)
const egressPrepared = shallowRef<KiloPrepared | null>(null)
const routingPrepared = shallowRef<KiloPrepared | null>(null)
const cancelTarget = shallowRef<KiloJob | null>(null)
const subscriptionRecovery = shallowRef<{ job: KiloJob; action: KiloSubscriptionRecoveryAction } | null>(null)
const reauthorizationConfirmed = shallowRef(false)
const proxyJob = shallowRef<KiloJob | null>(null)
const deleteTarget = shallowRef<{ kind: 'job'; item: KiloJob } | { kind: 'prepared'; item: KiloPrepared } | null>(null)
const busyJobId = shallowRef<number | null>(null)
const releaseTarget = shallowRef<KiloPrepared | null>(null)
const busyPreparedId = shallowRef<number | null>(null)
const refreshingIdentityId = shallowRef<number | null>(null)
const actionError = shallowRef('')
const notice = shallowRef('')
let actionController: AbortController | undefined
const activeTab = computed(() => route.query.tab === 'jobs' ? 'jobs' : 'prepared')
function queryId(value: unknown): number | null { const id = Number(value); return Number.isSafeInteger(id) && id > 0 ? id : null }
const sourceFocusId = computed(() => queryId(route.query.source_account_id))
const gatewayFocusId = computed(() => queryId(route.query.account_id))
const focusSource = computed(() => candidates.value.find(item => item.id === sourceFocusId.value))
const linkedPrepared = computed(() => prepared.value.find(item => sourceFocusId.value ? item.source_account_id === sourceFocusId.value : gatewayFocusId.value ? item.account_id === gatewayFocusId.value : false))
const tablePrepared = computed(() => gatewayFocusId.value ? prepared.value.filter(item => item.account_id === gatewayFocusId.value) : prepared.value)
const activeSourceJob = computed(() => jobs.value.find(item => item.source_account_id === sourceFocusId.value && !item.finished_at && ['queued', 'pending', 'running', 'binding', 'awaiting_authorization', 'verifying', 'needs_action', 'waiting'].includes(item.status)))
const visibleJobs = computed(() => jobs.value.filter(item => (!onboardingBatchId.value || item.batch_id === onboardingBatchId.value) && (!sourceFocusId.value || item.source_account_id === sourceFocusId.value) && (!gatewayFocusId.value || item.account_id === gatewayFocusId.value)))
const cancelName = computed(() => cancelTarget.value?.prepared_name || cancelTarget.value?.source_name || (cancelTarget.value?.source_account_id ? t('kilo.jobs.onboardingTitle', { id: cancelTarget.value.source_account_id }) : ''))
const deleteName = computed(() => {
  const target = deleteTarget.value
  if (!target) return ''
  return target.kind === 'prepared' ? target.item.name : target.item.prepared_name || target.item.source_name || t('kilo.jobs.onboardingTitle', { id: target.item.source_account_id ?? target.item.id })
})
const deleteRetainsIdentity = computed(() => {
  const target = deleteTarget.value
  return !!target && (target.kind === 'prepared' ? target.item.has_credential : !!target.item.credential_ready || !!target.item.prepared_id)
})
const actionBusy = computed(() => busyJobId.value !== null || busyPreparedId.value !== null)
const encryptionReady = computed(() => overview.value?.encryption_ready === true)
const recoveryIsCurrent = computed(() => {
  const target = subscriptionRecovery.value
  const current = target && jobs.value.find(job => job.id === target.job.id)
  return !!target && !!current && current.revision === target.job.revision && current.status === 'failed' && current.recovery_actions?.includes(target.action) === true
})
const recoveryIsReauthorization = computed(() => subscriptionRecovery.value?.action === 'reauthorize_subscription')
const canSubmitRecovery = computed(() => recoveryIsCurrent.value && !readOnly.value && encryptionReady.value && !actionBusy.value && (!recoveryIsReauthorization.value || reauthorizationConfirmed.value))
const readOnly = computed(() => !overview.value || !!error.value || !!onboardingBatchId.value && !!batchError.value)
const selectedPrepared = computed(() => selectedIds.value.flatMap(id => { const item = prepared.value.find(value => value.id === id); return item ? [item] : [] }))
const readyCount = computed(() => prepared.value.filter(canActivateKiloPrepared).length)
const idleCount = computed(() => prepared.value.filter(isReusableKiloPrepared).length)
const recoveringCount = computed(() => prepared.value.filter(item => item.binding_state === 'recovering').length)
const unbindingCount = computed(() => prepared.value.filter(item => item.binding_state === 'unbinding').length)
const bannedCount = computed(() => prepared.value.filter(item => item.identity_status === 'banned').length)
const retiredCount = computed(() => prepared.value.filter(item => item.identity_status === 'retired').length)
const boundCount = computed(() => prepared.value.filter(item => item.status === 'bound').length)
const awaitingCount = computed(() => jobs.value.filter(item => item.status === 'awaiting_authorization').length)
const failedCount = computed(() => jobs.value.filter(item => item.status === 'failed').length)
const updatedTime = computed(() => updatedAt.value?.toLocaleTimeString(locale.value) ?? '')
watch(prepared, items => {
  const ready = new Set(items.filter(canActivateKiloPrepared).map(item => item.id))
  if (!batchOpen.value) selectedIds.value = selectedIds.value.filter(id => ready.has(id))
})
function tab(value: 'prepared' | 'jobs') { void router.replace({ query: { ...route.query, tab: value } }) }
function openDevice() { deviceOpen.value = true }
function openTwoFA(slot: KiloPrepared | null = null) {
  if (slot && !isReusableKiloPrepared(slot)) return
  initialSlot.value = slot
  twoFAOpen.value = true
}
function closeTwoFA() { if (!twoFABusy.value) twoFAOpen.value = false }
async function twoFACreated(result: KiloOnboardingBatchResult) {
  twoFAOpen.value = false
  await onboardingCreated(result)
}
async function refreshIdentity(item: KiloPrepared) {
  if (busyPreparedId.value !== null || readOnly.value || !item.has_credential) return
  busyPreparedId.value = item.id
  refreshingIdentityId.value = item.id
  actionError.value = ''
  notice.value = ''
  const controller = new AbortController()
  actionController = controller
  try {
    await api.refreshIdentityPolicy(item.id, controller.signal)
    if (controller.signal.aborted) return
    await refresh()
    if (!controller.signal.aborted && !error.value) notice.value = t('kilo.pool.identityRefreshed')
  } catch (cause) {
    if (!controller.signal.aborted && !api.isKiloRequestCanceled(cause)) actionError.value = api.kiloErrorMessage(cause, t('kilo.errors.identityRefresh'))
  } finally {
    busyPreparedId.value = null
    refreshingIdentityId.value = null
  }
}
function openUnbind(item: KiloPrepared) { unbindTarget.value = item; unbindRequestId.value = ''; actionError.value = '' }
async function unbindGPT() {
  const item = unbindTarget.value
  if (!item || busyPreparedId.value !== null || readOnly.value || !canUnbindKiloPrepared(item)) return
  busyPreparedId.value = item.id
  actionError.value = ''
  notice.value = ''
  actionController = new AbortController()
  try {
    const identity = await reserveKiloRequest('unbind', { prepared_id: item.id, binding_revision: item.binding_revision ?? 0 })
    unbindRequestId.value = identity.requestId
    await api.unbindPrepared(item.id, identity.requestId, actionController.signal)
    identity.complete()
    unbindTarget.value = null
    notice.value = t(item.identity_mode === 'gpt_coupled' ? 'kilo.pool.unbindCoupledQueued' : 'kilo.pool.unbindQueued')
    await refresh()
  } catch (cause) {
    if (!api.isKiloRequestCanceled(cause)) actionError.value = cause instanceof KiloRequestRecoveryError ? t('kilo.errors.requestRecovery') : api.kiloErrorMessage(cause, t('kilo.pool.unbindFailed'))
  } finally { busyPreparedId.value = null }
}
function openOnboarding() { onboardingOpen.value = true; void loadChoices() }
async function onboardingCreated(result: KiloOnboardingBatchResult) {
  onboardingOpen.value = false
  notice.value = t('kilo.batch.created', { id: result.batch_id })
  await router.replace({ query: { tab: 'jobs', onboarding_batch_id: result.batch_id } })
  await refresh()
}
function showAllBatches() { void router.replace({ query: { ...route.query, onboarding_batch_id: undefined } }) }
function openBatch() { batchBindSources.value = false; batchOpen.value = true; void loadChoices() }
function pairPrepared(item: KiloPrepared) { selectedIds.value = [item.id]; batchBindSources.value = true; batchOpen.value = true; void loadChoices() }
function openRouting(item: KiloPrepared) { routingPrepared.value = item; void loadChoices() }
async function routingSaved() { routingPrepared.value = null; notice.value = t('common.saved'); await refresh() }
async function sourceSaved() { sourceManagerId.value = null; notice.value = t('common.saved'); await refresh() }
function clearFocus() { void router.replace({ query: { tab: activeTab.value } }) }
async function batchCreated(batchId: string) {
  batchOpen.value = false
  selectedIds.value = []
  notice.value = t('kilo.batch.created', { id: batchId })
  await router.replace({ query: { ...route.query, tab: 'jobs', onboarding_batch_id: undefined } })
  await refresh()
}
async function organizationSaved() { organizationPrepared.value = null; notice.value = t('common.saved'); await refresh() }
async function jobAction(job: KiloJob, action: 'retry' | 'cancel') {
  if (busyJobId.value !== null || readOnly.value) return
  busyJobId.value = job.id
  actionError.value = ''
  notice.value = ''
  actionController = new AbortController()
  try {
    if (action === 'retry') await api.retryJob(job.id, actionController.signal)
    else await api.cancelJob(job.id, actionController.signal)
    cancelTarget.value = null
    notice.value = action === 'retry' ? t('kilo.jobs.retried') : t('kilo.jobs.cancelled')
    await refresh()
  } catch (cause) {
    if (!api.isKiloRequestCanceled(cause)) actionError.value = api.kiloErrorMessage(cause, t('kilo.errors.jobAction'))
  } finally { busyJobId.value = null }
}
function openSubscriptionRecovery(job: KiloJob, action: KiloSubscriptionRecoveryAction) {
  if (readOnly.value || actionBusy.value || !encryptionReady.value || job.status !== 'failed' || !Number.isSafeInteger(job.revision) || job.revision <= 0 || !job.recovery_actions?.includes(action)) return
  subscriptionRecovery.value = { job: { ...job }, action }
  reauthorizationConfirmed.value = false
  actionError.value = ''
}
async function recoverSubscription() {
  const target = subscriptionRecovery.value
  if (!target || !canSubmitRecovery.value) return
  busyJobId.value = target.job.id
  actionError.value = ''
  notice.value = ''
  const controller = new AbortController()
  actionController = controller
  try {
    const input = { expected_revision: target.job.revision }
    if (target.action === 'reconcile_subscription') await api.reconcileSubscription(target.job.id, input, controller.signal)
    else await api.reauthorizeSubscription(target.job.id, input, controller.signal)
    if (controller.signal.aborted) return
    subscriptionRecovery.value = null
    notice.value = t(target.action === 'reconcile_subscription' ? 'kilo.jobs.recovery.reconcileQueued' : 'kilo.jobs.recovery.reauthorizeQueued')
    await refresh()
  } catch (cause) {
    if (!controller.signal.aborted && !api.isKiloRequestCanceled(cause)) {
      const message = api.kiloErrorMessage(cause, t('kilo.jobs.recovery.unconfirmed'))
      await refresh()
      if (!controller.signal.aborted) actionError.value = message
    }
  } finally { busyJobId.value = null }
}
function openDeleteJob(job: KiloJob) { deleteTarget.value = { kind: 'job', item: job }; actionError.value = '' }
function openDeletePrepared(item: KiloPrepared) { deleteTarget.value = { kind: 'prepared', item }; actionError.value = '' }
async function deleteFailed() {
  const target = deleteTarget.value
  if (!target || actionBusy.value || readOnly.value) return
  if (target.kind === 'job') busyJobId.value = target.item.id
  else busyPreparedId.value = target.item.id
  actionError.value = ''
  notice.value = ''
  const controller = new AbortController()
  actionController = controller
  try {
    if (target.kind === 'job') await api.deleteJob(target.item.id, controller.signal)
    else await api.deletePrepared(target.item.id, controller.signal)
    if (controller.signal.aborted) return
    if (target.kind === 'prepared') selectedIds.value = selectedIds.value.filter(id => id !== target.item.id)
    deleteTarget.value = null
    notice.value = t(target.kind === 'job' ? 'kilo.cleanup.jobDeleted' : 'kilo.cleanup.preparedDeleted')
    await refresh()
  } catch (cause) {
    if (!controller.signal.aborted && !api.isKiloRequestCanceled(cause)) actionError.value = api.kiloErrorMessage(cause, t('kilo.errors.cleanup'))
  } finally {
    busyJobId.value = null
    busyPreparedId.value = null
  }
}
async function jobProxySaved() { proxyJob.value = null; notice.value = t('kilo.jobs.proxySaved'); await refresh() }
async function releasePrepared() {
  const item = releaseTarget.value
  if (!item || busyPreparedId.value !== null || readOnly.value) return
  busyPreparedId.value = item.id
  actionError.value = ''
  notice.value = ''
  actionController = new AbortController()
  try {
    await api.releasePrepared(item.id, actionController.signal)
    releaseTarget.value = null
    notice.value = t('kilo.release.requested')
    await refresh()
  } catch (cause) {
    if (!api.isKiloRequestCanceled(cause)) {
      const message = api.kiloErrorMessage(cause, t('kilo.errors.preparedRelease'))
      await refresh()
      actionError.value = message
    }
  } finally { busyPreparedId.value = null }
}
onBeforeUnmount(() => actionController?.abort())
</script>

<template>
  <div class="mx-auto max-w-7xl space-y-6 text-gray-800 dark:text-dark-200">
    <header class="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between"><div class="min-w-0"><h1 class="text-2xl font-bold tracking-tight text-gray-900 dark:text-dark-100">{{ t('kilo.title') }}</h1><p class="mt-2 max-w-2xl text-sm leading-6 text-gray-500 dark:text-dark-400">{{ t('kilo.description') }}</p></div><div class="flex shrink-0 flex-wrap gap-2"><button type="button" class="btn btn-primary" :disabled="readOnly || !encryptionReady" data-testid="kilo-open-two-fa" @click="openTwoFA()">{{ t('kilo.twoFA.label') }}</button><button type="button" class="btn btn-secondary" :disabled="readOnly || !encryptionReady" @click="openOnboarding">{{ t('kilo.onboarding.action') }}</button><button type="button" class="btn btn-secondary" :disabled="readOnly || !encryptionReady" @click="openDevice()">{{ t('kilo.device.action') }}</button><button type="button" class="btn btn-primary" :disabled="readOnly || !encryptionReady" @click="importOpen = true">{{ t('kilo.import.title') }}</button></div></header>
    <section v-if="sourceFocusId || gatewayFocusId" class="space-y-3 rounded-xl border border-primary-200 bg-primary-50 p-4 dark:border-primary-900 dark:bg-primary-900/20" aria-live="polite">
      <div class="flex flex-wrap items-center justify-between gap-3"><h2 class="text-sm font-semibold">{{ sourceFocusId ? t('kilo.accountEntry.source', { name: focusSource?.name || linkedPrepared?.source_name || '', id: sourceFocusId }) : t('kilo.accountEntry.gateway', { id: gatewayFocusId }) }}</h2><button type="button" class="text-xs underline" @click="clearFocus">{{ t('kilo.accountEntry.showAll') }}</button></div>
      <p class="text-sm leading-6">{{ t('kilo.accountEntry.conversionHint') }}</p>
      <div v-if="linkedPrepared" class="flex flex-wrap items-center gap-3 text-sm"><span>{{ t('kilo.accountEntry.linked', { name: linkedPrepared.name, id: linkedPrepared.id }) }}</span><KiloStatusBadge :status="linkedPrepared.status" /><button v-if="['bound', 'disabled'].includes(linkedPrepared.status)" type="button" class="btn btn-secondary btn-sm" :disabled="readOnly || busyPreparedId !== null" @click="releaseTarget = linkedPrepared; actionError = ''">{{ t('kilo.accountEntry.exit') }}</button><button type="button" class="btn btn-secondary btn-sm" :disabled="readOnly || linkedPrepared.status === 'binding'" @click="openRouting(linkedPrepared)">{{ t('kilo.routing.action') }}</button></div>
      <p v-else-if="activeSourceJob" class="text-sm">{{ t('kilo.accountEntry.pending', { id: activeSourceJob.id }) }} <button type="button" class="underline" @click="tab('jobs')">{{ t('kilo.jobs.title') }}</button></p>
      <div v-else-if="sourceFocusId" class="space-y-3"><p class="text-sm leading-6">{{ t('kilo.accountEntry.choosePrepared') }}</p><button type="button" class="btn btn-primary btn-sm" :disabled="readOnly || !encryptionReady || choicesLoading" @click="openOnboarding">{{ t('kilo.onboarding.action') }}</button></div>
    </section>
    <div v-if="overview && !encryptionReady" class="rounded-xl border border-amber-200 bg-amber-50 p-4 text-sm leading-6 text-amber-800 dark:border-amber-900 dark:bg-amber-900/20 dark:text-amber-300" role="alert">{{ t('kilo.encryptionRequired') }}</div>
    <div class="flex flex-col gap-3 rounded-xl border border-primary-100 bg-primary-50/60 px-4 py-3 sm:flex-row sm:items-center sm:justify-between dark:border-primary-900/40 dark:bg-primary-900/10"><p class="text-sm font-medium text-primary-800 dark:text-primary-200">{{ t('kilo.egressRule') }}</p><span class="text-xs text-primary-700 dark:text-primary-300">{{ t('kilo.multiEgress') }}</span></div>
    <p class="rounded-lg border border-primary-100 bg-primary-50/60 p-3 text-sm leading-6 text-primary-800 dark:border-primary-900/40 dark:bg-primary-900/10 dark:text-primary-200">{{ t('kilo.inferenceBoundary') }}</p>
    <p class="text-sm leading-6 text-gray-600 dark:text-dark-300">{{ t('kilo.officialBoundary') }}</p>
    <section class="space-y-3 rounded-xl border border-gray-200 p-4 dark:border-dark-700" :aria-label="t('kilo.pool.title')"><div class="flex flex-wrap items-center gap-3 text-sm"><strong data-testid="kilo-pool-idle">{{ t('kilo.pool.idleCount', { count: idleCount }) }}</strong><span>{{ t('kilo.pool.binding.recovering') }} {{ recoveringCount }}</span><span>{{ t('kilo.pool.binding.unbinding') }} {{ unbindingCount }}</span><span>{{ t('kilo.pool.identity.banned') }} {{ bannedCount }}</span><span>{{ t('kilo.pool.identity.retired') }} {{ retiredCount }}</span></div><p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('kilo.pool.preferIdleHint') }} {{ t('kilo.pool.lifecycle') }}</p><p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('kilo.pool.modeHint.independent') }} {{ t('kilo.pool.modeHint.unknown') }}</p></section>
    <div v-if="error" class="flex flex-wrap items-center justify-between gap-3 rounded-lg bg-red-50 p-4 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300" role="alert"><span>{{ error }} {{ overview ? t('kilo.stale') : '' }}</span><button type="button" class="btn btn-secondary btn-sm" :disabled="loading" @click="refresh">{{ t('common.refresh') }}</button></div>
    <p v-if="actionError" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300" role="alert">{{ actionError }}</p>
    <p v-if="notice" class="rounded-lg bg-emerald-50 p-3 text-sm text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-300" role="status">{{ notice }}</p>
    <dl class="grid grid-cols-2 divide-x divide-gray-200 rounded-xl border border-gray-200 bg-white dark:divide-dark-700 dark:border-dark-700 dark:bg-dark-800 lg:grid-cols-4"><div class="p-4"><dt class="text-xs text-gray-500 dark:text-dark-400">{{ t('kilo.stats.ready') }}</dt><dd class="mt-2 text-2xl font-semibold tabular-nums">{{ overview ? readyCount : t('common.notAvailable') }}</dd></div><div class="p-4"><dt class="text-xs text-gray-500 dark:text-dark-400">{{ t('kilo.stats.bound') }}</dt><dd class="mt-2 text-2xl font-semibold tabular-nums">{{ overview ? boundCount : t('common.notAvailable') }}</dd></div><div class="border-t border-gray-200 p-4 dark:border-dark-700 lg:border-t-0"><dt class="text-xs text-gray-500 dark:text-dark-400">{{ t('kilo.stats.awaiting') }}</dt><dd class="mt-2 text-2xl font-semibold tabular-nums text-amber-700 dark:text-amber-300">{{ overview ? awaitingCount : t('common.notAvailable') }}</dd></div><div class="border-t border-gray-200 p-4 dark:border-dark-700 lg:border-t-0"><dt class="text-xs text-gray-500 dark:text-dark-400">{{ t('kilo.stats.failed') }}</dt><dd class="mt-2 text-2xl font-semibold tabular-nums text-red-600 dark:text-red-400">{{ overview ? failedCount : t('common.notAvailable') }}</dd></div></dl>
    <section class="card overflow-hidden" :aria-busy="loading">
      <div class="flex flex-col gap-3 border-b border-gray-200 p-4 sm:flex-row sm:items-center sm:justify-between dark:border-dark-700"><nav class="flex gap-2" :aria-label="t('kilo.workspace')"><button type="button" class="rounded-lg px-3 py-2 text-sm font-medium focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500" :class="activeTab === 'prepared' ? 'bg-primary-50 text-primary-700 dark:bg-primary-900/30 dark:text-primary-300' : 'text-gray-500 hover:bg-gray-50 dark:text-dark-400 dark:hover:bg-dark-700'" :aria-current="activeTab === 'prepared' ? 'page' : undefined" @click="tab('prepared')">{{ t('kilo.prepared.title') }} <span class="ml-1 tabular-nums">{{ prepared.length }}</span></button><button type="button" class="rounded-lg px-3 py-2 text-sm font-medium focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500" :class="activeTab === 'jobs' ? 'bg-primary-50 text-primary-700 dark:bg-primary-900/30 dark:text-primary-300' : 'text-gray-500 hover:bg-gray-50 dark:text-dark-400 dark:hover:bg-dark-700'" :aria-current="activeTab === 'jobs' ? 'page' : undefined" @click="tab('jobs')">{{ t('kilo.jobs.title') }} <span class="ml-1 tabular-nums">{{ jobs.length }}</span></button></nav><div class="flex flex-wrap items-center gap-3"><span v-if="updatedTime" class="text-xs text-gray-400 dark:text-dark-500">{{ t('kilo.updated', { time: updatedTime }) }}</span><label class="flex items-center gap-2 text-xs text-gray-500 dark:text-dark-400"><Toggle :model-value="autoRefresh" :aria-label="t('kilo.autoRefresh')" @update:model-value="setAutoRefresh" /><span>{{ t('kilo.autoRefresh') }}</span></label><button type="button" class="btn btn-secondary btn-sm" :disabled="loading" @click="refresh">{{ loading ? t('common.loading') : t('common.refresh') }}</button></div></div>
      <div class="p-4 sm:p-5"><KiloPreparedTable v-if="activeTab === 'prepared'" v-model:selected-ids="selectedIds" :items="tablePrepared" :jobs="jobs" :loading="loading" :identity-refresh-id="refreshingIdentityId" :disabled="readOnly || !encryptionReady || busyPreparedId !== null" @batch="openBatch" @organization="organizationPrepared = $event" @egress="egressPrepared = $event" @release="releaseTarget = $event; actionError = ''" @routing="openRouting" @pair="pairPrepared" @manage-source="sourceManagerId = $event" @import="importOpen = true" @unbind="openUnbind" @fill-slot="openTwoFA" @refresh-identity="refreshIdentity" @delete="openDeletePrepared" @change-proxy="proxyJob = $event" /><div v-else class="space-y-4"><div v-if="onboardingBatchId" class="flex flex-wrap items-center justify-between gap-3 rounded-lg bg-primary-50 p-3 text-xs text-primary-800 dark:bg-primary-900/20 dark:text-primary-200"><p class="break-all">{{ t('kilo.jobs.batchFocus', { id: onboardingBatchId }) }} <span v-if="batchLoading">{{ t('kilo.jobs.batchLoading') }}</span></p><button type="button" class="underline" @click="showAllBatches">{{ t('kilo.jobs.allBatches') }}</button></div><p v-if="batchError" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300" role="alert">{{ batchError }}</p><KiloJobsPanel :items="visibleJobs" :busy-id="busyJobId" :disabled="readOnly || busyPreparedId !== null" @retry="jobAction($event, 'retry')" @recover-subscription="openSubscriptionRecovery" @cancel="cancelTarget = $event" @delete="openDeleteJob" @change-proxy="proxyJob = $event" /></div></div>
    </section>
    <BaseDialog :show="twoFAOpen" :title="t('kilo.twoFA.title')" width="wide" :show-close-button="!twoFABusy" :close-on-escape="!twoFABusy" @close="closeTwoFA"><KiloTwoFAImport v-if="twoFAOpen" :initial-prepared="initialSlot" @busy="twoFABusy = $event" @created="twoFACreated" /><template #footer><button type="button" class="btn btn-secondary" :disabled="twoFABusy" @click="closeTwoFA">{{ t('common.close') }}</button></template></BaseDialog>
    <BaseDialog :show="!!unbindTarget" :title="t('kilo.pool.unbindTitle')" width="narrow" :show-close-button="busyPreparedId === null" :close-on-escape="busyPreparedId === null" @close="busyPreparedId === null && (unbindTarget = null)"><p class="text-sm leading-6 text-gray-600 dark:text-dark-300">{{ t(unbindTarget?.identity_mode === 'gpt_coupled' ? 'kilo.pool.unbindCoupledConfirmation' : 'kilo.pool.unbindConfirmation', { name: unbindTarget?.name ?? '' }) }}</p><p v-if="unbindRequestId" class="mt-3 break-all text-xs text-gray-500 dark:text-dark-400">{{ t('kilo.requestIdentity', { id: unbindRequestId }) }}</p><p v-if="actionError" class="mt-3 text-sm text-red-600 dark:text-red-400" role="alert">{{ actionError }}</p><template #footer><button type="button" class="btn btn-secondary" :disabled="busyPreparedId !== null" @click="unbindTarget = null">{{ t('common.cancel') }}</button><button type="button" class="btn btn-primary" :disabled="busyPreparedId !== null" data-testid="kilo-confirm-unbind" @click="unbindGPT">{{ busyPreparedId !== null ? t('common.processing') : t('kilo.pool.unbindConfirm') }}</button></template></BaseDialog>
    <KiloOnboardingDialog :show="onboardingOpen" :candidates="candidates" :groups="groups" :proxy-groups="proxyGroups" :choices-loading="choicesLoading" :choices-error="choicesError" :encryption-ready="encryptionReady" :initial-source-id="sourceFocusId" @close="onboardingOpen = false" @created="onboardingCreated" @reload-choices="loadChoices" />
    <KiloImportDialog :show="importOpen" :encryption-ready="encryptionReady" @close="importOpen = false" @imported="refresh" />
    <KiloBatchDialog :show="batchOpen" :prepared="selectedPrepared" :candidates="candidates" :groups="groups" :proxy-groups="proxyGroups" :choices-loading="choicesLoading" :choices-error="choicesError" :encryption-ready="encryptionReady" :initial-source-id="sourceFocusId" :initial-bind-sources="batchBindSources" @close="batchOpen = false" @created="batchCreated" @reload-choices="loadChoices" />
    <KiloDeviceDialog :show="deviceOpen" :encryption-ready="encryptionReady" @close="deviceOpen = false" @authorized="refresh" />
    <BaseDialog :show="!!subscriptionRecovery" :title="t(recoveryIsReauthorization ? 'kilo.jobs.recovery.reauthorizeTitle' : 'kilo.jobs.recovery.reconcileTitle')" width="narrow" :show-close-button="!actionBusy" :close-on-escape="!actionBusy" @close="!actionBusy && (subscriptionRecovery = null)">
      <template v-if="subscriptionRecovery">
        <p class="text-sm leading-6 text-gray-600 dark:text-dark-300">{{ t(recoveryIsReauthorization ? 'kilo.jobs.recovery.reauthorizeDescription' : 'kilo.jobs.recovery.reconcileDescription') }}</p>
        <p class="mt-3 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('kilo.jobs.recovery.originalJob', { id: subscriptionRecovery.job.id, revision: subscriptionRecovery.job.revision, source: subscriptionRecovery.job.source_account_id, proxy: subscriptionRecovery.job.login_proxy_id }) }}</p>
        <label v-if="recoveryIsReauthorization" class="mt-4 flex items-start gap-2 text-sm leading-6"><input id="kilo-reauthorization-confirmed" v-model="reauthorizationConfirmed" type="checkbox" class="mt-1 rounded border-gray-300" :disabled="actionBusy || !recoveryIsCurrent || readOnly" /><span>{{ t('kilo.jobs.recovery.authorizationConfirmation') }}</span></label>
        <p v-if="!recoveryIsCurrent" class="mt-3 text-sm text-amber-700 dark:text-amber-300" role="alert">{{ t('kilo.jobs.recovery.stale') }}</p>
        <p v-if="actionError" class="mt-3 text-sm text-red-600 dark:text-red-400" role="alert">{{ actionError }}</p>
      </template>
      <template #footer><button type="button" class="btn btn-secondary" :disabled="actionBusy" @click="subscriptionRecovery = null">{{ t('common.cancel') }}</button><button type="button" class="btn btn-primary" :disabled="!canSubmitRecovery" data-testid="kilo-confirm-subscription-recovery" @click="recoverSubscription">{{ t(actionBusy ? 'common.processing' : recoveryIsReauthorization ? 'kilo.jobs.recovery.confirmReauthorize' : 'kilo.jobs.recovery.confirmReconcile') }}</button></template>
    </BaseDialog>
    <KiloJobProxyDialog v-if="proxyJob" :key="proxyJob.id" :job="proxyJob" @close="proxyJob = null" @saved="jobProxySaved" />
    <BaseDialog :show="!!deleteTarget" :title="t('kilo.cleanup.title')" width="narrow" :show-close-button="!actionBusy" :close-on-escape="!actionBusy" @close="!actionBusy && (deleteTarget = null)"><p class="text-sm leading-6 text-gray-600 dark:text-dark-300">{{ t(deleteTarget?.kind === 'prepared' ? 'kilo.cleanup.preparedConfirmation' : 'kilo.cleanup.jobConfirmation', { name: deleteName }) }}</p><p class="mt-3 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t(deleteRetainsIdentity ? 'kilo.cleanup.retainedIdentity' : 'kilo.cleanup.safetyHint') }}</p><p v-if="actionError" class="mt-3 text-sm text-red-600 dark:text-red-400" role="alert">{{ actionError }}</p><template #footer><button type="button" class="btn btn-secondary" :disabled="actionBusy" @click="deleteTarget = null">{{ t('common.cancel') }}</button><button type="button" class="btn btn-primary" :disabled="actionBusy" data-testid="kilo-confirm-delete" @click="deleteFailed">{{ t(actionBusy ? 'common.processing' : 'kilo.cleanup.confirm') }}</button></template></BaseDialog>
    <KiloSourceAccountManager v-if="sourceManagerId" :key="sourceManagerId" :source-id="sourceManagerId" @close="sourceManagerId = null" @updated="sourceSaved" />
    <KiloOrganizationDialog v-if="organizationPrepared" :key="organizationPrepared.id" :prepared="organizationPrepared" @close="organizationPrepared = null" @saved="organizationSaved" />
    <KiloEgressDialog v-if="egressPrepared" :key="egressPrepared.id" :prepared="egressPrepared" @close="egressPrepared = null" />
    <KiloRoutingDialog v-if="routingPrepared" :key="routingPrepared.id" :prepared="routingPrepared" :proxy-groups="proxyGroups" :choices-loading="choicesLoading" :choices-error="choicesError" @close="routingPrepared = null" @saved="routingSaved" @reload-choices="loadChoices" />
    <BaseDialog :show="!!releaseTarget" :title="t('kilo.release.title')" width="narrow" :show-close-button="busyPreparedId === null" :close-on-escape="busyPreparedId === null" @close="busyPreparedId === null && (releaseTarget = null)"><p class="text-sm leading-6 text-gray-600 dark:text-dark-300">{{ t('kilo.release.confirmation', { name: releaseTarget?.name ?? '' }) }}</p><p v-if="actionError" class="mt-3 text-sm text-red-600 dark:text-red-400" role="alert">{{ actionError }}</p><template #footer><button type="button" class="btn btn-secondary" :disabled="busyPreparedId !== null" @click="releaseTarget = null">{{ t('common.cancel') }}</button><button type="button" class="btn btn-primary" :disabled="busyPreparedId !== null" @click="releasePrepared">{{ busyPreparedId !== null ? t('common.processing') : t('kilo.release.confirm') }}</button></template></BaseDialog>
    <BaseDialog :show="!!cancelTarget" :title="t('kilo.jobs.cancelTitle')" width="narrow" :show-close-button="busyJobId === null" :close-on-escape="busyJobId === null" @close="busyJobId === null && (cancelTarget = null)"><p class="text-sm leading-6 text-gray-600 dark:text-dark-300">{{ t('kilo.jobs.cancelConfirmation', { name: cancelName }) }}</p><template #footer><button type="button" class="btn btn-secondary" :disabled="busyJobId !== null" @click="cancelTarget = null">{{ t('common.back') }}</button><button type="button" class="btn btn-primary" :disabled="busyJobId !== null" @click="cancelTarget && jobAction(cancelTarget, 'cancel')">{{ busyJobId !== null ? t('common.processing') : t('kilo.jobs.confirmCancel') }}</button></template></BaseDialog>
  </div>
</template>
