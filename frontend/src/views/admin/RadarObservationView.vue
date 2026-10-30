<template>
  <AppLayout>
    <div class="mx-auto max-w-[1600px] space-y-6">
      <header class="flex flex-wrap items-start justify-between gap-4">
        <div class="flex items-center gap-4">
          <div class="flex h-12 w-12 shrink-0 items-center justify-center rounded-2xl bg-primary-100 text-primary-600 dark:bg-primary-950/50 dark:text-primary-300" aria-hidden="true"><svg viewBox="0 0 40 40" class="h-8 w-8" fill="none"><circle cx="20" cy="20" r="15" stroke="currentColor" stroke-width="1.5"/><circle cx="20" cy="20" r="8" stroke="currentColor" stroke-width="1.2" opacity=".45"/><path d="M20 5v15l11 10M5 20h30" stroke="currentColor" stroke-width="1.2" opacity=".45"/><path d="M20 20 30 9" stroke="currentColor" stroke-width="2" stroke-linecap="round"/><circle cx="13" cy="25" r="2" fill="currentColor"/></svg></div>
          <div><h1 class="text-2xl font-semibold tracking-tight text-gray-900 dark:text-white">{{ t('radar.title') }}</h1><p class="mt-1 max-w-3xl text-sm leading-6 text-gray-500 dark:text-dark-400">{{ t('radar.description') }}</p></div>
        </div>
        <button v-if="tab === 'accounts'" type="button" class="btn btn-primary" @click="showSettings = true">{{ t('accountCandy.settings') }}</button>
      </header>
      <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 pb-4 dark:border-dark-700">
        <div class="inline-flex rounded-2xl bg-primary-100/60 p-1 dark:bg-dark-800" role="tablist" :aria-label="t('radar.title')">
          <button v-for="item in tabs" :id="'radar-tab-' + item" :key="item" type="button" role="tab" :aria-selected="tab === item" :aria-controls="'radar-panel-' + item" :tabindex="tab === item ? 0 : -1" class="rounded-xl px-6 py-2.5 text-sm font-semibold focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500" :class="tab === item ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-300' : 'text-gray-500 dark:text-dark-400'" @click="tab = item" @keydown.left.prevent="switchTab" @keydown.right.prevent="switchTab">{{ t('radar.' + item) }}</button>
        </div>
        <RouterLink v-if="tab === 'groups'" to="/intelligence-test" class="text-sm font-medium text-primary-600 hover:underline dark:text-primary-400">{{ t('radar.viewResults') }} →</RouterLink>
        <p v-else class="flex items-center gap-2 text-xs text-gray-500 dark:text-dark-400"><span class="h-1.5 w-1.5 rounded-full bg-primary-400" />{{ t('radar.refreshHint') }}</p>
      </div>
      <section v-if="tab === 'groups'" id="radar-panel-groups" role="tabpanel" aria-labelledby="radar-tab-groups" class="space-y-4">
        <p v-if="groupError" class="text-sm text-red-600" role="alert">{{ groupError }} <button class="underline" @click="loadGroups">{{ t('accountCandy.retry') }}</button></p>
        <IntelligenceTestSettings :groups="testGroups" :groups-loaded="groupsLoaded" @saved="loadGroups" @keys-synced="loadGroups" />
      </section>
      <section v-else id="radar-panel-accounts" role="tabpanel" aria-labelledby="radar-tab-accounts" class="space-y-5">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div class="flex flex-wrap items-center gap-3">
            <div class="w-36"><Select v-model="filter" :options="filterOptions" :aria-label="t('radar.filter')" /></div>
            <form class="flex gap-2" @submit.prevent="applySearch"><input v-model="searchDraft" class="input w-52" :placeholder="t('radar.search')" :aria-label="t('radar.search')" maxlength="200" /><button class="btn btn-secondary" type="submit">{{ t('common.search') }}</button></form>
          </div>
          <span class="text-xs tabular-nums text-gray-500">{{ t('radar.total', { count: total }) }}</span>
        </div>
        <div v-if="error" class="flex items-center justify-between gap-4 rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-700 dark:border-red-900 dark:bg-red-950/20 dark:text-red-300" role="alert"><span>{{ error }}</span><button class="shrink-0 underline" @click="loadAccounts()">{{ t('accountCandy.retry') }}</button></div>
        <div v-if="loading && !items.length" class="card p-16 text-center text-sm text-gray-500" role="status">{{ t('accountCandy.loading') }}</div>
        <div v-else-if="!items.length" class="card p-16 text-center text-sm text-gray-500">{{ t('radar.empty') }}</div>
        <div v-else class="grid grid-cols-1 items-stretch gap-5 md:grid-cols-2 2xl:grid-cols-3" :aria-busy="loading">
          <article v-for="item in items" :key="item.account.id" class="card relative flex min-w-0 flex-col overflow-hidden">
            <div class="h-1 w-full" :class="item.verdict === 'fail' ? 'bg-red-400' : item.verdict === 'pass' ? 'bg-emerald-400' : 'bg-primary-200 dark:bg-primary-800'" />
            <div class="flex-1 space-y-5 p-5">
              <div class="flex items-start justify-between gap-3">
                <div class="min-w-0"><h2 class="break-words text-base font-semibold text-gray-900 dark:text-white">{{ item.account.name }}</h2><p v-if="email(item.account)" class="mt-1 truncate text-xs text-gray-500" :title="email(item.account)">{{ email(item.account) }}</p></div>
                <span class="shrink-0 rounded-full px-2.5 py-1 text-xs font-semibold" :class="item.verdict === 'fail' ? 'bg-red-50 text-red-700 dark:bg-red-950/40 dark:text-red-300' : item.verdict === 'pass' ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300' : 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-dark-300'">{{ verdictText(item) }}</span>
              </div>
              <PlatformTypeBadge :platform="item.account.platform" :type="item.account.type" :auth-mode="stringValue(item.account.credentials?.auth_mode)" :plan-type="stringValue(item.account.credentials?.plan_type) || item.account.parent_plan_type" :privacy-mode="stringValue(item.account.extra?.privacy_mode) || item.account.parent_privacy_mode" :subscription-expires-at="stringValue(item.account.credentials?.subscription_expires_at) || item.account.parent_subscription_expires_at" />
              <div><h3 class="mb-2 text-xs font-medium text-gray-400">{{ t('radar.usage') }}</h3><AccountUsageCell :account="item.account" :batched-usage="usage[item.account.id]" :batched-usage-error="usageError[item.account.id]" :batched-usage-loading="usageLoading[item.account.id]" :request-batched-usage="queueUsage" /></div>
              <div><h3 class="mb-2 text-xs font-medium text-gray-400">{{ t('radar.currentGroups') }}</h3><AccountGroupsCell :groups="accountGroups(item.account)" /></div>
              <dl class="grid grid-cols-2 gap-x-4 gap-y-3 border-t border-gray-100 pt-4 text-xs dark:border-dark-700">
                <div><dt class="text-gray-400">{{ t('radar.lastTest') }}</dt><dd class="mt-1.5 tabular-nums text-gray-700 dark:text-dark-200">{{ item.run ? formatDateTime(item.run.started_at) : '—' }}</dd></div>
                <div><dt class="text-gray-400">{{ t('radar.nextTest') }}</dt><dd class="mt-1.5 tabular-nums text-gray-700 dark:text-dark-200">{{ item.next_run_at ? formatDateTime(item.next_run_at) : t('radar.reason.' + (item.schedule_reason || 'unknown')) }}</dd></div>
                <div><dt class="text-gray-400">{{ t('radar.degraded') }}</dt><dd class="mt-1.5 font-medium" :class="item.verdict === 'fail' ? 'text-red-600 dark:text-red-400' : 'text-gray-700 dark:text-dark-200'">{{ t(item.verdict === 'fail' ? 'radar.yes' : item.verdict === 'pass' ? 'radar.no' : 'radar.unknown') }}</dd></div>
                <div><dt class="text-gray-400">{{ t('radar.result') }}</dt><dd class="mt-1.5 text-gray-700 dark:text-dark-200">{{ verdictText(item) }}</dd></div>
              </dl>
              <div class="rounded-xl bg-gray-50 p-3 dark:bg-dark-800/70" :title="t('radar.egressHint')">
                <div class="flex items-center justify-between gap-2 text-xs text-gray-400"><span>{{ t('radar.egressIP') }}</span><span>{{ t((item.run?.egress_mode || (item.account.proxy_id ? 'proxy' : 'direct')) === 'proxy' ? 'radar.proxy' : 'radar.direct') }}</span></div>
                <p class="mt-2 break-all font-mono text-sm text-gray-800 dark:text-dark-100">{{ item.run?.egress_ip || t('radar.unknown') }}</p>
                <p v-if="item.run?.egress_checked_at" class="mt-1 text-[11px] text-gray-400">{{ formatDateTime(item.run.egress_checked_at) }}</p>
                <p v-if="item.run?.egress_error" class="mt-1 text-xs text-amber-600 dark:text-amber-400">{{ item.run.egress_error }}</p>
              </div>
              <div v-if="item.run?.model_results?.length" class="space-y-2">
                <h3 class="text-xs font-medium text-gray-400">{{ t('radar.modelResults') }}</h3>
                <div v-for="model in item.run.model_results" :key="model.id" class="flex items-start justify-between gap-3 rounded-lg px-2.5 py-2 text-xs" :class="model.passed === false ? 'bg-red-50 text-red-700 dark:bg-red-950/30 dark:text-red-300' : 'bg-gray-50 text-gray-600 dark:bg-dark-800 dark:text-dark-300'"><span class="min-w-0 break-all font-mono">{{ model.model }}</span><span class="shrink-0 font-medium">{{ model.status === 'completed' ? model.passed ? '21 ✓' : (model.answer ?? t('accountCandy.noAnswer')) + ' ✕' : t('accountCandy.' + model.status) }}</span></div>
              </div>
              <p v-if="item.run?.moved_to_group_id" class="text-xs text-amber-700 dark:text-amber-300">{{ t('accountCandy.moved', { group: groupName(item.run.moved_to_group_id) }) }}</p>
              <p v-if="item.run?.move_error || item.run?.error_message" class="break-words text-xs text-amber-700 dark:text-amber-300">{{ item.run.move_error || item.run.error_message }}</p>
            </div>
            <footer class="flex items-center justify-between gap-3 border-t border-gray-100 bg-gray-50/50 px-5 py-3 dark:border-dark-700 dark:bg-dark-800/30">
              <span class="font-mono text-[11px] text-gray-400">#{{ item.account.id }}</span>
              <div class="flex items-center gap-4"><button type="button" class="text-xs font-medium text-gray-500 hover:underline dark:text-dark-300" @click="selected = item">{{ t('radar.history') }}</button><button type="button" class="text-xs font-semibold text-primary-600 hover:underline disabled:cursor-not-allowed disabled:opacity-40 dark:text-primary-300" :disabled="!supportsAccountCandyTest(item.account) || starting.has(item.account.id) || item.run?.status === 'running'" @click="start(item)">{{ t(item.run?.status === 'running' ? 'accountCandy.running' : starting.has(item.account.id) ? 'accountCandy.starting' : 'accountCandy.run') }}</button></div>
            </footer>
          </article>
        </div>
        <Pagination v-if="total > 0" :page="page" :page-size="pageSize" :total="total" :show-page-size-selector="false" @update:page="page = $event" />
        <p class="text-xs leading-5 text-gray-400">{{ t('radar.egressHint') }}</p>
      </section>
      <AccountCandyTestSettings :show="showSettings" @close="showSettings = false" @saved="settingsSaved" />
      <AccountCandyTestDetails :show="!!selected" :account-id="selected?.account.id" :account-name="selected?.account.name || ''" :result="selected?.run || null" :groups="groups" @close="selected = null" />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useIntervalFn } from '@vueuse/core'
import { useRoute, useRouter } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import Select from '@/components/common/Select.vue'
import Pagination from '@/components/common/Pagination.vue'
import PlatformTypeBadge from '@/components/common/PlatformTypeBadge.vue'
import AccountGroupsCell from '@/components/account/AccountGroupsCell.vue'
import AccountUsageCell from '@/components/account/AccountUsageCell.vue'
import IntelligenceTestSettings from '@/components/intelligenceTest/IntelligenceTestSettings.vue'
import AccountCandyTestSettings from '@/components/admin/account/AccountCandyTestSettings.vue'
import AccountCandyTestDetails from '@/components/admin/account/AccountCandyTestDetails.vue'
import { accountCandyAPI, type RadarObservation } from '@/api/admin/accountCandy'
import { intelligenceTestAPI, type IntelligenceTestGroup } from '@/api/intelligenceTest'
import { apiClient } from '@/api/client'
import { getBatchUsage } from '@/api/admin/accounts'
import { supportsAccountCandyTest } from '@/composables/useAccountCandyTests'
import { formatDateTime } from '@/utils/format'
import { extractApiErrorMessage } from '@/utils/apiError'
import { useAppStore } from '@/stores/app'
import type { Account, AdminGroup, AccountUsageInfo } from '@/types'

const { t } = useI18n()
const app = useAppStore()
const route = useRoute()
const router = useRouter()
const tabs = ['groups', 'accounts'] as const
const tab = ref<typeof tabs[number]>(route.query.tab === 'accounts' ? 'accounts' : 'groups')
const items = ref<RadarObservation[]>([])
const groups = ref<AdminGroup[]>([])
const testGroups = ref<IntelligenceTestGroup[]>([])
const groupsLoaded = ref(false)
const groupError = ref('')
const filter = ref('all')
const searchDraft = ref('')
const search = ref('')
const page = ref(1)
const pageSize = 12
const total = ref(0)
const loading = ref(false)
const error = ref('')
const showSettings = ref(false)
const selected = ref<RadarObservation | null>(null)
const starting = ref(new Set<number>())
const filterOptions = computed(() => ['all', 'pass', 'fail'].map(value => ({ value, label: t('radar.filter' + value) })))
let controller: AbortController | null = null
let disposed = false
const stringValue = (value: unknown): string | undefined => typeof value === 'string' && value ? value : undefined
const email = (account: Account) => stringValue(account.extra?.email_address) || stringValue(account.extra?.email) || stringValue(account.credentials?.email) || account.parent_email || ''
const accountGroups = (account: Account) => groups.value.filter(group => account.group_ids?.includes(group.id))
const groupName = (id: number) => groups.value.find(group => group.id === id)?.name || t('accountCandy.groupId', { id })
const verdictText = (item: RadarObservation) => t(item.run?.status === 'running' ? 'accountCandy.running' : item.verdict === 'pass' ? 'accountCandy.passed' : item.verdict === 'fail' ? 'accountCandy.notPassed' : item.run?.status === 'failed' ? 'accountCandy.failed' : 'radar.unknown')

async function switchTab() { tab.value = tab.value === 'groups' ? 'accounts' : 'groups'; await nextTick(); document.getElementById('radar-tab-' + tab.value)?.focus() }
async function loadGroups() {
  try { const data = await intelligenceTestAPI.groups(); if (!disposed) { testGroups.value = data; groupsLoaded.value = true; groupError.value = '' } }
  catch (cause) { if (!disposed) groupError.value = extractApiErrorMessage(cause, t('accountCandy.loadFailed')) }
}
async function loadAccounts(silent = false) {
  if (disposed || tab.value !== 'accounts') return
  if (silent && loading.value) return
  controller?.abort()
  const request = new AbortController(); controller = request; loading.value = true
  try {
    const data = await accountCandyAPI.accounts({ page: page.value, page_size: pageSize, filter: filter.value, search: search.value }, request.signal)
    if (request.signal.aborted || disposed) return
    total.value = data.total
    if (page.value > Math.max(1, Math.ceil(total.value / pageSize))) { page.value = Math.max(1, Math.ceil(total.value / pageSize)); return }
    items.value = data.items; error.value = ''
  } catch (cause) { if (!request.signal.aborted && !disposed) error.value = extractApiErrorMessage(cause, t('accountCandy.loadFailed')) }
  finally { if (!request.signal.aborted && !disposed) loading.value = false }
}
function applySearch() { search.value = searchDraft.value.trim(); if (page.value !== 1) page.value = 1; else void loadAccounts() }
function settingsSaved() { items.value = []; void loadAccounts() }
async function start(item: RadarObservation) {
  starting.value = new Set([...starting.value, item.account.id])
  try { await accountCandyAPI.start(item.account.id); if (!disposed) { app.showSuccess(t('accountCandy.started')); await loadAccounts() } }
  catch (cause) { if (!disposed) app.showError(extractApiErrorMessage(cause, t('accountCandy.startFailed'))) }
  finally { const ids = new Set(starting.value); ids.delete(item.account.id); starting.value = ids }
}

// Coalesce visible-card requests; retain the account table's passive batch endpoint.
const usage = ref<Record<number, AccountUsageInfo | null>>({})
const usageError = ref<Record<number, string | null>>({})
const usageLoading = ref<Record<number, boolean>>({})
const usageTime = new Map<number, number>()
const pendingUsage = new Set<number>()
let usageTimer: ReturnType<typeof setTimeout> | undefined
let forceUsage = false
function queueUsage(account: Account, options?: { force?: boolean }) {
  if (disposed || usageLoading.value[account.id]) return
  if (!options?.force && Date.now() - (usageTime.get(account.id) ?? 0) < 300000) return
  pendingUsage.add(account.id); forceUsage ||= options?.force === true; usageLoading.value[account.id] = true
  if (usageTimer) return
  usageTimer = setTimeout(() => { usageTimer = undefined; void flushUsage() }, 20)
}
async function flushUsage() {
  const ids = [...pendingUsage]; pendingUsage.clear(); const force = forceUsage; forceUsage = false
  if (!ids.length || disposed) return
  try { const data = await getBatchUsage(ids, force); if (disposed) return; for (const id of ids) { usage.value[id] = data.usage[id] ?? null; usageError.value[id] = data.errors[id] ?? null; usageTime.set(id, Date.now()) } }
  catch { if (!disposed) for (const id of ids) usageError.value[id] = t('accountCandy.loadFailed') }
  finally { if (!disposed) for (const id of ids) usageLoading.value[id] = false }
}

watch(filter, () => { items.value = []; if (page.value !== 1) page.value = 1; else void loadAccounts() })
watch(page, () => { items.value = []; void loadAccounts() })
watch(() => route.query.tab, value => { tab.value = value === 'accounts' ? 'accounts' : 'groups' })
watch(tab, () => { if (route.query.tab !== tab.value) void router.replace({ query: { ...route.query, tab: tab.value } }); if (tab.value === 'accounts') void loadAccounts(); else { controller?.abort(); loading.value = false; void loadGroups() } })
useIntervalFn(() => { if (!document.hidden && !showSettings.value) void loadAccounts(true) }, 5000)
onMounted(async () => {
  void loadAccounts(); void loadGroups()
  try { const response = await apiClient.get<AdminGroup[]>('/admin/groups/all'); if (!disposed) groups.value = response.data }
  catch (cause) { if (!disposed) error.value = extractApiErrorMessage(cause, t('accountCandy.loadFailed')) }
})
onUnmounted(() => { disposed = true; controller?.abort(); if (usageTimer) clearTimeout(usageTimer) })
</script>
