<template>
  <div class="space-y-5">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <span class="break-all font-mono text-sm text-gray-500">{{ order.order_no }}</span>
      <BinanceOrderStatus :status="order.status" />
    </div>
    <p v-if="showPaymentInstructions && order.status === 'PENDING'" class="text-sm text-gray-600 dark:text-gray-300">{{ t('binanceRecharge.instruction') }}</p>
    <div class="rounded-xl border border-primary-200 bg-primary-50 p-4 dark:border-primary-800 dark:bg-primary-900/20">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div>
          <p class="text-xs text-gray-500">{{ t('binanceRecharge.exactAmount') }}</p>
          <p data-testid="binance-exact-amount" class="mt-1 break-all font-mono text-2xl font-semibold text-gray-900 dark:text-white">{{ order.pay_amount }} <span class="text-sm">USDT</span></p>
        </div>
        <button v-if="order.status === 'PENDING'" class="btn btn-secondary" data-testid="binance-copy-amount" @click="copyToClipboard(order.pay_amount)">{{ t('binanceRecharge.copyAmount') }}</button>
      </div>
      <div class="mt-4 border-t border-primary-200 pt-4 dark:border-primary-800">
        <p class="text-xs text-gray-500">{{ t('binanceRecharge.systemAmount') }}</p>
        <p data-testid="binance-system-amount" class="mt-1 break-all font-mono text-xl font-semibold text-gray-900 dark:text-white">{{ order.system_amount }} <span class="text-sm">USD</span></p>
        <p data-testid="binance-order-rate" class="mt-2 text-sm text-gray-600 dark:text-gray-300">{{ t('binanceRecharge.snapshotRate', { multiplier: formatBinanceMultiplier(order.credit_multiplier) }) }}</p>
        <p class="mt-1 break-all font-mono text-xs text-gray-500">{{ order.pay_amount }} USDT × {{ formatBinanceMultiplier(order.credit_multiplier) }} = {{ order.system_amount }} USD</p>
        <p v-if="order.status !== 'COMPLETED'" class="mt-2 text-xs text-gray-500">{{ t('binanceRecharge.systemAmountHint') }}</p>
      </div>
      <div class="mt-4 flex flex-wrap items-center justify-between gap-3 border-t border-primary-200 pt-4 dark:border-primary-800">
        <div><p class="text-xs text-gray-500">{{ t('binanceRecharge.uid') }} · {{ order.account_name }}</p><p class="mt-1 break-all font-mono text-lg font-semibold text-gray-900 dark:text-white">{{ order.recipient_uid }}</p></div>
        <button v-if="order.status === 'PENDING'" class="btn btn-secondary" @click="copyToClipboard(order.recipient_uid)">{{ t('binanceRecharge.copyUID') }}</button>
      </div>
    </div>
    <p v-if="order.status === 'COMPLETED'" data-testid="binance-credited" class="rounded-lg bg-green-50 p-3 text-sm text-green-800 dark:bg-green-900/20 dark:text-green-300">{{ t('binanceRecharge.completed', { amount: order.credited_amount }) }}</p>
    <p v-else-if="showPaymentInstructions && order.status === 'PENDING'" class="text-sm text-gray-600 dark:text-gray-300">{{ t('binanceRecharge.creditHint', { payAmount: order.pay_amount, systemAmount: order.system_amount }) }}</p>
    <p v-if="showPaymentInstructions" class="text-sm text-amber-700 dark:text-amber-300">{{ order.status === 'PENDING' ? t('binanceRecharge.noChain') : order.status !== 'COMPLETED' ? t('binanceRecharge.expiryHint') : '' }}</p>
    <dl class="grid grid-cols-1 gap-3 text-sm sm:grid-cols-2">
      <div><dt class="text-gray-500">{{ t('binanceRecharge.createdAt') }}</dt><dd>{{ date(order.created_at) }}</dd></div>
      <div><dt class="text-gray-500">{{ t('binanceRecharge.expiresAt') }}</dt><dd>{{ date(order.expires_at) }}</dd></div>
      <div v-if="order.paid_at"><dt class="text-gray-500">{{ t('binanceRecharge.paidAt') }}</dt><dd>{{ date(order.paid_at) }}</dd></div>
      <div v-if="order.completed_at"><dt class="text-gray-500">{{ t('binanceRecharge.completedAt') }}</dt><dd>{{ date(order.completed_at) }}</dd></div>
      <div v-if="order.transaction_id" class="sm:col-span-2"><dt class="text-gray-500">{{ t('binanceRecharge.transactionID') }}</dt><dd class="break-all font-mono">{{ order.transaction_id }}</dd></div>
      <div v-if="order.review_reason" class="sm:col-span-2"><dt class="text-gray-500">{{ t('binanceRecharge.reviewReason') }}</dt><dd class="break-words">{{ order.review_reason }}</dd></div>
    </dl>
    <button v-if="order.status !== 'COMPLETED'" class="btn btn-primary w-full" :disabled="busy" @click="emit('refresh')">{{ busy ? t('binanceRecharge.refreshing') : t('binanceRecharge.refreshOrder') }}</button>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useClipboard } from '@/composables/useClipboard'
import { formatBinanceMultiplier } from './binanceAmounts'
import type { BinanceRechargeOrder } from '@/types/binanceRecharge'
import BinanceOrderStatus from './BinanceOrderStatus.vue'
defineProps<{ order: BinanceRechargeOrder; busy?: boolean; showPaymentInstructions?: boolean }>()
const emit = defineEmits<{ refresh: [] }>()
const { t } = useI18n()
const { copyToClipboard } = useClipboard()
const date = (value: string) => new Date(value).toLocaleString()
</script>
