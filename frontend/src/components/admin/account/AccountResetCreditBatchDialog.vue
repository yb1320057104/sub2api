<template>
  <BaseDialog :show="show" :title="t('admin.accounts.resetCreditsBatch.title')" width="wide" @close="close">
    <div class="space-y-4">
      <p class="text-sm text-gray-600 dark:text-dark-300">{{ t('admin.accounts.resetCreditsBatch.hint') }}</p>
      <p class="text-sm tabular-nums" role="status">{{ t('admin.accounts.resetCreditsBatch.progress', { done: completed, total: rows.length, success: counts.success, failed: counts.failed, skipped: counts.skipped }) }}</p>
      <div class="max-h-[55vh] overflow-auto rounded-lg border border-gray-200 dark:border-dark-700">
        <table class="min-w-full text-left text-sm">
          <thead class="sticky top-0 bg-gray-50 dark:bg-dark-800"><tr><th class="px-3 py-2">{{ t('admin.accounts.resetCreditsBatch.account') }}</th><th class="px-3 py-2">{{ t('admin.accounts.resetCreditsBatch.remaining') }}</th><th class="px-3 py-2">{{ t('admin.accounts.resetCreditsBatch.result') }}</th></tr></thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
            <tr v-for="row in rows" :key="row.account_id">
              <td class="max-w-64 break-words px-3 py-2"><span class="font-mono text-xs text-gray-500">#{{ row.account_id }}</span><span class="ml-2">{{ row.account_name }}</span></td>
              <td class="px-3 py-2 tabular-nums">{{ row.total_count ?? '—' }}<span v-if="row.available_count != null && row.available_count !== row.total_count" class="ml-1 text-xs text-gray-500">{{ t('admin.accounts.resetCreditsBatch.available', { count: row.available_count }) }}</span></td>
              <td class="px-3 py-2" :class="row.status === 'failed' ? 'text-red-600 dark:text-red-400' : row.status === 'success' ? 'text-emerald-600 dark:text-emerald-400' : 'text-gray-500'"><span>{{ t('admin.accounts.resetCreditsBatch.' + row.status) }}</span><p v-if="row.error_code" class="mt-1 text-xs">{{ errorText(row.error_code) }}</p><p v-if="row.warning_code" class="mt-1 text-xs text-amber-600 dark:text-amber-400">{{ t('admin.accounts.resetCreditsBatch.cacheWarning') }}</p></td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
    <template #footer><button type="button" class="btn btn-secondary" @click="close">{{ t(running ? 'admin.accounts.resetCreditsBatch.stop' : 'common.close') }}</button></template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { batchQueryResetCredits, type ResetCreditBatchResult } from '@/api/admin/accounts'

type BatchRow = Omit<ResetCreditBatchResult, 'status'> & { status: ResetCreditBatchResult['status'] | 'pending' | 'querying' | 'cancelled' }
const props = defineProps<{ show: boolean; accountIds: number[] }>()
const emit = defineEmits<{ close: []; results: [results: ResetCreditBatchResult[]] }>()
const { t } = useI18n()
const rows = ref<BatchRow[]>([])
const running = ref(false)
const completed = computed(() => rows.value.filter(row => !['pending', 'querying'].includes(row.status)).length)
const counts = computed(() => ({
  success: rows.value.filter(row => row.status === 'success').length,
  failed: rows.value.filter(row => row.status === 'failed').length,
  skipped: rows.value.filter(row => row.status === 'skipped').length
}))
let request: AbortController | null = null
let generation = 0

function errorText(code: string) {
  if (code === 'RESET_CREDITS_UNSUPPORTED') return t('admin.accounts.resetCreditsBatch.unsupported')
  if (code === 'ACCOUNT_NOT_FOUND') return t('admin.accounts.resetCreditsBatch.notFound')
  return `${t('admin.accounts.resetCreditsBatch.queryFailed')} (${code})`
}

async function run() {
  request?.abort()
  const current = ++generation
  const controller = new AbortController()
  request = controller
  const ids = [...new Set(props.accountIds.filter(id => Number.isSafeInteger(id) && id > 0))]
  rows.value = ids.map(account_id => ({ account_id, account_name: '', platform: '', status: 'pending', available_count: null, total_count: null }))
  running.value = true
  try {
    // Three IDs per request keeps each response within one upstream timeout
    // window and exposes progress even when many accounts were selected.
    for (let start = 0; start < ids.length; start += 3) {
      if (controller.signal.aborted || current !== generation) return
      const chunk = ids.slice(start, start + 3)
      const idSet = new Set(chunk)
      rows.value = rows.value.map(row => idSet.has(row.account_id) ? { ...row, status: 'querying' } : row)
      try {
        const response = await batchQueryResetCredits(chunk, controller.signal)
        if (controller.signal.aborted || current !== generation) return
        const results = new Map(response.results.map(result => [result.account_id, result]))
        rows.value = rows.value.map(row => results.get(row.account_id) ?? (idSet.has(row.account_id) ? { ...row, status: 'failed', error_code: 'RESET_CREDITS_EMPTY_RESULT' } : row))
        emit('results', response.results)
      } catch {
        if (controller.signal.aborted || current !== generation) return
        rows.value = rows.value.map(row => idSet.has(row.account_id) ? { ...row, status: 'failed', error_code: 'RESET_CREDITS_REQUEST_FAILED' } : row)
      }
    }
  } finally {
    if (current === generation) running.value = false
  }
}

function stop() {
  request?.abort()
  generation++
  running.value = false
  rows.value = rows.value.map(row => ['pending', 'querying'].includes(row.status) ? { ...row, status: 'cancelled' } : row)
}
function close() { stop(); emit('close') }
watch(() => props.show, value => { if (value) void run(); else stop() }, { immediate: true })
onUnmounted(stop)
</script>
