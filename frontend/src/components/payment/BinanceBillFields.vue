<template>
  <div class="space-y-3">
    <p v-if="!visibleFields.length" class="text-sm text-gray-500">{{ t('binanceRecharge.liveBills.noFields') }}</p>
    <template v-for="(field, index) in visibleFields" :key="`${field.key}-${index}`">
      <fieldset v-if="field.type === 'object' || field.type === 'array'" class="min-w-0 rounded-lg border border-gray-200 p-3 dark:border-dark-600">
        <legend class="max-w-full break-all px-1 text-sm font-medium">{{ label(field.key, index) }}</legend>
        <BinanceBillFields v-if="field.children?.length" :fields="field.children" :array="field.type === 'array'" />
        <p v-else class="text-xs text-gray-500">{{ field.type === 'array' ? t('binanceRecharge.liveBills.emptyArray') : t('binanceRecharge.liveBills.emptyObject') }}</p>
      </fieldset>
      <dl v-else class="grid min-w-0 gap-1 border-b border-gray-100 py-2 text-sm dark:border-dark-700 sm:grid-cols-3 sm:gap-4">
        <dt class="break-all text-gray-500">{{ label(field.key, index) }}</dt>
        <dd class="select-text break-all font-mono sm:col-span-2">{{ field.type === 'null' ? t('binanceRecharge.liveBills.nullValue') : field.value }}</dd>
      </dl>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { BinanceBillField } from '@/types/binanceRecharge'
const props = defineProps<{ fields: BinanceBillField[]; array?: boolean }>()
const { t } = useI18n()
// The server returns a trade-field allowlist. Also suppress credential-like keys in the renderer.
const visibleFields = computed(() => props.fields.filter(field => !/(?:apikey|secret|signature|token|authorization|password)/i.test(field.key.replace(/[^a-z]/gi, ''))))
const translatedKeys = new Set(['transactionId', 'orderType', 'transactionTime', 'amount', 'currency', 'walletType', 'walletTypes', 'fundsDetail', 'walletAssetCost', 'payerInfo', 'receiverInfo', 'name', 'type', 'binanceId', 'accountId', 'countryCode', 'extend'])
function label(key: string, index: number) {
  if (props.array) return t('binanceRecharge.liveBills.item', { number: index + 1 })
  return translatedKeys.has(key) ? t(`binanceRecharge.liveBills.fieldLabels.${key}`) : key
}
</script>
