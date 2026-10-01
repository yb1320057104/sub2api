<template>
  <span class="inline-flex rounded-full px-2.5 py-1 text-xs font-medium" :class="colors[status] || colors.REVIEW">
    {{ t(`binanceRecharge.status.${status}`) }}
  </span>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { BinanceRechargeStatus } from '@/types/binanceRecharge'
defineProps<{ status: BinanceRechargeStatus }>()
const { t } = useI18n()
const colors: Record<BinanceRechargeStatus, string> = {
  PENDING: 'bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-300',
  COMPLETED: 'bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-300',
  EXPIRED: 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-400',
  REVIEW: 'bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-300',
}
</script>
