<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import Toggle from '@/components/common/Toggle.vue'
import KiloProxySelect from './KiloProxySelect.vue'
import { KILO_ALL_REQUEST_PROXIES, kiloRequestProxyOptions, kiloRoutingOptions, parseAutoReleaseStatuses } from './routing'
import { canActivateKiloPrepared } from './pool'
import { createBatch, isKiloRequestCanceled, kiloErrorMessage } from '@/api/admin/kilo'
import type { KiloCandidate, KiloGroup, KiloPrepared, KiloProxyGroup } from '@/api/admin/kilo'
import { KiloRequestRecoveryError, reserveKiloRequest } from './requestIdentity'

const props = defineProps<{ show: boolean; prepared: KiloPrepared[]; candidates: KiloCandidate[]; groups: KiloGroup[]; proxyGroups: KiloProxyGroup[]; choicesLoading: boolean; choicesError: string; encryptionReady: boolean; initialSourceId?: number | null; initialBindSources?: boolean }>()
const emit = defineEmits<{ close: []; created: [batchId: string]; reloadChoices: [] }>()
const { t } = useI18n()
const bindSources = shallowRef(false)
const selectedSources = shallowRef<number[]>([])
const groupIds = shallowRef<number[]>([])
const proxyGroupId = shallowRef<number | null>(null)
const proxiesPerAccount = shallowRef(5)
const proxyConcurrency = shallowRef(20)
const accountConcurrency = shallowRef(100)
const loginProxyIds = reactive<Record<number, number | null>>({})
const autoReleaseInput = shallowRef('401, 402, 403')
const autoReleaseStatuses = computed(() => parseAutoReleaseStatuses(autoReleaseInput.value))
const priority = shallowRef(1)
const search = shallowRef('')
const confirmed = shallowRef(false)
const byokEnabledConfirmed = shallowRef(false)
const busy = shallowRef(false)
const error = shallowRef('')
const requestId = shallowRef('')
let controller: AbortController | undefined
const sources = computed(() => props.candidates.filter(item => `${item.name} ${item.email} ${item.id}`.toLowerCase().includes(search.value.trim().toLowerCase())))
const sourceById = computed(() => new Map(props.candidates.map(item => [item.id, item])))
const proxyOptions = computed(() => kiloRequestProxyOptions(props.proxyGroups, t))
const groupOptions = computed(() => props.groups.map(item => ({ value: item.id, label: item.name })))
const selectedProxyGroup = computed(() => props.proxyGroups.find(item => item.id === proxyGroupId.value))
const requiredProxies = computed(() => props.prepared.length * Number(proxiesPerAccount.value))
const validNumber = (value: number, min: number, max: number) => Number.isInteger(value) && value >= min && value <= max
const validation = computed(() => {
  if (!props.encryptionReady) return t('kilo.encryptionRequired')
  if (props.choicesError) return props.choicesError
  if (!props.prepared.length || props.prepared.length > 1000 || props.prepared.some(item => !canActivateKiloPrepared(item))) return t('kilo.batch.notReady')
  if (props.initialSourceId && props.prepared.length !== 1) return t('kilo.accountEntry.selectOne')
  if (props.prepared.some(item => !item.login_proxy_id && !loginProxyIds[item.id])) return t('kilo.routing.loginRequired')
  if (autoReleaseStatuses.value === null) return t('kilo.routing.invalidStatuses')
  if (proxyGroupId.value !== KILO_ALL_REQUEST_PROXIES && !selectedProxyGroup.value) return t('kilo.batch.proxyRequired')
  if (!groupIds.value.length) return t('kilo.batch.groupsRequired')
  if (!validNumber(proxiesPerAccount.value, 1, 100) || !validNumber(proxyConcurrency.value, 1, 10000) || !validNumber(accountConcurrency.value, 1, 10000) || !validNumber(priority.value, 0, 10000)) return t('kilo.batch.invalidNumbers')
  if (bindSources.value && selectedSources.value.length !== props.prepared.length) return t('kilo.batch.countMismatch', { prepared: props.prepared.length, sources: selectedSources.value.length })
  if (bindSources.value && selectedSources.value.some(id => !sourceById.value.get(id)?.email?.trim())) return t('kilo.batch.sourceUnavailable')
  if (bindSources.value) {
    for (const [index, item] of props.prepared.entries()) {
      if (item.identity_mode === 'independent') continue
      if (item.identity_mode !== 'gpt_coupled') return t('kilo.pool.modeHint.unknown')
      // Pairing is ordered; a coupled identity can only return to its original GPT source.
      if (!item.identity_source_account_id || item.identity_source_account_id !== selectedSources.value[index]) return t('kilo.pool.modeHint.gpt_coupled')
    }
  }
  if (bindSources.value && !byokEnabledConfirmed.value) return t('kilo.batch.byokEnabledRequired')
  return ''
})
const capacityWarning = computed(() => selectedProxyGroup.value && requiredProxies.value > selectedProxyGroup.value.active_proxy_count ? t('kilo.batch.capacityWarning', { required: requiredProxies.value, available: selectedProxyGroup.value.active_proxy_count }) : '')
const pairs = computed(() => props.prepared.map((item, index) => ({ prepared: item, source: bindSources.value ? sourceById.value.get(selectedSources.value[index] ?? -1) : undefined })))

function toggleSource(id: number) {
  selectedSources.value = selectedSources.value.includes(id) ? selectedSources.value.filter(value => value !== id) : [...selectedSources.value, id]
}
function selectVisible() {
  const values = new Set(selectedSources.value)
  sources.value.filter(item => item.email.trim()).forEach(item => values.add(item.id))
  selectedSources.value = [...values]
}
watch(() => props.show, show => {
  if (show) {
    const options = kiloRoutingOptions(props.prepared[0]?.routing_options)
    bindSources.value = !!props.initialSourceId || !!props.initialBindSources; selectedSources.value = props.initialSourceId ? [props.initialSourceId] : []; groupIds.value = []; proxyGroupId.value = options.proxy_group_id || null
    proxiesPerAccount.value = options.proxies_per_account; proxyConcurrency.value = options.proxy_concurrency; accountConcurrency.value = options.account_concurrency; priority.value = options.priority
    autoReleaseInput.value = options.auto_release_statuses.join(', ')
    for (const key of Object.keys(loginProxyIds)) delete loginProxyIds[Number(key)]
    for (const item of props.prepared) loginProxyIds[item.id] = item.login_proxy_id ?? null
    search.value = ''; confirmed.value = false; byokEnabledConfirmed.value = false; error.value = ''; requestId.value = ''
  } else controller?.abort()
})
watch([bindSources, selectedSources, () => props.prepared.map(item => item.id).join(',')], () => { byokEnabledConfirmed.value = false })
watch([bindSources, selectedSources, groupIds, proxyGroupId, proxiesPerAccount, proxyConcurrency, accountConcurrency, priority, autoReleaseInput, byokEnabledConfirmed, () => ({ ...loginProxyIds })], () => { confirmed.value = false; requestId.value = '' })
async function submit() {
  if (busy.value || props.choicesLoading || validation.value || !confirmed.value || !proxyGroupId.value) return
  busy.value = true
  error.value = ''
  controller = new AbortController()
  try {
    const payload = { prepared_ids: props.prepared.map(item => item.id), source_account_ids: bindSources.value ? [...selectedSources.value] : [], byok_enabled_confirmed: bindSources.value && byokEnabledConfirmed.value, group_ids: [...groupIds.value], proxy_group_id: proxyGroupId.value, proxies_per_account: proxiesPerAccount.value, proxy_concurrency: proxyConcurrency.value, account_concurrency: accountConcurrency.value, priority: priority.value, auto_release_statuses: autoReleaseStatuses.value!, login_proxy_ids: Object.fromEntries(props.prepared.filter(item => !item.login_proxy_id && loginProxyIds[item.id]).map(item => [item.id, loginProxyIds[item.id]!])) }
    const identity = await reserveKiloRequest('batch', payload)
    requestId.value = identity.requestId
    if (controller.signal.aborted) return
    const result = await createBatch({ request_id: identity.requestId, ...payload }, controller.signal)
    identity.complete()
    emit('created', result.batch_id)
  } catch (cause) {
    if (!isKiloRequestCanceled(cause)) error.value = cause instanceof KiloRequestRecoveryError ? t('kilo.errors.requestRecovery') : kiloErrorMessage(cause, t('kilo.errors.batch'))
  } finally { busy.value = false }
}
onBeforeUnmount(() => controller?.abort())
</script>

<template>
  <BaseDialog :show="show" :title="t('kilo.batch.title')" width="wide" :show-close-button="!busy" :close-on-escape="!busy" @close="!busy && emit('close')">
    <form id="kilo-batch-form" class="space-y-5" @submit.prevent="submit">
      <p class="text-sm leading-6 text-gray-600 dark:text-dark-300">{{ t('kilo.batch.description', { count: prepared.length }) }}</p>
      <p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('kilo.officialBoundary') }}</p>
      <p v-if="requestId" class="break-all text-xs leading-5 text-gray-500 dark:text-dark-400" role="status">{{ t('kilo.requestIdentity', { id: requestId }) }}</p>
      <p v-if="initialSourceId" class="rounded-lg bg-primary-50 p-3 text-sm leading-6 text-primary-800 dark:bg-primary-900/20 dark:text-primary-200">{{ t('kilo.accountEntry.conversionHint') }}</p>
      <h3 class="text-sm font-medium">{{ t('kilo.batch.mode') }}</h3>
      <div class="flex items-start gap-3 rounded-lg border border-gray-200 p-3 dark:border-dark-700">
        <Toggle id="kilo-bind-sources" v-model="bindSources" :disabled="busy || !!initialSourceId" :aria-label="t('kilo.batch.bindSources')" />
        <div><label for="kilo-bind-sources" class="text-sm font-medium text-gray-800 dark:text-dark-100">{{ t('kilo.batch.bindSources') }}</label><p class="mt-1 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ bindSources ? t('kilo.batch.authorizationHint') : t('kilo.batch.activationHint') }}</p></div>
      </div>
      <div v-if="bindSources && !initialSourceId" class="space-y-3">
        <label for="kilo-source-search" class="block text-sm font-medium">{{ t('kilo.batch.sources') }}</label>
        <input id="kilo-source-search" v-model="search" type="search" class="input w-full" :placeholder="t('kilo.batch.sourceSearch')" :disabled="busy" />
        <div class="flex flex-wrap items-center justify-between gap-2 text-xs"><span class="text-gray-500 dark:text-dark-400">{{ t('kilo.batch.sourceCount', { count: selectedSources.length, required: prepared.length }) }}</span><div class="flex gap-2"><button type="button" class="btn btn-secondary btn-sm" :disabled="busy || !sources.length" @click="selectVisible">{{ t('kilo.selectVisible') }}</button><button type="button" class="btn btn-secondary btn-sm" :disabled="busy || !selectedSources.length" @click="selectedSources = []">{{ t('common.clear') }}</button></div></div>
        <div class="max-h-52 overflow-auto rounded-lg border border-gray-200 dark:border-dark-700">
          <p v-if="!sources.length" class="p-4 text-sm text-gray-500 dark:text-dark-400">{{ choicesLoading ? t('common.loading') : t('kilo.batch.noSources') }}</p>
          <label v-for="source in sources" :key="source.id" class="flex min-h-11 cursor-pointer items-center gap-3 border-b border-gray-100 px-3 py-2 last:border-0 hover:bg-gray-50 dark:border-dark-700 dark:hover:bg-dark-700/50"><input type="checkbox" class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500" :checked="selectedSources.includes(source.id)" :disabled="busy || !source.email.trim()" @change="toggleSource(source.id)" /><span class="min-w-0 text-sm"><span class="break-words font-medium">{{ source.name }} <span class="text-gray-400">#{{ source.id }}</span></span><span class="block break-all text-xs text-gray-500 dark:text-dark-400">{{ source.email || t('kilo.batch.sourceMissingEmail') }}</span></span></label>
        </div>
      </div>
      <section class="space-y-3 rounded-lg border border-gray-200 p-3 dark:border-dark-700">
        <h3 class="text-sm font-medium">{{ t('kilo.routing.loginProxy') }}</h3>
        <p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('kilo.routing.loginHint') }}</p>
        <div v-for="item in prepared" :key="item.id" class="space-y-2">
          <label :for="`kilo-batch-login-${item.id}`" class="block text-sm font-medium">{{ item.name }} #{{ item.id }}</label>
          <p v-if="item.login_proxy_id" class="text-xs text-gray-600 dark:text-dark-300">{{ t('kilo.routing.loginIdentity', { name: item.login_proxy_name || '', id: item.login_proxy_id }) }}</p>
          <KiloProxySelect v-else :id="`kilo-batch-login-${item.id}`" v-model="loginProxyIds[item.id]" :account-id="item.account_id" :disabled="busy" />
        </div>
      </section>
      <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <div><label for="kilo-batch-proxy" class="mb-2 block text-sm font-medium">{{ t('kilo.batch.proxyGroup') }}</label><Select id="kilo-batch-proxy" v-model="proxyGroupId" :options="proxyOptions" :disabled="busy || choicesLoading" :aria-label="t('kilo.batch.proxyGroup')" :placeholder="t('kilo.batch.proxyPlaceholder')" /></div>
        <div><label for="kilo-batch-groups" class="mb-2 block text-sm font-medium">{{ t('kilo.batch.groups') }}</label><Select id="kilo-batch-groups" v-model="groupIds" :options="groupOptions" multiple :disabled="busy || choicesLoading" :aria-label="t('kilo.batch.groups')" :placeholder="t('kilo.batch.groupPlaceholder')" /></div>
        <div><label for="kilo-egress-count" class="mb-2 block text-sm font-medium">{{ t('kilo.batch.proxiesPerAccount') }}</label><input id="kilo-egress-count" v-model.number="proxiesPerAccount" type="number" class="input w-full" min="1" max="100" required :disabled="busy" /></div>
        <div><label for="kilo-proxy-concurrency" class="mb-2 block text-sm font-medium">{{ t('kilo.batch.proxyConcurrency') }}</label><input id="kilo-proxy-concurrency" v-model.number="proxyConcurrency" type="number" class="input w-full" min="1" max="10000" required :disabled="busy" /></div>
        <div><label for="kilo-account-concurrency" class="mb-2 block text-sm font-medium">{{ t('kilo.batch.accountConcurrency') }}</label><input id="kilo-account-concurrency" v-model.number="accountConcurrency" type="number" class="input w-full" min="1" max="10000" required :disabled="busy" /></div>
        <div><label for="kilo-priority" class="mb-2 block text-sm font-medium">{{ t('kilo.batch.priority') }}</label><input id="kilo-priority" v-model.number="priority" type="number" class="input w-full" min="0" max="10000" required :disabled="busy" /></div>
      </div>
      <p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('kilo.routing.requestHint') }}</p>
      <div><label for="kilo-batch-statuses" class="mb-2 block text-sm font-medium">{{ t('kilo.routing.autoReleaseStatuses') }}</label><input id="kilo-batch-statuses" v-model="autoReleaseInput" type="text" class="input w-full" :disabled="busy" placeholder="401, 402, 403" /><p class="mt-2 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('kilo.routing.autoReleaseHint') }}</p></div>
      <p class="rounded-lg bg-primary-50 p-3 text-sm leading-6 text-primary-800 dark:bg-primary-900/20 dark:text-primary-200">{{ t('kilo.egressRule') }} {{ t('kilo.batch.egressHint') }}</p>
      <p v-if="capacityWarning" class="text-sm text-amber-700 dark:text-amber-300">{{ capacityWarning }}</p>
      <div class="rounded-lg border border-gray-200 dark:border-dark-700">
        <h3 class="px-3 py-2 text-sm font-medium">{{ t('kilo.batch.mapping') }}</h3>
        <ol class="max-h-48 divide-y divide-gray-100 overflow-auto dark:divide-dark-700"><li v-for="(pair, index) in pairs" :key="pair.prepared.id" class="grid grid-cols-[2rem_1fr] gap-2 px-3 py-2 text-sm"><span class="tabular-nums text-gray-400">{{ index + 1 }}</span><div class="min-w-0"><p class="break-words font-medium">{{ pair.prepared.name }}</p><p class="break-words text-xs text-gray-500 dark:text-dark-400">{{ bindSources ? t('kilo.batch.expectedSource', { name: pair.source?.name ?? t('kilo.batch.unselected'), email: pair.source?.email || t('common.notAvailable') }) : t('kilo.batch.activationOnly') }}</p></div></li></ol>
      </div>
      <div v-if="bindSources" class="space-y-2 rounded-lg border border-amber-200 bg-amber-50 p-3 dark:border-amber-900 dark:bg-amber-900/20">
        <label for="kilo-byok-enabled-confirmed" class="flex items-start gap-3 text-sm leading-6"><input id="kilo-byok-enabled-confirmed" v-model="byokEnabledConfirmed" type="checkbox" class="mt-1 h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500" :disabled="busy" aria-describedby="kilo-byok-enabled-help" /><span>{{ t('kilo.batch.byokEnabledConfirmed') }}</span></label>
        <p id="kilo-byok-enabled-help" class="text-xs leading-5 text-amber-800 dark:text-amber-300">{{ t('kilo.batch.byokEnabledHint') }}</p>
      </div>
      <label class="flex items-start gap-3 text-sm leading-6"><input v-model="confirmed" type="checkbox" class="mt-1 h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500" :disabled="busy" /><span>{{ t('kilo.batch.confirmation') }}</span></label>
      <div v-if="validation" class="text-sm text-amber-700 dark:text-amber-300" role="status">{{ validation }}<button v-if="choicesError" type="button" class="ml-2 underline" :disabled="busy || choicesLoading" @click="emit('reloadChoices')">{{ t('common.refresh') }}</button></div>
      <p v-if="error" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300" role="alert">{{ error }}</p>
    </form>
    <template #footer><button type="button" class="btn btn-secondary" :disabled="busy" @click="emit('close')">{{ t('common.cancel') }}</button><button type="submit" form="kilo-batch-form" class="btn btn-primary" :disabled="busy || choicesLoading || !!validation || !confirmed">{{ busy ? t('common.submitting') : t('kilo.batch.create') }}</button></template>
  </BaseDialog>
</template>
