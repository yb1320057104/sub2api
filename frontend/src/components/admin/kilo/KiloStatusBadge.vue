<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

const props = defineProps<{ status: string }>()
const { t, te } = useI18n()
const label = computed(() => te(`kilo.status.${props.status}`) ? t(`kilo.status.${props.status}`) : t('kilo.status.unknown'))
const tone = computed(() => {
  if (['bound', 'ready', 'active', 'approved', 'imported', 'completed', 'succeeded'].includes(props.status)) return 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-300'
  if (['failed', 'error', 'denied', 'expired', 'conflict', 'unhealthy'].includes(props.status)) return 'bg-red-50 text-red-700 dark:bg-red-900/20 dark:text-red-300'
  if (['awaiting_authorization', 'pending', 'queued', 'binding', 'running', 'draining', 'verifying', 'needs_action', 'waiting'].includes(props.status)) return 'bg-amber-50 text-amber-800 dark:bg-amber-900/20 dark:text-amber-300'
  return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-dark-300'
})
</script>

<template>
  <span class="inline-flex items-center rounded-md px-2 py-1 text-xs font-medium" :class="tone">{{ label }}</span>
</template>
