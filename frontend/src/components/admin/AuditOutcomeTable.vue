<template>
  <div class="space-y-3">
    <h4 class="font-medium">{{ title }}</h4>
    <p class="text-xs text-gray-500 sm:hidden">{{ t('admin.auditCenter.tableScroll') }}</p>
    <div class="overflow-x-auto">
      <table class="w-full min-w-[560px] text-left text-sm">
        <thead><tr class="border-b border-gray-200 dark:border-dark-700"><th v-if="sourceLabel" class="p-2">{{ t('admin.auditCenter.eventSources') }}</th><th class="p-2">{{ t('common.status') }}</th><th class="p-2">{{ t('admin.auditCenter.reason') }}</th><th class="p-2 text-right">{{ t('admin.auditCenter.count') }}</th><th class="p-2 text-right">{{ t('admin.auditCenter.amount') }}</th></tr></thead>
        <tbody>
          <tr v-for="(row, index) in rows" :key="index" class="border-b border-gray-100 dark:border-dark-800"><td v-if="sourceLabel" class="p-2">{{ sourceLabel(row.source ?? '') }}</td><td class="p-2">{{ statusLabel(row.status) }}</td><td class="p-2">{{ reasonLabel(row.reason) }}</td><td class="p-2 text-right tabular-nums">{{ row.count }}</td><td class="p-2 text-right tabular-nums">{{ row.amount }}</td></tr>
          <tr v-if="!rows.length"><td :colspan="sourceLabel ? 5 : 4" class="p-4 text-center text-gray-500">{{ emptyMessage ?? t('admin.auditCenter.noChargeEvidence') }}</td></tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
defineProps<{ title: string; rows: Array<{ status: string; reason: string; source?: string; count: number; amount: string }>; statusLabel: (status: string) => string; reasonLabel: (reason: string) => string; sourceLabel?: (source: string) => string; emptyMessage?: string }>()
const { t } = useI18n()
</script>
