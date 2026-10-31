<template>
  <div class="min-w-[100px] text-xs" :title="hint">
    <span v-if="error" class="text-amber-600 dark:text-amber-400">{{ t('admin.accounts.requestStats.unavailable') }}</span>
    <span v-else-if="loading && !stats" class="text-gray-400">{{ t('common.loading') }}</span>
    <template v-else>
      <span class="font-medium tabular-nums" :class="isFailure ? 'text-red-600 dark:text-red-400' : 'text-gray-700 dark:text-dark-200'">{{ value }}</span>
      <div v-if="timestamp" class="mt-1 whitespace-nowrap text-[11px] text-gray-400 dark:text-dark-400">{{ formatDateTime(timestamp) }}</div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { formatDateTime } from '@/utils/format'
import type { AccountRequestStats } from '@/types'

const props = defineProps<{
  stats: AccountRequestStats | null
  metric: 'first_token' | 'failures'
  loading: boolean
  error: boolean
}>()
const { t } = useI18n()
const hint = computed(() => t(props.metric === 'first_token'
  ? 'admin.accounts.requestStats.firstTokenHint' : 'admin.accounts.requestStats.failuresHint'))
const isFailure = computed(() => props.metric === 'failures' && (props.stats?.consecutive_upstream_failures ?? 0) > 0)
const value = computed(() => {
  if (!props.stats) return '—'
  if (props.metric === 'failures') return String(props.stats.consecutive_upstream_failures)
  const ms = props.stats.latest_first_token_ms
  return ms == null ? '—' : `${(ms / 1000).toFixed(2)}s`
})
const timestamp = computed(() => props.metric === 'first_token'
  ? props.stats?.latest_completed_at : props.stats?.last_upstream_failure_at)
</script>
