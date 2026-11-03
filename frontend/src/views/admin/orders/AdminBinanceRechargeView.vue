<template>
  <AppLayout>
    <div class="space-y-6">
      <div class="flex flex-wrap gap-2 border-b border-gray-200 pb-3 dark:border-dark-700">
        <button v-for="tab in tabs" :key="tab" class="btn" :class="activeTab === tab ? 'btn-primary' : 'btn-secondary'" @click="activeTab = tab">{{ t(tab === 'dailyUsage' || tab === 'liveBills' ? `binanceRecharge.${tab}.title` : `binanceRecharge.${tab}`) }}</button>
      </div>
      <template v-if="activeTab === 'accounts'">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <p class="max-w-3xl text-sm text-gray-500">{{ t('binanceRecharge.enabledHint') }}</p>
          <div class="flex gap-2"><button class="btn btn-secondary" :disabled="accountsLoading" @click="loadAccounts()">{{ t('common.refresh') }}</button><button class="btn btn-primary" @click="editAccount()">{{ t('binanceRecharge.addAccount') }}</button></div>
        </div>
        <div v-if="accountsLoading" class="card p-6 text-sm text-gray-500">{{ t('common.loading') }}</div>
        <div v-else-if="!accounts.length" class="card p-8 text-center text-gray-500">{{ t('binanceRecharge.noAccounts') }}</div>
        <div v-else class="grid gap-4 xl:grid-cols-2">
          <article v-for="account in accounts" :key="account.id" class="card space-y-4 p-5">
            <div class="flex flex-wrap items-start justify-between gap-3">
              <div><h2 class="text-base font-semibold">{{ account.name }}</h2><p class="mt-1 break-all font-mono text-sm text-gray-500">UID {{ account.recipient_uid }}</p></div>
              <span class="rounded-full px-2.5 py-1 text-xs" :class="account.enabled ? 'bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-300' : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-400'">{{ account.enabled ? t('common.enabled') : t('common.disabled') }}</span>
            </div>
            <dl class="grid grid-cols-2 gap-3 text-sm">
              <div><dt class="text-gray-500">{{ t('binanceRecharge.credentials') }}</dt><dd>{{ account.api_key_configured && account.api_secret_configured ? t('binanceRecharge.credentialsConfigured') : t('binanceRecharge.credentialsMissing') }}</dd></div>
              <div><dt class="text-gray-500">{{ t('binanceRecharge.ttl') }}</dt><dd>{{ t('binanceRecharge.minutes', { minutes: account.order_ttl_minutes }) }}</dd></div>
              <div class="col-span-2"><dt class="text-gray-500">{{ t('binanceRecharge.proxy') }}</dt><dd>{{ account.proxy_configured ? t(account.proxy_preflight ? 'binanceRecharge.proxyWithPreflight' : 'binanceRecharge.proxyConfigured') : t('binanceRecharge.proxyInherited') }}</dd></div>
              <div v-if="account.backup_proxy_configured" class="col-span-2"><dt class="text-gray-500">{{ t('binanceRecharge.backupProxy') }}</dt><dd>{{ t(account.backup_proxy_preflight ? 'binanceRecharge.proxyWithPreflight' : 'binanceRecharge.proxyConfigured') }}</dd></div>
              <div v-if="account.backup_proxy_configured && account.query_status?.attempt" class="col-span-2 rounded-lg bg-blue-50 p-3 text-blue-800 dark:bg-blue-900/20 dark:text-blue-200" :data-testid="`binance-route-status-${account.id}`"><dt>{{ t('binanceRecharge.routeProgress', { route: t(`binanceRecharge.routes.${account.query_status.route}`), attempt: account.query_status.attempt }) }}</dt><dd class="mt-1 text-xs">{{ routeStateLabel(account) }}</dd></div>
              <div class="col-span-2"><dt class="text-gray-500">{{ t('binanceRecharge.creditMultiplier') }}</dt><dd>{{ t('binanceRecharge.exchangeRate', { multiplier: formatBinanceMultiplier(account.credit_multiplier) }) }}</dd></div>
              <div class="col-span-2"><dt class="text-gray-500">{{ t('binanceRecharge.lastChecked') }}</dt><dd>{{ account.last_checked_at ? date(account.last_checked_at) : t('binanceRecharge.neverChecked') }}</dd></div>
              <div class="col-span-2"><dt class="text-gray-500">{{ t('binanceRecharge.checkStatus') }}</dt><dd :data-testid="`binance-check-state-${account.id}`">{{ checkStateLabel(account) }}</dd><dd v-if="account.check_available_at && account.check_state !== 'ready'" class="mt-1 text-xs text-gray-500">{{ t('binanceRecharge.checkAvailableAt', { time: date(account.check_available_at) }) }}</dd><dd v-if="remainingWait(account) > 0" class="mt-1 text-xs text-gray-500" :data-testid="`binance-check-countdown-${account.id}`">{{ t('binanceRecharge.checkCountdown', { seconds: remainingWait(account) }) }}</dd></div>
            </dl>
            <p v-if="account.last_error" class="break-words rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">{{ t('binanceRecharge.lastError') }}: {{ account.last_error }}</p>
            <div class="flex flex-wrap justify-end gap-2">
              <button class="btn btn-secondary" :data-testid="`binance-check-${account.id}`" :disabled="checkingId !== null || remainingWait(account) > 0" @click="checkAccount(account)">{{ checkingId === account.id ? t('binanceRecharge.refreshing') : t('binanceRecharge.check') }}</button>
              <button class="btn btn-secondary" @click="editAccount(account)">{{ t('common.edit') }}</button>
            </div>
          </article>
        </div>
      </template>
      <template v-else-if="activeTab === 'orders'">
        <form class="card flex flex-wrap gap-3 p-4" @submit.prevent="page = 1; loadOrders()">
          <input v-model="keyword" class="input min-w-48 flex-1" :placeholder="t('binanceRecharge.search')" :aria-label="t('binanceRecharge.search')" />
          <Select v-model="accountFilter" class="min-w-48" :aria-label="t('binanceRecharge.account')" :options="[{ value: '', label: t('binanceRecharge.allAccounts') }, ...accounts.map(account => ({ value: String(account.id), label: account.name + ' · ' + account.recipient_uid }))]" @change="page = 1; loadOrders()" />
          <Select v-model="status" class="min-w-36" :aria-label="t('binanceRecharge.statusLabel')" :options="[{ value: '', label: t('binanceRecharge.allStatuses') }, ...statuses.map(value => ({ value, label: t('binanceRecharge.status.' + value) }))]" @change="page = 1; loadOrders()" />
          <button class="btn btn-secondary" type="submit" :disabled="ordersLoading">{{ t('common.refresh') }}</button>
        </form>
        <BinanceOrderTable :orders="orders" :loading="ordersLoading" admin @select="selectedOrder = $event" />
        <Pagination v-if="total > 0" :page="page" :page-size="pageSize" :total="total" @update:page="changePage" @update:page-size="changePageSize" />
      </template>
      <BinanceRechargeStats v-else-if="activeTab === 'totals'" admin :accounts="accounts" />
      <AdminDailyUsage v-else-if="activeTab === 'dailyUsage'" />
      <BinanceLiveBills v-else :accounts="accounts" />
    </div>
    <BaseDialog :show="showAccountDialog" :title="editingAccount ? t('binanceRecharge.editAccount') : t('binanceRecharge.addAccount')" :close-on-escape="!saving" :close-on-click-outside="!saving" @close="closeAccountDialog">
      <form id="binance-account-form" class="space-y-4" autocomplete="off" @submit.prevent="saveAccount">
        <div><label for="binance-name" class="input-label">{{ t('binanceRecharge.accountName') }}</label><input id="binance-name" v-model="form.name" class="input mt-1 w-full" maxlength="100" required /></div>
        <div><label for="binance-uid" class="input-label">{{ t('binanceRecharge.uid') }}</label><input id="binance-uid" v-model="form.recipient_uid" class="input mt-1 w-full" inputmode="numeric" pattern="[0-9]+" :disabled="editingAccount?.has_orders" required /><p v-if="editingAccount?.has_orders" class="mt-1 text-xs text-gray-500">{{ t('binanceRecharge.uidLocked') }}</p></div>
        <div><label for="binance-multiplier" class="input-label">{{ t('binanceRecharge.creditMultiplier') }}</label><input id="binance-multiplier" v-model="form.credit_multiplier" class="input mt-1 w-full" type="text" inputmode="decimal" maxlength="11" required aria-describedby="binance-multiplier-hint" /><p id="binance-multiplier-hint" class="mt-2 text-xs text-gray-500">{{ t('binanceRecharge.multiplierHint') }}</p></div>
        <p class="text-xs text-gray-500">{{ t('binanceRecharge.credentialsHint') }}</p>
        <div><label for="binance-api-key" class="input-label">{{ t('binanceRecharge.apiKey') }}</label><input id="binance-api-key" v-model="form.api_key" type="password" autocomplete="new-password" class="input mt-1 w-full" :required="!editingAccount" /></div>
        <div><label for="binance-api-secret" class="input-label">{{ t('binanceRecharge.apiSecret') }}</label><input id="binance-api-secret" v-model="form.api_secret" type="password" autocomplete="new-password" class="input mt-1 w-full" :required="!editingAccount" /><p class="mt-2 text-xs text-gray-500">{{ t('binanceRecharge.secretHint') }}</p></div>
        <div>
          <label for="binance-proxy" class="input-label">{{ t('binanceRecharge.proxy') }}</label>
          <input id="binance-proxy" v-model="form.proxy_url" type="password" autocomplete="new-password" class="input mt-1 w-full" maxlength="2048" :disabled="saving || form.clear_proxy" :placeholder="t(editingAccount?.proxy_configured ? 'binanceRecharge.proxyKeepPlaceholder' : 'binanceRecharge.proxyPlaceholder')" />
          <p class="mt-2 text-xs text-gray-500">{{ t('binanceRecharge.proxyHint') }}</p>
          <label v-if="editingAccount?.proxy_configured" class="mt-2 flex items-center gap-2 text-sm"><input id="binance-clear-proxy" v-model="form.clear_proxy" type="checkbox" class="h-4 w-4 rounded" :disabled="saving" @change="clearAccountProxy" />{{ t('binanceRecharge.clearProxy') }}</label>
          <label class="mt-2 flex items-center gap-2 text-sm"><input id="binance-proxy-preflight" v-model="form.proxy_preflight" type="checkbox" class="h-4 w-4 rounded" :disabled="saving || form.clear_proxy || (!form.proxy_url?.trim() && !editingAccount?.proxy_configured)" />{{ t('binanceRecharge.proxyPreflight') }}</label>
          <p class="mt-1 text-xs text-gray-500">{{ t('binanceRecharge.proxyPreflightHint') }}</p>
        </div>
        <div class="rounded-xl border border-gray-200 p-4 dark:border-dark-600">
          <label for="binance-backup-proxy" class="input-label">{{ t('binanceRecharge.backupProxy') }}</label>
          <input id="binance-backup-proxy" v-model="form.backup_proxy_url" type="password" autocomplete="new-password" class="input mt-1 w-full" maxlength="2048" :disabled="saving || form.clear_backup_proxy" :placeholder="t(editingAccount?.backup_proxy_configured ? 'binanceRecharge.proxyKeepPlaceholder' : 'binanceRecharge.proxyPlaceholder')" />
          <p class="mt-2 text-xs leading-5 text-gray-500">{{ t('binanceRecharge.backupProxyHint') }}</p>
          <label v-if="editingAccount?.backup_proxy_configured" class="mt-2 flex items-center gap-2 text-sm"><input id="binance-clear-backup-proxy" v-model="form.clear_backup_proxy" type="checkbox" class="h-4 w-4 rounded" :disabled="saving" @change="clearBackupProxy" />{{ t('binanceRecharge.clearBackupProxy') }}</label>
          <label class="mt-2 flex items-center gap-2 text-sm"><input id="binance-backup-proxy-preflight" v-model="form.backup_proxy_preflight" type="checkbox" class="h-4 w-4 rounded" :disabled="saving || form.clear_backup_proxy || (!form.backup_proxy_url?.trim() && !editingAccount?.backup_proxy_configured)" />{{ t('binanceRecharge.proxyPreflight') }}</label>
          <p class="mt-1 text-xs text-gray-500">{{ t('binanceRecharge.proxyPreflightHint') }}</p>
        </div>
        <div><label for="binance-ttl" class="input-label">{{ t('binanceRecharge.ttl') }}</label><Select id="binance-ttl" v-model="form.order_ttl_minutes" class="mt-1 w-full" :disabled="saving" :options="[15, 30].map(value => ({ value, label: t('binanceRecharge.minutes', { minutes: value }) }))" /></div>
        <label class="flex items-center gap-2 text-sm"><input v-model="form.enabled" type="checkbox" class="h-4 w-4 rounded" />{{ t('binanceRecharge.enabled') }}</label>
      </form>
      <template #footer><div class="flex justify-end gap-2"><button class="btn btn-secondary" :disabled="saving" @click="closeAccountDialog">{{ t('common.cancel') }}</button><button form="binance-account-form" class="btn btn-primary" type="submit" :disabled="saving">{{ saving ? t('common.processing') : t('common.save') }}</button></div></template>
    </BaseDialog>
    <BaseDialog :show="!!selectedOrder" :title="t('binanceRecharge.details')" width="wide" @close="selectedOrder = null">
      <div v-if="selectedOrder" class="space-y-4"><p class="text-sm text-gray-500">{{ t('binanceRecharge.user') }}: {{ selectedOrder.user_email || `#${selectedOrder.user_id}` }} · #{{ selectedOrder.user_id }}</p><BinanceOrderDetails :order="selectedOrder" :busy="refreshingOrder" @refresh="refreshOrder" /></div>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import Select from '@/components/common/Select.vue'
import { onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminBinanceRechargeAPI } from '@/api/binanceRecharge'
import { useAppStore } from '@/stores/app'
import { useBinanceRechargeStore } from '@/stores/binanceRecharge'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatBinanceMultiplier, isValidBinanceMultiplier } from '@/components/payment/binanceAmounts'
import type { BinanceRechargeAccount, BinanceRechargeAccountInput, BinanceRechargeOrder, BinanceRechargeStatus } from '@/types/binanceRecharge'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import BinanceOrderTable from '@/components/payment/BinanceOrderTable.vue'
import BinanceOrderDetails from '@/components/payment/BinanceOrderDetails.vue'
import BinanceRechargeStats from '@/components/payment/BinanceRechargeStats.vue'
import AdminDailyUsage from '@/components/admin/usage/AdminDailyUsage.vue'
import BinanceLiveBills from '@/components/payment/BinanceLiveBills.vue'

const { t } = useI18n()
const appStore = useAppStore()
const rechargeStore = useBinanceRechargeStore()
const tabs = ['accounts', 'orders', 'totals', 'dailyUsage', 'liveBills'] as const
const activeTab = ref<typeof tabs[number]>('accounts')
const accounts = ref<BinanceRechargeAccount[]>([])
const accountsLoading = ref(false)
const checkingId = ref<number | null>(null)
const showAccountDialog = ref(false)
const editingAccount = ref<BinanceRechargeAccount | null>(null)
const saving = ref(false)
const form = reactive<BinanceRechargeAccountInput>({ name: '', recipient_uid: '', api_key: '', api_secret: '', proxy_url: '', clear_proxy: false, proxy_preflight: false, backup_proxy_url: '', clear_backup_proxy: false, backup_proxy_preflight: false, enabled: false, order_ttl_minutes: 15, credit_multiplier: '1' })
const orders = ref<BinanceRechargeOrder[]>([])
const ordersLoading = ref(false)
const selectedOrder = ref<BinanceRechargeOrder | null>(null)
const refreshingOrder = ref(false)
const keyword = ref('')
const accountFilter = ref('')
const status = ref<BinanceRechargeStatus | ''>('')
const statuses: BinanceRechargeStatus[] = ['PENDING', 'COMPLETED', 'EXPIRED', 'REVIEW']
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
let listSequence = 0
let accountsSequence = 0
let disposed = false
const waitClock = ref(Date.now())
const waitDeadlines = ref<Record<number, number>>({})
let waitTimer: number | undefined
let statusRefreshTicks = 0
let statusRefreshing = false
const date = (value: string) => new Date(value).toLocaleString()
function reportError(error: unknown) { appStore.showError(extractApiErrorMessage(error, t('common.error'))) }

function remainingWait(account: BinanceRechargeAccount) {
  if (!account.check_state || account.check_state === 'ready') return 0
  return Math.max(0, Math.ceil(((waitDeadlines.value[account.id] ?? waitClock.value) - waitClock.value) / 1000))
}
function checkStateLabel(account: BinanceRechargeAccount) {
  if (account.check_state && account.check_state !== 'ready' && remainingWait(account) === 0) {
    return t('binanceRecharge.checkWaitElapsed')
  }
  return t(`binanceRecharge.checkStates.${account.check_state || 'ready'}`)
}
function routeStateLabel(account: BinanceRechargeAccount) {
  const query = account.query_status
  if (account.check_state === 'running' && query?.state === 'waiting' && query.retry_at) {
    return t('binanceRecharge.routeRetryAt', { time: date(query.retry_at) })
  }
  if (account.check_state !== 'running' && (query?.state === 'running' || query?.state === 'waiting')) {
    return t('binanceRecharge.routeEnded')
  }
  return t(`binanceRecharge.routeStates.${query?.state || 'running'}`)
}
function syncWaitTimer() {
  if (waitTimer !== undefined) window.clearInterval(waitTimer)
  waitTimer = undefined
  waitClock.value = Date.now()
  if (!disposed && activeTab.value === 'accounts' && !document.hidden) {
    // Only update the display; a countdown must never trigger a payment lookup.
    waitTimer = window.setInterval(() => {
      waitClock.value = Date.now()
      if (++statusRefreshTicks % 3 === 0 && !statusRefreshing && !accountsLoading.value && (checkingId.value !== null || accounts.value.some(account => account.check_state === 'running'))) {
        statusRefreshing = true
        void loadAccounts(true).finally(() => { statusRefreshing = false })
      }
    }, 1000)
  }
}
async function loadAccounts(background = false) {
  if (disposed) return
  const sequence = ++accountsSequence
  if (!background) accountsLoading.value = true
  try {
    const { data } = await adminBinanceRechargeAPI.getAccounts()
    if (!disposed && sequence === accountsSequence) {
      accounts.value = data || []
      waitClock.value = Date.now()
      waitDeadlines.value = Object.fromEntries(accounts.value.map(account => {
        // Use the server's relative wait to avoid depending on the browser clock.
        const seconds = Number(account.retry_after_seconds)
        const availableAt = account.check_available_at ? Date.parse(account.check_available_at) : NaN
        const deadline = Number.isFinite(seconds) && seconds >= 0
          ? waitClock.value + seconds * 1000
          : Number.isFinite(availableAt) ? availableAt : waitClock.value
        return [account.id, deadline]
      }))
    }
  } catch (error) { if (!background && !disposed && sequence === accountsSequence) reportError(error) }
  finally { if (sequence === accountsSequence) accountsLoading.value = false }
}

function editAccount(account?: BinanceRechargeAccount) {
  editingAccount.value = account || null
  // Build the form explicitly: never copy unknown response fields or stored secrets.
  Object.assign(form, { name: account?.name || '', recipient_uid: account?.recipient_uid || '', api_key: '', api_secret: '', proxy_url: '', clear_proxy: false, proxy_preflight: account?.proxy_preflight ?? false, backup_proxy_url: '', clear_backup_proxy: false, backup_proxy_preflight: account?.backup_proxy_preflight ?? false, enabled: account?.enabled ?? false, order_ttl_minutes: account?.order_ttl_minutes || 15, credit_multiplier: account ? formatBinanceMultiplier(account.credit_multiplier) : '1' })
  showAccountDialog.value = true
}
function closeAccountDialog() {
  if (saving.value) return
  showAccountDialog.value = false
  form.api_key = ''
  form.api_secret = ''
  form.proxy_url = ''
  form.clear_proxy = false
  form.proxy_preflight = false
  form.backup_proxy_url = ''
  form.clear_backup_proxy = false
  form.backup_proxy_preflight = false
  editingAccount.value = null
}
function clearAccountProxy() {
  if (form.clear_proxy) {
    form.proxy_url = ''
    form.proxy_preflight = false
  } else {
    form.proxy_preflight = editingAccount.value?.proxy_preflight ?? false
  }
}
function clearBackupProxy() {
  if (form.clear_backup_proxy) { form.backup_proxy_url = ''; form.backup_proxy_preflight = false }
  else form.backup_proxy_preflight = editingAccount.value?.backup_proxy_preflight ?? false
}
async function saveAccount() {
  if (saving.value) return
  if (!form.name.trim() || !/^\d+$/.test(form.recipient_uid.trim()) || (!editingAccount.value && (!form.api_key?.trim() || !form.api_secret?.trim()))) {
    appStore.showError(t('binanceRecharge.requiredFields')); return
  }
  const multiplier = form.credit_multiplier?.trim() || ''
  if (!isValidBinanceMultiplier(multiplier)) {
    appStore.showError(t('binanceRecharge.invalidMultiplier')); return
  }
  const hasBackup = !form.clear_backup_proxy && (Boolean(form.backup_proxy_url?.trim()) || editingAccount.value?.backup_proxy_configured)
  const hasPrimary = !form.clear_proxy && (Boolean(form.proxy_url?.trim()) || editingAccount.value?.proxy_configured)
  if (hasBackup && !hasPrimary) { appStore.showError(t('binanceRecharge.backupRequiresPrimary')); return }
  saving.value = true
  try {
    const data: BinanceRechargeAccountInput = { name: form.name.trim(), recipient_uid: form.recipient_uid.trim(), enabled: form.enabled, order_ttl_minutes: form.order_ttl_minutes, credit_multiplier: multiplier }
    if (form.api_key?.trim()) data.api_key = form.api_key.trim()
    if (form.api_secret?.trim()) data.api_secret = form.api_secret.trim()
    if (form.clear_proxy) data.clear_proxy = true
    else {
      if (form.proxy_url?.trim()) data.proxy_url = form.proxy_url.trim()
      if (form.proxy_url?.trim() || Boolean(form.proxy_preflight) !== Boolean(editingAccount.value?.proxy_preflight)) {
        data.proxy_preflight = Boolean(form.proxy_preflight)
      }
    }
    if (form.clear_backup_proxy) data.clear_backup_proxy = true
    else {
      if (form.backup_proxy_url?.trim()) data.backup_proxy_url = form.backup_proxy_url.trim()
      if (form.backup_proxy_url?.trim() || Boolean(form.backup_proxy_preflight) !== Boolean(editingAccount.value?.backup_proxy_preflight)) {
        data.backup_proxy_preflight = Boolean(form.backup_proxy_preflight)
      }
    }
    if (editingAccount.value) await adminBinanceRechargeAPI.updateAccount(editingAccount.value.id, data)
    else await adminBinanceRechargeAPI.createAccount(data)
    form.api_key = ''
    form.api_secret = ''
    form.proxy_url = ''
    form.backup_proxy_url = ''
    showAccountDialog.value = false
    appStore.showSuccess(t('binanceRecharge.saveSuccess'))
    await loadAccounts()
    await rechargeStore.fetchConfig(true)
  } catch (error) { reportError(error) }
  finally { saving.value = false }
}
async function checkAccount(account: BinanceRechargeAccount) {
  if (checkingId.value !== null || remainingWait(account) > 0) return
  checkingId.value = account.id
  try {
    const { data } = await adminBinanceRechargeAPI.checkAccount(account.id)
    appStore.showSuccess(t('binanceRecharge.checkResult', data))
  } catch (error) { reportError(error) }
  finally { checkingId.value = null; await loadAccounts() }
}
async function loadOrders() {
  const sequence = ++listSequence
  ordersLoading.value = true
  try {
    const { data } = await adminBinanceRechargeAPI.getOrders({ page: page.value, page_size: pageSize.value, status: status.value, account_id: accountFilter.value ? Number(accountFilter.value) : undefined, keyword: keyword.value.trim() || undefined })
    if (!disposed && sequence === listSequence) { orders.value = data.items || []; total.value = data.total }
  } catch (error) { reportError(error) }
  finally { if (sequence === listSequence) ordersLoading.value = false }
}
async function refreshOrder() {
  if (!selectedOrder.value || refreshingOrder.value) return
  const id = selectedOrder.value.id
  refreshingOrder.value = true
  try {
    const { data } = await adminBinanceRechargeAPI.refreshOrder(id)
    if (!disposed && selectedOrder.value?.id === id) selectedOrder.value = data
    await loadOrders()
  } catch (error) { reportError(error) }
  finally { refreshingOrder.value = false }
}
function changePage(value: number) { page.value = value; void loadOrders() }
function changePageSize(value: number) { pageSize.value = value; page.value = 1; void loadOrders() }
watch(activeTab, value => { syncWaitTimer(); if (value === 'orders') void loadOrders() })
onMounted(() => {
  document.addEventListener('visibilitychange', syncWaitTimer)
  syncWaitTimer()
  void loadAccounts()
})
onUnmounted(() => {
  disposed = true
  document.removeEventListener('visibilitychange', syncWaitTimer)
  syncWaitTimer()
  form.api_key = ''; form.api_secret = ''; form.proxy_url = ''
})
</script>
