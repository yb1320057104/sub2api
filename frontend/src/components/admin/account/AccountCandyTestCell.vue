<template>
  <div class="mt-0.5 space-y-1 text-[11px] leading-4">
    <div v-if="result" class="max-w-[220px] break-words font-mono text-gray-500 dark:text-dark-400">{{ result.model }}</div>
    <div v-for="model in result?.failed_models ?? []" :key="model.id" class="max-w-[220px] break-words text-red-600 dark:text-red-400">{{ model.model }}: {{ model.answer ?? t('accountCandy.noAnswer') }}</div>
    <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
      <button
        type="button"
        class="font-medium text-primary-600 hover:underline disabled:cursor-not-allowed disabled:opacity-50 dark:text-primary-400"
        :disabled="starting || result?.status === 'running'"
        @click.stop="emit('test')"
      >{{ t(starting ? 'accountCandy.starting' : 'accountCandy.run') }}</button>
      <button
        v-if="result"
        type="button"
        class="text-gray-500 hover:underline dark:text-dark-400"
        @click.stop="emit('details')"
      >{{ t('accountCandy.details') }}</button>
    </div>
    <div class="flex flex-wrap items-center gap-x-2 gap-y-1" :title="error ? t('accountCandy.loadFailed') : undefined">
      <span :class="statusClass">{{ statusText }}</span>
      <span v-if="getCandyMovedGroupIDs(result).length" class="break-words text-gray-500 dark:text-dark-400">{{ t('accountCandy.moved', { group: targetName }) }}</span>
    </div>
    <div v-if="result" class="text-[10px] text-gray-400 dark:text-dark-500" :title="formatDateTime(result.started_at)">{{ formatRelativeTime(result.started_at) }}</div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { getCandyMovedGroupIDs, type AccountCandyResult } from '@/api/admin/accountCandy'
import { formatDateTime, formatRelativeTime } from '@/utils/format'

const props = defineProps<{
  result?: AccountCandyResult
  starting: boolean
  loaded: boolean
  error: boolean
  targetName?: string
}>()
const emit = defineEmits<{ test: []; details: [] }>()
const { t } = useI18n()
const statusText = computed(() => {
  if (!props.result) return t(props.error ? 'accountCandy.loadFailed' : props.loaded ? 'accountCandy.untested' : 'accountCandy.loading')
  if (props.result.passed != null) {
    if (props.result.passed) return t('accountCandy.passed')
    if (props.result.model_results?.length) return t('accountCandy.notPassed')
    return `${t('accountCandy.notPassed')} · ${props.result.answer ?? t('accountCandy.noAnswer')}`
  }
  return props.result.status === 'completed' ? t('radar.unknown') : t(`accountCandy.${props.result.status}`)
})
const statusClass = computed(() => {
  if (props.result?.passed != null) return props.result.passed
    ? 'text-emerald-600 dark:text-emerald-400'
    : 'text-red-600 dark:text-red-400'
  if (props.result?.status === 'running') return 'text-primary-600 dark:text-primary-400'
  if (props.result?.status === 'failed' || props.error) return 'text-amber-600 dark:text-amber-400'
  return 'text-gray-400 dark:text-dark-500'
})
</script>
