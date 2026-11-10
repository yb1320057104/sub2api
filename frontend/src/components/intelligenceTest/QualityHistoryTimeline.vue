<template>
  <section class="card mb-5 space-y-4 p-4 sm:p-5" aria-labelledby="quality-history-title" data-testid="quality-history">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <h2 id="quality-history-title" class="font-semibold text-gray-800 dark:text-gray-100">{{ t('intelligenceTest.quality.title') }}</h2>
      <span v-if="history?.enabled" class="text-xs text-gray-500 dark:text-dark-400">{{ t('intelligenceTest.quality.cadence', { minutes: history.interval_minutes }) }}</span>
    </div>
    <p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('intelligenceTest.quality.scopeHint') }}</p>
    <p v-if="!enabled" class="text-sm text-gray-500 dark:text-dark-400" role="status">{{ t('intelligenceTest.quality.disabled') }}</p>
    <p v-else-if="loading && !history" class="text-sm text-gray-500 dark:text-dark-400" role="status">{{ t('intelligenceTest.loading') }}</p>
    <p v-else-if="!series.length" class="text-sm text-gray-500 dark:text-dark-400" role="status">{{ t('intelligenceTest.quality.waiting') }}</p>
    <template v-else>
      <div v-for="item in series" :key="item.key" class="space-y-2" data-testid="quality-model-history">
        <div class="flex flex-wrap items-center justify-between gap-2 text-xs">
          <p class="min-w-0 break-all text-gray-600 dark:text-dark-300">{{ item.model ?? t('intelligenceTest.notRecorded') }} · {{ effortLabel(item.effort) }}</p>
          <span class="inline-flex items-center gap-1.5 text-gray-600 dark:text-dark-300" data-testid="quality-current-state">
            <span class="h-2 w-2 rounded-full" :class="colors[currentState(item.results[0])]" aria-hidden="true" />
            {{ t(`intelligenceTest.quality.states.${currentState(item.results[0])}`) }}
          </span>
        </div>
        <div class="flex h-6 gap-1" role="group" :aria-label="t('intelligenceTest.quality.historyCount', { count: item.results.length, limit })">
          <span v-for="slot in slots(item.results)" :key="slot.key" class="min-w-0 flex-1 rounded-sm" :class="slot.result ? colors[slot.result.state] : 'bg-gray-200 dark:bg-dark-700'"
            :tabindex="slot.result ? 0 : undefined" :role="slot.result ? 'img' : undefined"
            :aria-hidden="!slot.result || undefined" :aria-label="slot.result ? resultLabel(slot.result) : undefined"
            :title="slot.result ? resultLabel(slot.result) : t('intelligenceTest.quality.notChecked')" data-testid="quality-history-slot" />
        </div>
        <p class="text-xs text-gray-400 dark:text-dark-500">{{ t('intelligenceTest.quality.latestCheck', { time: formatTime(item.results[0].started_at) }) }}</p>
      </div>
      <ul class="flex flex-wrap gap-x-4 gap-y-2 text-xs text-gray-500 dark:text-dark-400" :aria-label="t('intelligenceTest.quality.legend')">
        <li v-for="state in states" :key="state" class="inline-flex items-center gap-1.5">
          <span class="h-2 w-2 rounded-sm" :class="colors[state]" aria-hidden="true" />{{ t(`intelligenceTest.quality.states.${state}`) }}
        </li>
      </ul>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { IntelligenceTestQualityHistory, IntelligenceTestQualityResult, IntelligenceTestQualityState } from '@/api/intelligenceTest'

const props = defineProps<{ history: IntelligenceTestQualityHistory | null; enabled: boolean; loading: boolean }>()
const { t, locale } = useI18n()
const states: IntelligenceTestQualityState[] = ['correct', 'incorrect', 'error', 'unknown', 'empty_pool', 'running']
const colors: Record<IntelligenceTestQualityState | 'stale', string> = {
  correct: 'bg-emerald-500', incorrect: 'bg-amber-500', error: 'bg-red-500',
  unknown: 'bg-slate-400', empty_pool: 'bg-gray-400', running: 'bg-sky-500', stale: 'bg-gray-400',
}
const limit = computed(() => Math.min(30, Math.max(1, props.history?.history_limit || 30)))
const series = computed(() => {
  const items = new Map<string, { key: string; model: string | null | undefined; effort: string | null | undefined; results: IntelligenceTestQualityResult[] }>()
  for (const result of props.history?.results.slice(0, limit.value) ?? []) {
    const key = JSON.stringify([result.model ?? null, result.effort ?? null])
    let item = items.get(key)
    if (!item) {
      item = { key, model: result.model, effort: result.effort, results: [] }
      items.set(key, item)
    }
    item.results.push(result)
  }
  return [...items.values()]
})

function slots(results: IntelligenceTestQualityResult[]) {
  const samples = [...results].reverse()
  return Array.from({ length: limit.value }, (_, index) => ({ key: index, result: samples[index - (limit.value - samples.length)] }))
}
function currentState(result: IntelligenceTestQualityResult): IntelligenceTestQualityState | 'stale' {
  const checked = Date.parse(result.finished_at || result.started_at)
  const observed = Date.parse(props.history?.observed_at || '')
  if (Number.isFinite(checked) && Number.isFinite(observed) && props.history && observed - checked > props.history.stale_after_seconds * 1000) return 'stale'
  return result.state
}
function effortLabel(effort: string | null | undefined) {
  return effort == null ? t('intelligenceTest.notRecorded') : effort === '' ? t('intelligenceTest.effortDefault') : effort
}
function formatTime(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return t('intelligenceTest.notRecorded')
  return new Intl.DateTimeFormat(locale.value === 'zh' ? 'zh-CN' : 'en-US', {
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
  }).format(date)
}
function resultLabel(result: IntelligenceTestQualityResult) {
  return `${formatTime(result.started_at)} · ${result.model ?? t('intelligenceTest.notRecorded')} · ${effortLabel(result.effort)} · ${t(`intelligenceTest.quality.states.${result.state}`)}`
}
</script>
