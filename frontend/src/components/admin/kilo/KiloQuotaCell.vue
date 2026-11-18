<script setup lang="ts">
import { computed, onBeforeUnmount, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import UsageProgressBar from '@/components/account/UsageProgressBar.vue'
import { accountsAPI } from '@/api/admin/accounts'
import { isKiloRequestCanceled, kiloErrorMessage } from '@/api/admin/kilo'
import type { AccountUsageInfo, KiloAccountView, UsageProgress } from '@/types'

const props = withDefaults(defineProps<{
  gatewayId: number | null
  view: KiloAccountView
  manualRefreshToken?: number
}>(), { manualRefreshToken: 0 })
const { t, locale } = useI18n()
const usage = shallowRef<AccountUsageInfo | null>(null)
const loading = shallowRef(false)
const error = shallowRef('')
let generation = 0

const canQuery = computed(() => !!props.view.source && props.gatewayId !== null && props.gatewayId > 0)
const windows = computed(() => {
  const values: Array<{ label: string; progress: UsageProgress | null | undefined; color: 'indigo' | 'emerald' }> = [
    { label: '5h', progress: usage.value?.five_hour, color: 'indigo' },
    { label: '7d', progress: usage.value?.seven_day, color: 'emerald' }
  ]
  return values.flatMap(value => value.progress && Number.isFinite(value.progress.utilization)
    ? [{ label: value.label, progress: value.progress, color: value.color }]
    : [])
})
const quotaError = computed(() => error.value || (usage.value?.needs_reauth ? t('kilo.quota.needsReauth') : usage.value?.error || ''))
const updatedTime = computed(() => {
  if (!usage.value?.updated_at) return ''
  const date = new Date(usage.value.updated_at)
  return Number.isNaN(date.getTime()) ? '' : date.toLocaleString(locale.value)
})

async function load(force = false) {
  if (!canQuery.value) return
  const id = props.gatewayId!
  const sourceId = props.view.source!.id
  const current = ++generation
  usage.value = null
  loading.value = true
  error.value = ''
  try {
    // The gateway owns quota-source resolution; never query or generate with the OAuth source ID here.
    const result = await accountsAPI.getUsage(id, 'active', force)
    if (current !== generation || id !== props.gatewayId || sourceId !== props.view.source?.id) return
    usage.value = result
  } catch (cause) {
    if (current === generation && !isKiloRequestCanceled(cause)) error.value = kiloErrorMessage(cause, t('kilo.errors.quotaLoad'))
  } finally {
    if (current === generation) loading.value = false
  }
}

watch(
  [() => props.gatewayId, () => props.view.source?.id, () => props.view.source?.status, () => props.manualRefreshToken],
  (values, previous) => {
    generation++
    usage.value = null
    error.value = ''
    loading.value = false
    const force = previous.length > 0 && values[3] !== previous[3]
    void load(force)
  },
  { immediate: true, flush: 'sync' }
)
// getUsage has no cancellation parameter; invalidate in-flight completions when this cell leaves or changes identity.
onBeforeUnmount(() => { generation++ })
</script>

<template>
  <div class="min-w-0 space-y-1.5 text-xs" :aria-busy="loading" data-testid="kilo-quota">
    <p v-if="!view.source" class="text-gray-500 dark:text-dark-400" data-testid="kilo-quota-unbound">{{ t('kilo.quota.unbound') }}</p>
    <template v-else>
      <div class="flex flex-wrap items-center gap-1.5">
        <span class="font-medium text-gray-700 dark:text-dark-200">{{ t('kilo.quota.title') }}</span>
        <span class="rounded bg-gray-100 px-1.5 py-0.5 text-[10px] text-gray-500 dark:bg-dark-700 dark:text-dark-300">{{ t('kilo.quota.readOnly') }}</span>
      </div>
      <p v-if="!canQuery" class="leading-5 text-gray-500 dark:text-dark-400">{{ t('kilo.quota.pendingActivation') }}</p>
      <template v-else>
        <p v-if="loading" class="leading-5 text-gray-500 dark:text-dark-400" role="status">{{ t('kilo.quota.loading') }}</p>
        <template v-else>
          <p v-if="quotaError" class="break-words leading-5 text-amber-700 dark:text-amber-300" role="alert">{{ quotaError }}</p>
          <UsageProgressBar v-for="window in windows" :key="window.label" :label="window.label" :utilization="window.progress.utilization" :resets-at="window.progress.resets_at" :color="window.color" />
          <p v-if="!quotaError && !windows.length" class="leading-5 text-gray-500 dark:text-dark-400" data-testid="kilo-quota-empty">{{ t('kilo.quota.empty') }}</p>
          <p v-if="updatedTime" class="break-words text-[10px] text-gray-400 dark:text-dark-500">{{ t('kilo.updated', { time: updatedTime }) }}</p>
        </template>
        <button type="button" class="inline-flex min-h-8 items-center rounded-md px-1.5 text-xs font-medium text-primary-700 hover:bg-primary-50 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary-500 disabled:cursor-not-allowed disabled:opacity-50 dark:text-primary-300 dark:hover:bg-primary-900/20" :disabled="loading" data-testid="kilo-refresh-quota" @click="load(true)">{{ t('kilo.quota.refresh') }}</button>
      </template>
    </template>
  </div>
</template>
