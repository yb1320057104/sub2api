<template>
  <AdminBinanceRechargeView v-if="authStore.isAdmin" />
  <AppLayout v-else>
    <div class="space-y-6">
      <div class="flex gap-2 border-b border-gray-200 pb-3 dark:border-dark-700">
        <button v-for="tab in ['orders', 'totals'] as const" :key="tab" class="btn" :class="activeTab === tab ? 'btn-primary' : 'btn-secondary'" @click="activeTab = tab">{{ t(`binanceRecharge.${tab}`) }}</button>
      </div>
      <BinanceRechargeStats v-if="activeTab === 'totals'" :accounts="config?.accounts || []" />
      <template v-else>
        <div v-if="configLoading" class="card p-6 text-sm text-gray-500">{{ t('common.loading') }}</div>
        <div v-else-if="configFailed" class="card flex flex-wrap items-center justify-between gap-3 p-5 text-sm text-red-700 dark:text-red-300"><span>{{ t('binanceRecharge.configLoadFailed') }}</span><button class="btn btn-secondary" @click="loadConfiguration()">{{ t('common.refresh') }}</button></div>
        <div v-else-if="config?.enabled" class="grid gap-5 xl:grid-cols-2">
          <form class="card space-y-4 p-5" @submit.prevent="createOrder">
            <h2 class="text-lg font-semibold">{{ t('binanceRecharge.createOrder') }}</h2>
            <p class="text-sm text-gray-500">{{ t('binanceRecharge.description') }}</p>
            <div>
              <label for="binance-account" class="input-label">{{ t('binanceRecharge.account') }}</label>
              <select id="binance-account" v-model="accountId" class="input mt-1 w-full" required>
                <option v-for="account in config.accounts" :key="account.id" :value="account.id">{{ account.name }} · UID {{ account.recipient_uid }}</option>
              </select>
              <p v-if="selectedAccount" data-testid="binance-account-rate" class="mt-2 text-sm font-medium text-primary-700 dark:text-primary-300">{{ t('binanceRecharge.exchangeRate', { multiplier: formatBinanceMultiplier(selectedAccount.credit_multiplier) }) }}</p>
            </div>
            <div>
              <label for="binance-amount" class="input-label">{{ t('binanceRecharge.amount') }}</label>
              <input id="binance-amount" v-model="amountText" class="input mt-1 w-full" type="text" inputmode="numeric" pattern="[0-9]+" autocomplete="off" required />
              <p class="mt-2 text-xs text-gray-500">{{ t('binanceRecharge.amountHint') }}</p>
            </div>
            <p class="text-xs text-amber-700 dark:text-amber-300">{{ t('binanceRecharge.noChain') }}</p>
            <button class="btn btn-primary w-full" type="submit" :disabled="creating || !validAmount || !accountId">{{ creating ? t('common.processing') : t('binanceRecharge.createOrder') }}</button>
          </form>
          <div v-if="selectedOrder" class="card p-5" aria-live="polite">
            <BinanceOrderDetails :order="selectedOrder" :busy="refreshing" show-payment-instructions @refresh="refreshOrder" />
            <p class="mt-4 text-xs text-gray-500">{{ t('binanceRecharge.pollHint', { seconds: pollSeconds }) }}</p>
          </div>
        </div>
        <div v-else class="card p-5 text-sm text-gray-500">{{ t('binanceRecharge.unavailable') }}</div>
        <div v-if="selectedOrder && !config?.enabled" class="card p-5"><BinanceOrderDetails :order="selectedOrder" :busy="refreshing" show-payment-instructions @refresh="refreshOrder" /></div>
        <section class="space-y-4">
          <div class="flex flex-wrap items-center justify-between gap-3">
            <h2 class="text-lg font-semibold">{{ t('binanceRecharge.history') }}</h2>
            <div class="flex flex-wrap gap-2">
              <select v-model="accountFilter" :aria-label="t('binanceRecharge.account')" class="input" @change="page = 1; loadOrders()">
                <option value="">{{ t('binanceRecharge.allAccounts') }}</option>
                <option v-for="account in filterAccounts" :key="account.id" :value="String(account.id)">{{ account.name }} · {{ account.recipient_uid }}</option>
              </select>
              <select v-model="status" :aria-label="t('binanceRecharge.statusLabel')" class="input" @change="page = 1; loadOrders()">
                <option value="">{{ t('binanceRecharge.allStatuses') }}</option>
                <option v-for="value in statuses" :key="value" :value="value">{{ t(`binanceRecharge.status.${value}`) }}</option>
              </select>
              <button class="btn btn-secondary" :disabled="ordersLoading" @click="loadOrders()">{{ t('common.refresh') }}</button>
            </div>
          </div>
          <BinanceOrderTable :orders="orders" :loading="ordersLoading" @select="selectOrder" />
          <Pagination v-if="total > 0" :page="page" :page-size="pageSize" :total="total" @update:page="changePage" @update:page-size="changePageSize" />
        </section>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { useBinanceRechargeStore } from '@/stores/binanceRecharge'
import { binanceRechargeAPI } from '@/api/binanceRecharge'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatBinanceMultiplier } from '@/components/payment/binanceAmounts'
import type { BinanceRechargeAccountOption, BinanceRechargeOrder, BinanceRechargeStatus } from '@/types/binanceRecharge'
import AppLayout from '@/components/layout/AppLayout.vue'
import Pagination from '@/components/common/Pagination.vue'
import BinanceOrderTable from '@/components/payment/BinanceOrderTable.vue'
import BinanceOrderDetails from '@/components/payment/BinanceOrderDetails.vue'
import BinanceRechargeStats from '@/components/payment/BinanceRechargeStats.vue'
import AdminBinanceRechargeView from '@/views/admin/orders/AdminBinanceRechargeView.vue'

const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const rechargeStore = useBinanceRechargeStore()
const config = computed(() => rechargeStore.config)
const configLoading = ref(true)
const configFailed = ref(false)
const activeTab = ref<'orders' | 'totals'>('orders')
const accountId = ref<number | null>(null)
const selectedAccount = computed(() => config.value?.accounts.find(account => account.id === accountId.value))
const accountFilter = ref('')
const historicalAccounts = ref<BinanceRechargeAccountOption[]>([])
const amountText = ref('10')
const validAmount = computed(() => /^\d+$/.test(amountText.value) && Number(amountText.value) >= 1 && Number(amountText.value) <= 1000000)
const creating = ref(false)
const refreshing = ref(false)
const ordersLoading = ref(false)
const orders = ref<BinanceRechargeOrder[]>([])
const filterAccounts = computed(() => Array.from(new Map([
  ...(config.value?.accounts || []), ...historicalAccounts.value,
  ...orders.value.map(order => ({ id: order.account_id, name: order.account_name, recipient_uid: order.recipient_uid, order_ttl_minutes: 15, credit_multiplier: order.credit_multiplier })),
].map(account => [account.id, account])).values()))
const selectedOrder = ref<BinanceRechargeOrder | null>(null)
const statuses: BinanceRechargeStatus[] = ['PENDING', 'COMPLETED', 'EXPIRED', 'REVIEW']
const status = ref<BinanceRechargeStatus | ''>('')
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const pollSeconds = computed(() => Math.max(10, config.value?.poll_interval_seconds || 30))
let disposed = false
let listSequence = 0
let pollTimer: ReturnType<typeof setTimeout> | undefined

function reportError(error: unknown) { appStore.showError(extractApiErrorMessage(error, t('common.error'))) }

async function applyOrder(order: BinanceRechargeOrder) {
  // An earlier GET can finish after a successful explicit refresh. Never undo a confirmed credit in the UI.
  if (selectedOrder.value?.id === order.id && selectedOrder.value.status === 'COMPLETED' && order.status !== 'COMPLETED') {
    const index = orders.value.findIndex(item => item.id === order.id)
    if (index >= 0) orders.value[index] = selectedOrder.value
    return
  }
  const becameCompleted = selectedOrder.value?.id === order.id && selectedOrder.value.status !== 'COMPLETED' && order.status === 'COMPLETED'
  selectedOrder.value = order
  const index = orders.value.findIndex(item => item.id === order.id)
  if (index >= 0) orders.value[index] = order
  if (becameCompleted) {
    appStore.showSuccess(t('binanceRecharge.completed', { amount: order.credited_amount }))
    try { await authStore.refreshUser() } catch { /* The confirmed order remains credited if profile refresh fails. */ }
  }
}

async function loadOrders(silent = false) {
  const sequence = ++listSequence
  if (!silent) ordersLoading.value = true
  try {
    const { data } = await binanceRechargeAPI.getOrders({ page: page.value, page_size: pageSize.value, status: status.value, account_id: accountFilter.value ? Number(accountFilter.value) : undefined })
    if (disposed || sequence !== listSequence) return
    orders.value = data.items || []
    total.value = data.total
    const current = orders.value.find(order => order.id === selectedOrder.value?.id)
    if (current) await applyOrder(current)
  } catch (error) { if (!silent) reportError(error) }
  finally { if (sequence === listSequence) ordersLoading.value = false }
}

async function createOrder() {
  if (creating.value) return
  if (!validAmount.value || !accountId.value) { appStore.showError(t('binanceRecharge.invalidAmount')); return }
  creating.value = true
  try {
    const { data } = await binanceRechargeAPI.createOrder(accountId.value, Number(amountText.value))
    if (disposed) return
    selectedOrder.value = data
    page.value = 1
    status.value = ''
    accountFilter.value = ''
    await loadOrders()
  } catch (error) { reportError(error) }
  finally { creating.value = false }
}

async function selectOrder(order: BinanceRechargeOrder) {
  selectedOrder.value = order
  try {
    const { data } = await binanceRechargeAPI.getOrder(order.id)
    if (!disposed && selectedOrder.value?.id === order.id) await applyOrder(data)
  } catch (error) { reportError(error) }
}

async function refreshOrder() {
  if (!selectedOrder.value || refreshing.value) return
  const id = selectedOrder.value.id
  refreshing.value = true
  try {
    const { data } = await binanceRechargeAPI.refreshOrder(id)
    if (!disposed && selectedOrder.value?.id === id) await applyOrder(data)
  } catch (error) { reportError(error) }
  finally { refreshing.value = false }
}

function changePage(value: number) { page.value = value; void loadOrders() }
function changePageSize(value: number) { pageSize.value = value; page.value = 1; void loadOrders() }

async function poll() {
  if (disposed) return
  if (!document.hidden) {
    // Poll local order state; the server performs Binance reconciliation independently.
    if (selectedOrder.value && selectedOrder.value.status !== 'COMPLETED' && !refreshing.value) {
      const id = selectedOrder.value.id
      try {
        const { data } = await binanceRechargeAPI.getOrder(id)
        if (!disposed && selectedOrder.value?.id === id) await applyOrder(data)
      } catch { /* A failed lookup must not change the known payment status. */ }
    }
    if (activeTab.value === 'orders') await loadOrders(true)
  }
  if (!disposed) pollTimer = setTimeout(poll, pollSeconds.value * 1000)
}

onMounted(async () => {
  if (authStore.isAdmin) return
  try {
    await loadConfiguration()
    await loadOrders()
    try {
      const { data } = await binanceRechargeAPI.getStats({})
      if (!disposed) historicalAccounts.value = data.account_options || []
    } catch { /* History remains usable if aggregate statistics cannot be loaded. */ }
  } finally {
    configLoading.value = false
    if (!disposed) pollTimer = setTimeout(poll, pollSeconds.value * 1000)
  }
})
async function loadConfiguration() {
  configLoading.value = true
  const result = await rechargeStore.fetchConfig(true)
  if (!disposed) {
    configFailed.value = result === null
    accountId.value = config.value?.accounts[0]?.id ?? null
    configLoading.value = false
  }
}
onUnmounted(() => { disposed = true; clearTimeout(pollTimer) })
</script>
