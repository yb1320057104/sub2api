<template>
  <div class="space-y-5">
    <form class="card space-y-4 p-5" @submit.prevent="save">
      <div><h2 class="font-semibold text-gray-900 dark:text-white">{{ t('accountHealth.settings') }}</h2><p class="mt-1 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('accountHealth.formula') }}</p></div>
      <fieldset :disabled="!initialized || saving" class="flex flex-wrap items-end gap-5">
        <label class="flex h-10 items-center gap-2 text-sm"><input v-model="form.enabled" type="checkbox" class="rounded border-gray-300 text-primary-600" @change="dirty = true" />{{ t('accountHealth.enabled') }}</label>
        <label><span class="input-label">{{ t('accountHealth.interval') }}</span><input v-model.number="form.interval_minutes" type="number" min="5" max="1440" step="1" required class="input w-44" @input="dirty = true" /></label>
        <label><span class="input-label">{{ t('accountHealth.window') }}</span><input v-model.number="form.window_hours" type="number" min="1" max="168" step="1" required class="input w-44" @input="dirty = true" /></label>
        <button class="btn btn-primary" :disabled="saving || !dirty">{{ t(saving ? 'accountHealth.saving' : 'accountHealth.save') }}</button>
      </fieldset>
      <p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('accountHealth.scheduleHint') }}</p>
    </form>
    <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }} <button type="button" class="underline" @click="load">{{ t('accountCandy.retry') }}</button></p>
    <section class="card space-y-4 p-5" aria-live="polite">
      <div class="flex flex-wrap items-center justify-between gap-3"><h2 class="font-semibold text-gray-900 dark:text-white">{{ t('accountHealth.progress') }}</h2><span class="rounded-full bg-primary-50 px-3 py-1 text-xs text-primary-700 dark:bg-primary-950/30 dark:text-primary-300">{{ cycle ? t('accountHealth.status.' + cycle.status) : t(initialized ? 'accountHealth.waiting' : 'accountHealth.loading') }}</span></div>
      <div class="grid gap-4 text-sm sm:grid-cols-3">
        <div><p class="text-xs text-gray-500">{{ t('accountHealth.current') }}</p><p class="mt-1 break-all font-medium">{{ data?.current.length ? data.current[0]?.account_name : t(cycle?.status === 'running' ? 'accountHealth.waitingBatch' : 'accountHealth.idle') }}</p></div>
        <div><p class="text-xs text-gray-500">{{ t('accountHealth.finished') }}</p><p class="mt-1 tabular-nums">{{ finished }} / {{ cycle?.total_accounts ?? 0 }}<span class="ml-2 text-xs text-red-600">{{ t('accountHealth.failedCount', { count: failed }) }}</span></p></div>
        <div><p class="text-xs text-gray-500">{{ t('accountHealth.nextCycle') }}</p><p class="mt-1">{{ data?.config.enabled ? formatDateTime(data.config.next_cycle_at) : t('accountHealth.disabled') }}</p></div>
      </div>
      <p v-if="cycle && cycle.total_accounts === 0" class="rounded-lg bg-gray-50 p-3 text-sm text-gray-500 dark:bg-dark-800">{{ t('accountHealth.empty') }}</p>
      <div v-if="cycle?.batches.length" class="grid gap-3 sm:grid-cols-2 xl:grid-cols-5">
        <article v-for="batch in cycle.batches" :key="batch.batch_no" class="rounded-xl border p-3 text-xs" :class="batch.running ? 'border-primary-300 bg-primary-50/50 dark:border-primary-700 dark:bg-primary-950/20' : 'border-gray-200 dark:border-dark-700'">
          <div class="flex items-center justify-between"><h3 class="font-semibold">{{ t('accountHealth.batch', { n: batch.batch_no }) }}</h3><span class="tabular-nums">{{ batch.total }}</span></div>
          <p class="mt-2 text-gray-500">{{ t('accountHealth.batchFinished', { done: batch.completed + batch.failed + batch.skipped, total: batch.total }) }}</p>
          <p v-if="batch.failed" class="mt-1 text-red-600">{{ t('accountHealth.failedCount', { count: batch.failed }) }}</p>
          <p class="mt-1 text-gray-400">{{ formatDateTime(batch.scheduled_at) }}</p>
        </article>
      </div>
      <p class="text-xs text-gray-400">{{ t('accountHealth.progressHint') }}</p>
    </section>
    <section class="card overflow-hidden">
      <div class="overflow-x-auto"><table class="w-full text-left text-sm"><thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-800"><tr><th class="px-4 py-3">{{ t('accountHealth.account') }}</th><th class="px-4 py-3">{{ t('accountHealth.batchColumn') }}</th><th class="px-4 py-3">{{ t('accountHealth.statusColumn') }}</th><th class="px-4 py-3">{{ t('accountHealth.health') }}</th><th class="px-4 py-3">{{ t('accountHealth.completedAt') }}</th></tr></thead><tbody class="divide-y divide-gray-100 dark:divide-dark-700">
        <tr v-for="job in data?.items ?? []" :key="job.account_id"><td class="max-w-60 px-4 py-4"><p class="truncate font-medium" :title="job.account_name">{{ job.account_name }}</p><span class="text-xs text-gray-400">#{{ job.account_id }}</span></td><td class="px-4 py-4 tabular-nums">{{ job.batch_no }}</td><td class="max-w-72 px-4 py-4"><span class="rounded px-2 py-1 text-xs" :class="statusClass(job.status)">{{ t('accountHealth.status.' + job.status) }}</span><p v-if="job.error_message" class="mt-2 text-xs leading-5 text-amber-600 dark:text-amber-400">{{ job.error_message }}</p></td><td class="px-4 py-4"><AccountHealthCell v-if="job.result" :stats="job.result" /><span v-else class="text-xs text-gray-400">{{ t(job.status === 'failed' ? 'accountHealth.preserved' : 'accountHealth.pending') }}</span></td><td class="whitespace-nowrap px-4 py-4 text-xs text-gray-500">{{ job.finished_at ? formatDateTime(job.finished_at) : '—' }}</td></tr>
        <tr v-if="!data?.items.length"><td colspan="5" class="p-10 text-center text-sm text-gray-400">{{ t(loading ? 'accountHealth.loading' : 'accountHealth.noCycle') }}</td></tr>
      </tbody></table></div>
      <div class="flex items-center justify-between border-t border-gray-100 px-4 py-3 text-xs text-gray-500 dark:border-dark-700"><span>{{ t('accountHealth.page', { page, total: pages }) }}</span><div class="flex gap-2"><button class="btn btn-secondary px-3 py-1.5" :disabled="page <= 1 || loading" @click="page--">{{ t('pagination.previous') }}</button><button class="btn btn-secondary px-3 py-1.5" :disabled="page >= pages || loading" @click="page++">{{ t('pagination.next') }}</button></div></div>
    </section>
  </div>
</template>
<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useIntervalFn } from '@vueuse/core'
import { accountHealthAPI, type AccountHealthConfig, type AccountHealthProgress } from '@/api/admin/accountHealth'
import AccountHealthCell from '@/components/account/AccountHealthCell.vue'
import { formatDateTime } from '@/utils/format'
import { extractApiErrorMessage } from '@/utils/apiError'
import { useAppStore } from '@/stores/app'
const { t } = useI18n()
const app = useAppStore()
const data = ref<AccountHealthProgress | null>(null)
const form = ref<AccountHealthConfig>({ enabled: true, interval_minutes: 30, window_hours: 24, revision: 0, next_cycle_at: '' })
const initialized = ref(false), dirty = ref(false), saving = ref(false), loading = ref(false), error = ref(''), page = ref(1)
const cycle = computed(() => data.value?.cycle)
const finished = computed(() => cycle.value?.batches.reduce((n, b) => n + b.completed + b.failed + b.skipped, 0) ?? 0)
const failed = computed(() => cycle.value?.batches.reduce((n, b) => n + b.failed, 0) ?? 0)
const pages = computed(() => Math.max(1, Math.ceil((data.value?.total ?? 0) / 20)))
let disposed = false, sequence = 0, controller: AbortController | undefined
async function load() {
  if (disposed) return
  const id = ++sequence
  controller?.abort(); controller = new AbortController(); loading.value = true
  try {
    const value = await accountHealthAPI.progress(page.value, controller.signal)
    if (disposed || id !== sequence) return
    data.value = value; error.value = ''
    if (!initialized.value || !dirty.value) form.value = { ...value.config }
    initialized.value = true
    if (page.value > pages.value) page.value = pages.value
  } catch (e) { if (!disposed && id === sequence) error.value = extractApiErrorMessage(e, t('accountHealth.loadFailed')) }
  finally { if (id === sequence) loading.value = false }
}
async function save() {
  if (saving.value || !initialized.value) return
  saving.value = true
  // Cancel an older polling response before adopting the new configuration revision.
  sequence++; controller?.abort(); loading.value = false
  try { const value = await accountHealthAPI.save(form.value); if (disposed) return; form.value = value; dirty.value = false; app.showSuccess(t('accountHealth.saved')); await load() }
  catch (e) { if (!disposed) app.showError(extractApiErrorMessage(e, t('accountHealth.saveFailed'))) }
  finally { saving.value = false }
}
function statusClass(status: string) { return status === 'failed' ? 'bg-red-50 text-red-700 dark:bg-red-950/30 dark:text-red-300' : status === 'completed' ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/30 dark:text-emerald-300' : status === 'running' ? 'bg-primary-50 text-primary-700 dark:bg-primary-950/30 dark:text-primary-300' : 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-dark-300' }
watch(page, () => { void load() })
useIntervalFn(() => { if (!document.hidden && !loading.value && !saving.value) void load() }, 5000)
onMounted(load)
onUnmounted(() => { disposed = true; sequence++; controller?.abort() })
</script>
