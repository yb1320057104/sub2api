<template>
  <section class="space-y-5">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <p class="text-sm text-gray-500">{{ t('binanceRecharge.totalsHint') }}</p>
      <div class="flex gap-2">
        <select v-model="accountId" :aria-label="t('binanceRecharge.account')" class="input" @change="page = 1; load()">
          <option value="">{{ t('binanceRecharge.allAccounts') }}</option>
          <option v-for="account in availableAccounts" :key="account.id" :value="String(account.id)">{{ account.name }} · {{ account.recipient_uid }}</option>
        </select>
        <button class="btn btn-secondary" :disabled="loading" @click="load()">{{ t('common.refresh') }}</button>
      </div>
    </div>
    <div class="grid gap-4" :class="admin ? 'sm:grid-cols-2 xl:grid-cols-4' : 'sm:grid-cols-3'" aria-live="polite">
      <div class="card p-5"><p class="text-sm text-gray-500">{{ t('binanceRecharge.totalAmount') }}</p><p data-testid="binance-total-usdt" class="mt-2 break-all font-mono text-2xl font-semibold">{{ stats?.total_amount ?? '—' }}</p></div>
      <div class="card p-5"><p class="text-sm text-gray-500">{{ t('binanceRecharge.totalCreditedAmount') }}</p><p data-testid="binance-total-credited" class="mt-2 break-all font-mono text-2xl font-semibold">{{ stats?.total_credited_amount ?? '—' }}</p></div>
      <div class="card p-5"><p class="text-sm text-gray-500">{{ t('binanceRecharge.totalOrders') }}</p><p class="mt-2 text-2xl font-semibold">{{ stats?.total_orders ?? '—' }}</p></div>
      <div v-if="admin" class="card p-5"><p class="text-sm text-gray-500">{{ t('binanceRecharge.totalUsers') }}</p><p class="mt-2 text-2xl font-semibold">{{ stats?.total ?? '—' }}</p></div>
    </div>
    <DataTable v-if="admin" :columns="columns" :data="stats?.items || []" :loading="loading">
      <template #cell-user_id="{ row }">{{ row.user_email || `#${row.user_id}` }} <span v-if="row.user_email" class="text-xs text-gray-500">#{{ row.user_id }}</span></template>
      <template #cell-total_amount="{ value }"><span class="font-mono">{{ value }}</span></template>
      <template #cell-total_credited_amount="{ value }"><span class="font-mono">{{ value }}</span></template>
    </DataTable>
    <Pagination v-if="admin && stats && stats.total > 0" :page="page" :page-size="pageSize" :total="stats.total" @update:page="changePage" @update:page-size="changePageSize" />
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminBinanceRechargeAPI, binanceRechargeAPI } from '@/api/binanceRecharge'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { BinanceRechargeAccountOption, BinanceRechargeStats } from '@/types/binanceRecharge'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
const props = defineProps<{ admin?: boolean; accounts: BinanceRechargeAccountOption[] }>()
const { t } = useI18n()
const appStore = useAppStore()
const stats = ref<BinanceRechargeStats | null>(null)
const historicalAccounts = ref<BinanceRechargeAccountOption[]>([])
const availableAccounts = computed(() => Array.from(new Map([...props.accounts, ...historicalAccounts.value].map(account => [account.id, account])).values()))
const loading = ref(false)
const accountId = ref('')
const page = ref(1)
const pageSize = ref(20)
let sequence = 0
let disposed = false
const columns = computed(() => [
  { key: 'user_id', label: t('binanceRecharge.user') },
  { key: 'total_amount', label: t('binanceRecharge.totalAmount') },
  { key: 'total_credited_amount', label: t('binanceRecharge.totalCreditedAmount') },
  { key: 'total_orders', label: t('binanceRecharge.totalOrders') },
])
async function load() {
  const current = ++sequence
  loading.value = true
  stats.value = null
  try {
    const api = props.admin ? adminBinanceRechargeAPI : binanceRechargeAPI
    const { data } = await api.getStats({ account_id: accountId.value ? Number(accountId.value) : undefined, page: page.value, page_size: pageSize.value })
    if (!disposed && current === sequence) {
      stats.value = data
      historicalAccounts.value = data.account_options || []
    }
  } catch (error) {
    if (!disposed && current === sequence) {
      stats.value = null
      appStore.showError(extractApiErrorMessage(error, t('common.error')))
    }
  } finally { if (current === sequence) loading.value = false }
}
function changePage(value: number) { page.value = value; void load() }
function changePageSize(value: number) { pageSize.value = value; page.value = 1; void load() }
onMounted(() => { void load() })
onUnmounted(() => { disposed = true })
</script>
