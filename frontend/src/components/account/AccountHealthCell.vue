<template>
  <div class="min-w-40 space-y-1 text-xs" :title="snapshotHint">
    <span v-if="error" class="text-amber-600 dark:text-amber-400">{{ t('accountHealth.unavailable') }}</span>
    <span v-else-if="!stats" class="text-gray-400">{{ t(loading ? 'accountHealth.loading' : 'accountHealth.pending') }}</span>
    <template v-else>
      <div class="flex items-center gap-2"><span class="rounded px-1.5 py-0.5 font-semibold tabular-nums" :class="scoreClass">{{ stats.health_score == null ? t('accountHealth.noTraffic') : stats.health_score.toFixed(1) + '%' }}</span><span class="text-gray-500 dark:text-dark-400">{{ t('accountHealth.requests', { count: stats.total_requests }) }}</span></div>
      <div class="h-1.5 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700" aria-hidden="true"><div class="h-full rounded-full" :class="barClass" :style="{ width: (stats.health_score ?? 0) + '%' }" /></div>
      <div class="flex items-center justify-between gap-3 text-gray-500 dark:text-dark-400"><span>{{ t('accountHealth.upstreamError') }}</span><span class="tabular-nums">{{ stats.total_requests ? (stats.upstream_failures / stats.total_requests * 100).toFixed(1) + '%' : '—' }} ({{ stats.upstream_failures }})</span></div>
      <div class="flex justify-between gap-3 tabular-nums"><span class="text-emerald-700 dark:text-emerald-400" :title="t('accountHealth.fastest')">↓ {{ ttft(stats.fastest_first_token_ms) }}</span><span class="text-amber-700 dark:text-amber-400" :title="t('accountHealth.slowest')">↑ {{ ttft(stats.slowest_first_token_ms) }}</span></div>
      <p class="text-[11px] text-gray-400">{{ formatDateTime(stats.computed_at) }}</p>
    </template>
  </div>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AccountHealthStats } from '@/types'
import { formatDateTime } from '@/utils/format'
const props = defineProps<{ stats: AccountHealthStats | null; loading?: boolean; error?: boolean }>()
const { t } = useI18n()
const level = computed(() => props.stats?.health_score == null ? 'unknown' : props.stats.health_score >= 95 ? 'good' : props.stats.health_score >= 80 ? 'warning' : 'bad')
const scoreClass = computed(() => ({ unknown: 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-dark-300', good: 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300', warning: 'bg-amber-50 text-amber-700 dark:bg-amber-950/40 dark:text-amber-300', bad: 'bg-red-50 text-red-700 dark:bg-red-950/40 dark:text-red-300' })[level.value])
const barClass = computed(() => ({ unknown: 'bg-gray-300', good: 'bg-emerald-500', warning: 'bg-amber-500', bad: 'bg-red-500' })[level.value])
const snapshotHint = computed(() => t('accountHealth.formula') + (props.stats ? '\n' + formatDateTime(props.stats.window_start) + ' – ' + formatDateTime(props.stats.window_end) : ''))
function ttft(value: number | null) { return value == null ? '—' : (value / 1000).toFixed(3) + ' s' }
</script>
