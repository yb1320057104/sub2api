<template>
  <section class="space-y-5">
    <p class="text-sm text-gray-500">{{ t('binanceRecharge.liveBills.description') }}</p>
    <form class="card flex flex-wrap items-end gap-4 p-4" @submit.prevent="queryBills()">
      <div class="min-w-52 flex-1">
        <label for="binance-bills-account" class="input-label">{{ t('binanceRecharge.account') }}</label>
        <Select id="binance-bills-account" v-model="accountId" class="mt-1 w-full" :disabled="loading" :options="[{ value: '', label: t('binanceRecharge.liveBills.selectAccount'), disabled: true }, ...accounts.map(account => ({ value: String(account.id), label: account.name + ' · UID ' + account.recipient_uid }))]" searchable />
      </div>
      <div><label for="binance-bills-start" class="input-label">{{ t('binanceRecharge.liveBills.startTime') }}</label><input id="binance-bills-start" v-model="startTime" class="input mt-1 w-full" type="datetime-local" step="1" required /></div>
      <div><label for="binance-bills-end" class="input-label">{{ t('binanceRecharge.liveBills.endTime') }}</label><input id="binance-bills-end" v-model="endTime" class="input mt-1 w-full" type="datetime-local" step="1" required /></div>
      <div class="flex flex-wrap gap-2">
        <button class="btn btn-primary" type="submit" :disabled="loading || !accountId">{{ loading ? t('common.loading') : t('binanceRecharge.liveBills.query') }}</button>
        <button class="btn btn-secondary" type="button" :disabled="loading || !accountId" @click="queryBills(true)">{{ t('binanceRecharge.liveBills.refreshToNow') }}</button>
      </div>
    </form>
    <p v-if="error" role="alert" class="rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-800 dark:border-red-800 dark:bg-red-900/20 dark:text-red-300">{{ error }}</p>
    <template v-if="result">
      <p class="break-words text-xs text-gray-500">{{ t('binanceRecharge.liveBills.queryScope', { account: result.account_name, uid: result.recipient_uid, start: date(result.start_time), end: date(result.end_time) }) }}</p>
      <p v-if="result.possibly_more" role="status" data-testid="binance-bills-more" class="rounded-xl border border-amber-200 bg-amber-50 p-4 text-sm text-amber-800 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-300">{{ t('binanceRecharge.liveBills.possiblyMore') }}</p>
      <div class="space-y-2">
        <p class="text-xs text-gray-500">{{ t('binanceRecharge.liveBills.filterHint') }}</p>
        <div class="flex flex-wrap gap-3">
          <input v-model="search" class="input min-w-48 flex-1" :aria-label="t('binanceRecharge.liveBills.search')" :placeholder="t('binanceRecharge.liveBills.search')" />
          <Select v-model="currency" class="min-w-36" :aria-label="t('binanceRecharge.liveBills.currency')" :options="[{ value: '', label: t('binanceRecharge.liveBills.allCurrencies') }, ...currencies.map(value => ({ value, label: value }))]" />
          <Select v-model="direction" class="min-w-36" :aria-label="t('binanceRecharge.liveBills.direction')" :options="[{ value: '', label: t('binanceRecharge.liveBills.allDirections') }, ...directions.map(value => ({ value, label: t('binanceRecharge.liveBills.directionLabels.' + value) }))]" />
        </div>
      </div>
    </template>
    <DataTable v-if="result || loading" :columns="columns" :data="visibleBills" :loading="loading">
      <template #cell-id="{ row }"><span class="break-all font-mono text-xs">{{ row.id }}</span></template>
      <template #cell-direction="{ row }"><span :class="row.direction === 'incoming' ? 'text-green-700 dark:text-green-300' : row.direction === 'outgoing' ? 'text-red-700 dark:text-red-300' : 'text-gray-500'">{{ t(`binanceRecharge.liveBills.directionLabels.${row.direction}`) }}</span></template>
      <template #cell-amount="{ row }"><span data-testid="binance-bill-amount" class="break-all font-mono">{{ row.amount }} {{ row.currency }}</span></template>
      <template #cell-time="{ row }"><span class="text-xs">{{ date(row.time) }}</span></template>
      <template #cell-actions="{ row }"><button class="btn btn-secondary text-xs" @click="selected = row">{{ t('common.view') }}</button></template>
    </DataTable>
    <p v-else-if="!error" class="card p-8 text-center text-sm text-gray-500">{{ accounts.length ? t('binanceRecharge.liveBills.ready') : t('binanceRecharge.liveBills.noAccounts') }}</p>
    <p v-if="result" class="text-xs text-gray-500">{{ t('binanceRecharge.liveBills.resultCount', { visible: visibleBills.length, count: result.items.length, limit: result.limit }) }}</p>
    <BaseDialog :show="!!selected && !!result" :title="t('binanceRecharge.liveBills.details')" width="wide" @close="selected = null">
      <div v-if="selected" class="space-y-4">
        <dl class="grid gap-3 text-sm sm:grid-cols-2">
          <div class="sm:col-span-2"><dt class="text-gray-500">{{ t('binanceRecharge.transactionID') }}</dt><dd class="break-all font-mono">{{ selected.id }}</dd></div>
          <div><dt class="text-gray-500">{{ t('binanceRecharge.liveBills.amount') }}</dt><dd class="break-all font-mono">{{ selected.amount }} {{ selected.currency }}</dd></div>
          <div><dt class="text-gray-500">{{ t('binanceRecharge.liveBills.time') }}</dt><dd>{{ date(selected.time) }}</dd></div>
          <div><dt class="text-gray-500">{{ t('binanceRecharge.liveBills.payerUID') }}</dt><dd class="break-all font-mono">{{ selected.payer_uid || '—' }}</dd></div>
          <div><dt class="text-gray-500">{{ t('binanceRecharge.liveBills.receiverUID') }}</dt><dd class="break-all font-mono">{{ selected.receiver_uid || '—' }}</dd></div>
        </dl>
        <h3 class="text-sm font-semibold">{{ t('binanceRecharge.liveBills.fields') }}</h3>
        <BinanceBillFields :fields="selected.fields || []" />
      </div>
    </BaseDialog>
  </section>
</template>

<script setup lang="ts">
import Select from '@/components/common/Select.vue'
import { computed, nextTick, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminBinanceRechargeAPI } from '@/api/binanceRecharge'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { BinanceLiveBill, BinanceLiveBills as BillsResult, BinanceRechargeAccountOption } from '@/types/binanceRecharge'
import DataTable from '@/components/common/DataTable.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import BinanceBillFields from './BinanceBillFields.vue'
const props = defineProps<{ accounts: BinanceRechargeAccountOption[] }>()
const { t } = useI18n()
function localDateTime(date: Date): string {
  return new Date(date.getTime() - date.getTimezoneOffset() * 60000).toISOString().slice(0, 19)
}
const today = new Date()
const startOfDay = new Date(today.getFullYear(), today.getMonth(), today.getDate())
const accountId = ref('')
const startTime = ref(localDateTime(startOfDay))
const endTime = ref(localDateTime(today))
const loading = ref(false)
const error = ref('')
const result = ref<BillsResult | null>(null)
const selected = ref<BinanceLiveBill | null>(null)
const search = ref('')
const currency = ref('')
const direction = ref('')
const directions = ['incoming', 'outgoing', 'self', 'unknown'] as const
let sequence = 0
let disposed = false
const date = (value: string) => new Date(value).toLocaleString()
const currencies = computed(() => [...new Set((result.value?.items || []).map(bill => bill.currency))].sort())
const visibleBills = computed(() => {
  const keyword = search.value.trim().toLowerCase()
  return (result.value?.items || []).filter(bill =>
    (!currency.value || bill.currency === currency.value) &&
    (!direction.value || bill.direction === direction.value) &&
    (!keyword || [bill.id, bill.kind, bill.payer_uid, bill.receiver_uid].some(value => String(value || '').toLowerCase().includes(keyword))),
  ).slice().sort((a, b) => new Date(b.time).getTime() - new Date(a.time).getTime())
})
const columns = computed(() => [
  { key: 'id', label: t('binanceRecharge.transactionID') },
  { key: 'direction', label: t('binanceRecharge.liveBills.direction') },
  { key: 'kind', label: t('binanceRecharge.liveBills.kind') },
  { key: 'amount', label: t('binanceRecharge.liveBills.amount') },
  { key: 'time', label: t('binanceRecharge.liveBills.time') },
  { key: 'actions', label: t('common.actions') },
])
watch(() => props.accounts, accounts => {
  if (!accounts.some(account => String(account.id) === accountId.value)) accountId.value = accounts.length === 1 ? String(accounts[0].id) : ''
}, { immediate: true })
watch([accountId, startTime, endTime], () => {
  // A different account or date range must never display a previous account's result.
  sequence++
  result.value = null
  selected.value = null
  error.value = ''
  loading.value = false
})
async function queryBills(refreshToNow = false) {
  if (loading.value) return
  if (refreshToNow) {
    endTime.value = localDateTime(new Date())
    await nextTick()
  }
  const account = props.accounts.find(item => String(item.id) === accountId.value)
  if (!account) { error.value = t('binanceRecharge.liveBills.selectAccount'); return }
  const start = new Date(startTime.value)
  const end = new Date(endTime.value)
  if (!Number.isFinite(start.getTime()) || !Number.isFinite(end.getTime()) || end <= start || end.getTime() - start.getTime() > 89 * 86400000) {
    error.value = t('binanceRecharge.liveBills.invalidRange'); return
  }
  const request = ++sequence
  loading.value = true
  error.value = ''
  result.value = null
  selected.value = null
  try {
    const { data } = await adminBinanceRechargeAPI.getBills(account.id, { start_time: start.toISOString(), end_time: end.toISOString() })
    if (!disposed && request === sequence) {
      result.value = data
      search.value = ''
      currency.value = ''
      direction.value = ''
    }
  } catch (err) {
    if (!disposed && request === sequence) error.value = extractApiErrorMessage(err, t('binanceRecharge.liveBills.unavailable'))
  } finally { if (request === sequence) loading.value = false }
}
onUnmounted(() => { disposed = true; sequence++ })
</script>
