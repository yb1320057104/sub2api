<template>
  <BaseDialog :show="show" :title="t('accountCandy.detailsTitle', { name: accountName })" width="wide" @close="emit('close')">
    <label v-if="history.length" class="mb-5 block"><span class="input-label">{{ t('radar.history') }}</span><Select v-model="selectedKey" :options="historyOptions" :disabled="loading" @change="selectHistory" /></label>
    <p v-if="!loading && !details && !error" class="py-8 text-center text-sm text-gray-500">{{ t('accountCandy.untested') }}</p>
    <div v-if="loading && !details" class="py-8 text-center text-sm text-gray-500">{{ t('accountCandy.loading') }}</div>
    <div v-else-if="error && !details" class="space-y-3 py-5 text-center">
      <p class="text-sm text-red-600 dark:text-red-400" role="alert">{{ error }}</p>
      <button type="button" class="btn btn-secondary" @click="load">{{ t('accountCandy.retry') }}</button>
    </div>
    <div v-if="details" class="space-y-5">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <span class="rounded-md px-2 py-1 text-sm font-medium" :class="statusClass">{{ statusText }}</span>
        <span class="text-xs text-gray-500 dark:text-dark-400">{{ t('accountCandy.expectedAnswer') }}: 21</span>
      </div>
      <dl class="grid gap-3 rounded-xl bg-gray-50 p-4 text-sm dark:bg-dark-800 sm:grid-cols-2">
        <div><dt class="text-xs text-gray-500">{{ t('accountCandy.model') }}</dt><dd class="mt-1 break-all font-mono text-gray-900 dark:text-white">{{ details.model }}</dd></div>
        <div><dt class="text-xs text-gray-500">{{ t('accountCandy.effort') }}</dt><dd class="mt-1 text-gray-900 dark:text-white">{{ details.effort || t('accountCandy.defaultEffort') }}</dd></div>
        <div><dt class="text-xs text-gray-500">{{ t('accountCandy.testedAt') }}</dt><dd class="mt-1 text-gray-900 dark:text-white">{{ formatDateTime(details.started_at) }}</dd></div>
        <div><dt class="text-xs text-gray-500">{{ t('accountCandy.duration') }}</dt><dd class="mt-1 text-gray-900 dark:text-white">{{ details.duration_ms == null ? '—' : t('accountCandy.durationSeconds', { seconds: (details.duration_ms / 1000).toFixed(1) }) }}</dd></div>
        <div v-if="details.status === 'completed' && (details.model_results?.length || 0) <= 1"><dt class="text-xs text-gray-500">{{ t('accountCandy.answer') }}</dt><dd class="mt-1 text-gray-900 dark:text-white">{{ details.answer ?? t('accountCandy.noAnswer') }}</dd></div>
        <div><dt class="text-xs text-gray-500">{{ t('accountCandy.source') }}</dt><dd class="mt-1 text-gray-900 dark:text-white">{{ t(details.origin === 'authorization' ? 'radar.authorizationTest' : details.source === 'scheduled' ? 'accountCandy.scheduled' : 'accountCandy.manual') }}</dd></div>
      </dl>
      <p v-if="details.started_at_estimated" class="text-xs text-gray-500">{{ t('radar.authorizationTimeFallback') }}</p>
      <p v-if="movedGroupIDs.length" class="break-words rounded-lg bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-950/30 dark:text-amber-300">{{ t('accountCandy.moved', { group: movedGroupIDs.map(groupName).join(' / ') }) }}</p>
      <p v-if="details.move_error" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ t('accountCandy.moveFailed') }}: {{ details.move_error }}</p>
      <p v-if="error" class="text-sm text-red-600 dark:text-red-400" role="alert">{{ error }}</p>
      <section>
        <h4 class="mb-2 text-sm font-medium text-gray-800 dark:text-dark-100">{{ t('accountCandy.prompt') }}</h4>
        <p class="max-h-56 overflow-y-auto whitespace-pre-wrap break-words rounded-lg border border-gray-200 p-4 text-sm leading-6 text-gray-700 dark:border-dark-700 dark:text-dark-200">{{ details.prompt }}</p>
      </section>
      <section v-if="details.model_results?.length" class="space-y-3">
        <article v-for="model in details.model_results" :key="model.id" class="overflow-hidden rounded-xl border" :class="model.passed === false ? 'border-red-200 dark:border-red-900' : 'border-gray-200 dark:border-dark-700'">
          <div class="flex flex-wrap items-start justify-between gap-2 bg-gray-50 px-4 py-3 dark:bg-dark-800">
            <div class="min-w-0"><h4 class="break-all font-mono text-sm font-semibold text-gray-900 dark:text-white">{{ model.model }}</h4><p class="mt-1 text-xs text-gray-500">{{ t('accountCandy.effort') }}: {{ model.effort || t('accountCandy.defaultEffort') }}</p><p v-if="model.mapped_model && model.mapped_model !== model.model" class="mt-1 break-all font-mono text-xs text-gray-500">{{ t('radar.mappedModel') }}: {{ model.mapped_model }}</p></div>
            <span class="text-xs font-semibold" :class="model.passed === false ? 'text-red-600 dark:text-red-400' : model.passed ? 'text-emerald-600 dark:text-emerald-400' : 'text-gray-500'">{{ model.status === 'completed' ? t(model.passed ? 'accountCandy.passed' : 'accountCandy.notPassed') : t('accountCandy.' + model.status) }}</span>
          </div>
          <div class="space-y-3 p-4 text-sm">
            <p v-if="model.status === 'completed'" class="text-gray-700 dark:text-dark-200">{{ t('accountCandy.answer') }}: <strong class="font-mono">{{ model.answer ?? t('accountCandy.noAnswer') }}</strong></p>
            <p class="break-all text-xs text-gray-500">{{ t('radar.egressIP') }}: <span class="font-mono">{{ model.egress_ip || t('radar.unknown') }}</span><span v-if="model.egress_checked_at"> · {{ formatDateTime(model.egress_checked_at) }}</span></p>
            <p v-if="model.egress_error" class="text-xs text-amber-600 dark:text-amber-400">{{ model.egress_error }}</p>
            <p v-if="model.error_message" class="text-amber-700 dark:text-amber-300">{{ model.error_message }}</p>
            <p class="max-h-80 overflow-y-auto whitespace-pre-wrap break-words leading-6 text-gray-700 dark:text-dark-200">{{ model.response_text || t(model.status === 'running' ? 'accountCandy.waiting' : details.origin === 'authorization' && model.status === 'completed' ? 'radar.authorizationResponseMissing' : 'accountCandy.noResponse') }}</p>
          </div>
        </article>
        <p class="text-xs leading-5 text-gray-500">{{ t('radar.egressHint') }}</p>
      </section>
      <section v-else>
        <h4 class="mb-2 text-sm font-medium text-gray-800 dark:text-dark-100">{{ t('accountCandy.response') }}</h4>
        <p class="max-h-96 overflow-y-auto whitespace-pre-wrap break-words rounded-lg border border-gray-200 p-4 text-sm leading-6 text-gray-700 dark:border-dark-700 dark:text-dark-200">{{ details.response_text || t(details.status === 'running' ? 'accountCandy.waiting' : 'accountCandy.noResponse') }}</p>
      </section>
      <section v-if="details.error_message">
        <h4 class="mb-2 text-sm font-medium text-gray-800 dark:text-dark-100">{{ t('accountCandy.error') }}</h4>
        <p class="whitespace-pre-wrap break-words text-sm text-amber-700 dark:text-amber-300">{{ details.error_message }}</p>
      </section>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useIntervalFn } from '@vueuse/core'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import { accountCandyAPI, getCandyMovedGroupIDs, type AccountCandyDetails, type AccountCandyResult } from '@/api/admin/accountCandy'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatDateTime } from '@/utils/format'

const props = defineProps<{
  show: boolean
  accountName: string
  accountId?: number
  result: AccountCandyResult | null
  groups: { id: number; name: string }[]
}>()
const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()
const details = ref<AccountCandyDetails | null>(null)
const movedGroupIDs = computed(() => getCandyMovedGroupIDs(details.value))
const loading = ref(false)
const error = ref('')
let controller: AbortController | null = null
let historyController: AbortController | null = null
const history = ref<AccountCandyResult[]>([])
const selectedKey = ref('')
const keyOf = (result: AccountCandyResult) => (result.origin || 'radar') + ':' + result.id
const selectedResult = computed(() => history.value.find(result => keyOf(result) === selectedKey.value) ?? props.result)
const historyOptions = computed(() => history.value.map(result => ({ value: keyOf(result), label: formatDateTime(result.started_at) + ' · ' + (result.origin === 'authorization' ? t('radar.authorizationTest') + ' · ' : result.origin === 'legacy' ? t('radar.legacy') + ' · ' : '') + result.model })))
const statusText = computed(() => {
  if (!details.value) return ''
  if (details.value.verdict === 'fail' || details.value.passed === false) return t('accountCandy.notPassed')
  if (details.value.verdict === 'pass' || details.value.passed === true) return t('accountCandy.passed')
  return t(`accountCandy.${details.value.status}`)
})
const statusClass = computed(() => details.value?.passed != null
  ? details.value.passed ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/30 dark:text-emerald-300' : 'bg-red-50 text-red-700 dark:bg-red-950/30 dark:text-red-300'
  : 'bg-primary-50 text-primary-700 dark:bg-primary-950/30 dark:text-primary-300')

function groupName(id: number) {
  return props.groups.find(group => group.id === id)?.name ?? t('accountCandy.groupId', { id })
}

async function load() {
  const selected = selectedResult.value
  if (!props.show || !selected) return
  controller?.abort()
  const request = new AbortController()
  controller = request
  loading.value = true
  error.value = ''
  try {
    const result = await accountCandyAPI.details(selected.account_id, selected.id, request.signal, selected.origin)
    if (!request.signal.aborted) details.value = result
  } catch (cause) {
    if (!request.signal.aborted) error.value = extractApiErrorMessage(cause, t('accountCandy.loadFailed'))
  } finally {
    if (!request.signal.aborted) loading.value = false
  }
}

function selectHistory() { details.value = null; void load() }

async function openHistory() {
  historyController?.abort()
  const id = props.accountId ?? props.result?.account_id
  if (!props.show || !id) return
  const request = new AbortController()
  historyController = request
  loading.value = true
  error.value = ''
  try {
    const rows = await accountCandyAPI.history(id, request.signal)
    if (request.signal.aborted) return
    history.value = rows
    selectedKey.value = props.result ? keyOf(props.result) : rows[0] ? keyOf(rows[0]) : ''
    await load()
  } catch (cause) {
    if (!request.signal.aborted) error.value = extractApiErrorMessage(cause, t('accountCandy.loadFailed'))
  } finally { if (!request.signal.aborted) loading.value = false }
}

watch(() => [props.show, props.accountId, props.result?.account_id, props.result?.id, props.result?.origin] as const, () => {
  controller?.abort()
  historyController?.abort()
  details.value = null
  history.value = []
  loading.value = false
  if (props.show) void openHistory()
}, { immediate: true })
useIntervalFn(() => {
  if (props.show && !document.hidden && !loading.value && details.value?.status === 'running') void load()
}, 5000)
onUnmounted(() => { controller?.abort(); historyController?.abort() })
</script>
