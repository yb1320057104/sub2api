<template>
  <BaseDialog :show="show" :title="t('accountCandy.detailsTitle', { name: accountName })" width="wide" @close="emit('close')">
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
        <div v-if="details.status === 'completed'"><dt class="text-xs text-gray-500">{{ t('accountCandy.answer') }}</dt><dd class="mt-1 text-gray-900 dark:text-white">{{ details.answer ?? t('accountCandy.noAnswer') }}</dd></div>
        <div><dt class="text-xs text-gray-500">{{ t('accountCandy.source') }}</dt><dd class="mt-1 text-gray-900 dark:text-white">{{ t(details.source === 'scheduled' ? 'accountCandy.scheduled' : 'accountCandy.manual') }}</dd></div>
      </dl>
      <p v-if="details.moved_to_group_id != null" class="rounded-lg bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-950/30 dark:text-amber-300">{{ t('accountCandy.moved', { group: groupName(details.moved_to_group_id) }) }}</p>
      <p v-if="details.move_error" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ t('accountCandy.moveFailed') }}: {{ details.move_error }}</p>
      <p v-if="error" class="text-sm text-red-600 dark:text-red-400" role="alert">{{ error }}</p>
      <section>
        <h4 class="mb-2 text-sm font-medium text-gray-800 dark:text-dark-100">{{ t('accountCandy.prompt') }}</h4>
        <p class="max-h-56 overflow-y-auto whitespace-pre-wrap break-words rounded-lg border border-gray-200 p-4 text-sm leading-6 text-gray-700 dark:border-dark-700 dark:text-dark-200">{{ details.prompt }}</p>
      </section>
      <section>
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
import { accountCandyAPI, type AccountCandyDetails, type AccountCandyResult } from '@/api/admin/accountCandy'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatDateTime } from '@/utils/format'

const props = defineProps<{
  show: boolean
  accountName: string
  result: AccountCandyResult | null
  groups: { id: number; name: string }[]
}>()
const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()
const details = ref<AccountCandyDetails | null>(null)
const loading = ref(false)
const error = ref('')
let controller: AbortController | null = null
const statusText = computed(() => {
  if (!details.value) return ''
  if (details.value.status === 'completed') return t(details.value.passed ? 'accountCandy.passed' : 'accountCandy.notPassed')
  return t(`accountCandy.${details.value.status}`)
})
const statusClass = computed(() => details.value?.status === 'completed'
  ? details.value.passed ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/30 dark:text-emerald-300' : 'bg-red-50 text-red-700 dark:bg-red-950/30 dark:text-red-300'
  : 'bg-primary-50 text-primary-700 dark:bg-primary-950/30 dark:text-primary-300')

function groupName(id: number) {
  return props.groups.find(group => group.id === id)?.name ?? t('accountCandy.groupId', { id })
}

async function load() {
  if (!props.show || !props.result) return
  controller?.abort()
  const request = new AbortController()
  controller = request
  loading.value = true
  error.value = ''
  try {
    const result = await accountCandyAPI.details(props.result.account_id, props.result.id, request.signal)
    if (!request.signal.aborted) details.value = result
  } catch (cause) {
    if (!request.signal.aborted) error.value = extractApiErrorMessage(cause, t('accountCandy.loadFailed'))
  } finally {
    if (!request.signal.aborted) loading.value = false
  }
}

watch(() => [props.show, props.result?.id] as const, () => {
  controller?.abort()
  details.value = null
  loading.value = false
  if (props.show) void load()
}, { immediate: true })
useIntervalFn(() => {
  if (props.show && !document.hidden && !loading.value && details.value?.status === 'running') void load()
}, 5000)
onUnmounted(() => controller?.abort())
</script>
