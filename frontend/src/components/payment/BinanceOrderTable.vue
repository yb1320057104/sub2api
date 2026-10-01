<template>
  <DataTable :columns="columns" :data="orders" :loading="loading">
    <template #cell-order_no="{ value }"><span class="font-mono text-xs">{{ value }}</span></template>
    <template #cell-user_id="{ row }"><span>{{ row.user_email || `#${row.user_id}` }}</span></template>
    <template #cell-recipient_uid="{ row }">
      <div>{{ row.account_name }}</div><div class="font-mono text-xs text-gray-500">UID {{ row.recipient_uid }}</div>
    </template>
    <template #cell-pay_amount="{ row }">
      <div class="font-mono font-semibold">{{ row.pay_amount }} USDT</div>
    </template>
    <template #cell-system_amount="{ row }">
      <div class="font-mono font-semibold">{{ row.system_amount }} USD</div>
      <div class="mt-1 text-xs text-gray-500">{{ t('binanceRecharge.creditedAmount') }}: <span class="font-mono">{{ row.credited_amount }} USD</span></div>
    </template>
    <template #cell-status="{ row }"><BinanceOrderStatus :status="row.status" /></template>
    <template #cell-created_at="{ row }">
      <div class="text-xs">{{ date(row.created_at) }}</div>
      <div class="mt-1 text-xs text-gray-500">{{ t('binanceRecharge.expiresAt') }}: {{ date(row.expires_at) }}</div>
    </template>
    <template #cell-actions="{ row }">
      <button class="btn btn-secondary text-xs" @click="emit('select', row)">{{ t('common.view') }}</button>
    </template>
  </DataTable>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import DataTable from '@/components/common/DataTable.vue'
import type { Column } from '@/components/common/types'
import BinanceOrderStatus from './BinanceOrderStatus.vue'
import type { BinanceRechargeOrder } from '@/types/binanceRecharge'
const props = defineProps<{ orders: BinanceRechargeOrder[]; loading: boolean; admin?: boolean }>()
const emit = defineEmits<{ select: [order: BinanceRechargeOrder] }>()
const { t } = useI18n()
const date = (value: string) => new Date(value).toLocaleString()
const columns = computed<Column[]>(() => [
  { key: 'order_no', label: t('binanceRecharge.orderNo') },
  ...(props.admin ? [{ key: 'user_id', label: t('binanceRecharge.user') }] : []),
  { key: 'recipient_uid', label: t('binanceRecharge.account') },
  { key: 'pay_amount', label: t('binanceRecharge.paidAmount') },
  { key: 'system_amount', label: t('binanceRecharge.systemAmount') },
  { key: 'status', label: t('binanceRecharge.statusLabel') },
  { key: 'created_at', label: t('binanceRecharge.createdAt') },
  { key: 'actions', label: t('common.actions') },
])
</script>
