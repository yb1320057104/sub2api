<template>
  <section class="space-y-5" data-testid="binance-daily-usage">
    <div class="card flex flex-wrap items-center justify-between gap-4 p-4">
      <p class="max-w-3xl text-sm text-gray-600 dark:text-gray-300">{{ t('binanceRecharge.dailyUsage.description') }}</p>
      <div class="flex flex-wrap items-center gap-4">
        <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-200">
          <input v-model="autoRefresh" type="checkbox" class="h-4 w-4 rounded" data-testid="daily-usage-auto-refresh" />
          {{ t('binanceRecharge.dailyUsage.autoRefresh') }}
        </label>
        <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-200">
          {{ t('binanceRecharge.dailyUsage.refreshSeconds') }}
          <input
            v-model.number="refreshSeconds"
            type="number"
            min="1"
            max="3600"
            step="1"
            class="input w-24"
            data-testid="daily-usage-refresh-seconds"
            :aria-invalid="!validInterval"
            :aria-describedby="validInterval ? undefined : 'daily-usage-interval-error'"
          />
        </label>
      </div>
      <p v-if="!validInterval" id="daily-usage-interval-error" class="w-full text-sm text-red-600" role="alert">{{ t('binanceRecharge.dailyUsage.invalidInterval') }}</p>
    </div>
    <UsageView ref="usageView" embedded initial-today ignore-route-filters />
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import UsageView from '@/views/admin/UsageView.vue'

const { t } = useI18n()
const usageView = ref<InstanceType<typeof UsageView> | null>(null)
const autoRefresh = ref(true)
const refreshSeconds = ref<number | string>(5)
const validInterval = computed(() => Number.isInteger(refreshSeconds.value) && Number(refreshSeconds.value) >= 1 && Number(refreshSeconds.value) <= 3600)
let timer: ReturnType<typeof setTimeout> | undefined
let mounted = false
let refreshing = false

const visible = () => document.visibilityState !== 'hidden'
const stopTimer = () => {
  clearTimeout(timer)
  timer = undefined
}

// Schedule after the previous refresh completes. A slow request never adds more
// requests to a timer queue, and changed interval settings apply to the next run.
const schedule = () => {
  stopTimer()
  if (!mounted || !visible() || !autoRefresh.value || !validInterval.value || refreshing) return
  timer = setTimeout(async () => {
    timer = undefined
    if (!mounted || !visible() || !autoRefresh.value || !validInterval.value) return
    refreshing = true
    try {
      await usageView.value?.refreshData()
    } finally {
      refreshing = false
      schedule()
    }
  }, Number(refreshSeconds.value) * 1000)
}

const onVisibilityChange = async () => {
  stopTimer()
  if (!visible()) {
    usageView.value?.pauseRequests()
    return
  }
  refreshing = true
  try {
    await usageView.value?.resumeRequests(autoRefresh.value && validInterval.value)
  } finally {
    refreshing = false
    schedule()
  }
}

watch([autoRefresh, refreshSeconds], schedule)
onMounted(() => {
  mounted = true
  document.addEventListener('visibilitychange', onVisibilityChange)
  if (!visible()) usageView.value?.pauseRequests()
  schedule()
})
onUnmounted(() => {
  mounted = false
  stopTimer()
  usageView.value?.pauseRequests()
  document.removeEventListener('visibilitychange', onVisibilityChange)
})
</script>
