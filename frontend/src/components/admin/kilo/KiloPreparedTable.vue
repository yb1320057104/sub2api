<script setup lang="ts">
import { computed, shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import KiloStatusBadge from './KiloStatusBadge.vue'
import KiloPoolStatus from './KiloPoolStatus.vue'
import { canActivateKiloPrepared, canUnbindKiloPrepared, isReusableKiloPrepared } from './pool'
import KiloModelsButton from './KiloModelsButton.vue'
import KiloAccountIdentity from './KiloAccountIdentity.vue'
import KiloQuotaCell from './KiloQuotaCell.vue'
import type { KiloAccountView } from '@/types'
import type { KiloJob, KiloPrepared } from '@/api/admin/kilo'
import { kiloRoutingOptions } from './routing'

const props = defineProps<{ items: KiloPrepared[]; jobs?: KiloJob[]; selectedIds: number[]; loading: boolean; disabled: boolean; identityRefreshId?: number | null }>()
const emit = defineEmits<{ 'update:selectedIds': [ids: number[]]; batch: []; organization: [item: KiloPrepared]; egress: [item: KiloPrepared]; release: [item: KiloPrepared]; routing: [item: KiloPrepared]; pair: [item: KiloPrepared]; 'manage-source': [id: number]; import: []; unbind: [item: KiloPrepared]; fillSlot: [item: KiloPrepared]; refreshIdentity: [item: KiloPrepared]; delete: [item: KiloPrepared]; changeProxy: [job: KiloJob] }>()
const { t, locale } = useI18n()
const query = shallowRef('')
const status = shallowRef('all')
const page = shallowRef(1)
const pageSize = 25
const filtered = computed(() => {
  const search = query.value.trim().toLowerCase()
  return props.items.filter(item => (status.value === 'all' || status.value === 'idle' && isReusableKiloPrepared(item) || status.value === 'banned' && item.identity_status === 'banned' || status.value === 'retired' && item.identity_status === 'retired' || status.value === item.binding_state || status.value === item.status) && (!search || `${item.name} ${item.email} ${item.id} ${item.account_id ?? ''} ${item.source_account_id ?? ''} ${item.identity_source_account_id ?? ''} ${item.source_name ?? ''} ${item.gateway_name ?? ''} ${item.account_view?.email ?? ''} ${item.account_view?.source?.name ?? ''} ${item.account_view?.source?.email ?? ''} ${item.account_view?.source?.plan_type ?? ''} ${item.account_view?.source?.workspace_id ?? ''}`.toLowerCase().includes(search)))
})
const pageCount = computed(() => Math.max(1, Math.ceil(filtered.value.length / pageSize)))
const currentPage = computed(() => Math.min(page.value, pageCount.value))
const visible = computed(() => filtered.value.slice((currentPage.value - 1) * pageSize, currentPage.value * pageSize))
const selectableVisible = computed(() => visible.value.filter(canActivateKiloPrepared))
const allVisibleSelected = computed(() => selectableVisible.value.length > 0 && selectableVisible.value.every(item => props.selectedIds.includes(item.id)))
const statusOptions = computed(() => [{ value: 'all', label: t('common.all') }, { value: 'idle', label: t('kilo.pool.reusable') }, { value: 'banned', label: t('kilo.pool.identity.banned') }, { value: 'retired', label: t('kilo.pool.identity.retired') }, ...['recovering', 'unbinding', 'blocked'].map(value => ({ value, label: t(`kilo.pool.binding.${value}`) })), ...['ready', 'binding', 'bound', 'disabled'].map(value => ({ value, label: t(`kilo.status.${value}`) }))])
function toggle(id: number) { emit('update:selectedIds', props.selectedIds.includes(id) ? props.selectedIds.filter(value => value !== id) : [...props.selectedIds, id]) }
function toggleVisible() {
  const ids = new Set(props.selectedIds)
  selectableVisible.value.forEach(item => { if (allVisibleSelected.value) ids.delete(item.id); else ids.add(item.id) })
  emit('update:selectedIds', [...ids])
}
const latestJobs = computed(() => {
  const latest = new Map<number, KiloJob>()
  for (const job of props.jobs ?? []) {
    if (job.prepared_id === null) continue
    if (!latest.has(job.prepared_id) || latest.get(job.prepared_id)!.id < job.id) latest.set(job.prepared_id, job)
  }
  return latest
})
function failedJob(item: KiloPrepared): KiloJob | undefined {
  const job = latestJobs.value.get(item.id)
  return job && ['failed', 'cancelled', 'canceled'].includes(job.status) ? job : undefined
}
function formatDate(value: string) { return new Date(value).toLocaleString(locale.value) }
function accountView(item: KiloPrepared): KiloAccountView {
  if (item.account_view) return item.account_view
  // Older snapshots still expose associations; do not label a linked source as an unbound key.
  return {
    prepared_id: item.id, name: item.gateway_name || item.name, email: item.email,
    organization_id: item.organization_id, has_key: item.has_credential,
    ...(item.source_account_id ? { source: {
      id: item.source_account_id, name: item.source_name || '', email: '', plan_type: '',
      workspace_id: '', status: 'unknown', quota_only: false
    } } : {})
  }
}
</script>

<template>
  <section class="space-y-4" :aria-label="t('kilo.prepared.title')">
    <div class="flex flex-col gap-3 sm:flex-row sm:items-end">
      <div class="min-w-0 flex-1"><label for="kilo-prepared-search" class="mb-2 block text-sm font-medium">{{ t('common.search') }}</label><input id="kilo-prepared-search" v-model="query" type="search" class="input w-full" :placeholder="t('kilo.prepared.search')" @input="page = 1" /></div>
      <div class="w-full sm:w-44"><label for="kilo-status-filter" class="mb-2 block text-sm font-medium">{{ t('common.status') }}</label><Select id="kilo-status-filter" v-model="status" :options="statusOptions" :aria-label="t('common.status')" @change="page = 1" /></div>
    </div>
    <div class="flex flex-wrap items-center justify-between gap-3 rounded-lg bg-gray-50 px-3 py-2 dark:bg-dark-900/50">
      <div class="flex flex-wrap items-center gap-3"><label class="flex min-h-9 cursor-pointer items-center gap-2 text-sm"><input type="checkbox" class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500" :checked="allVisibleSelected" :disabled="disabled || !selectableVisible.length" @change="toggleVisible" /><span>{{ t('kilo.selectPage') }}</span></label><span class="text-xs text-gray-500 dark:text-dark-400">{{ t('kilo.prepared.selected', { count: selectedIds.length }) }}</span><button v-if="selectedIds.length" type="button" class="text-xs text-primary-600 underline dark:text-primary-400" :disabled="disabled" @click="emit('update:selectedIds', [])">{{ t('common.clear') }}</button></div>
      <button type="button" class="btn btn-primary btn-sm" :disabled="disabled || !selectedIds.length" @click="emit('batch')">{{ t('kilo.prepared.createBatch') }}</button>
    </div>
    <p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('kilo.prepared.selectionHint') }}</p>
    <div v-if="!visible.length" class="rounded-xl border border-dashed border-gray-200 px-4 py-12 text-center dark:border-dark-700"><p class="text-sm text-gray-500 dark:text-dark-400">{{ loading ? t('common.loading') : items.length ? t('kilo.prepared.noMatches') : t('kilo.prepared.empty') }}</p><button v-if="!items.length && !loading" type="button" class="btn btn-primary mt-4" :disabled="disabled" @click="emit('import')">{{ t('kilo.import.title') }}</button></div>
    <div v-else class="overflow-hidden rounded-lg border border-gray-200 dark:border-dark-700">
      <div class="hidden grid-cols-[2rem_1.5fr_1fr_1fr_1fr] gap-3 bg-gray-50 px-4 py-3 text-xs font-medium text-gray-600 dark:bg-dark-900/50 dark:text-dark-300 xl:grid" aria-hidden="true"><span /><span>{{ t('kilo.prepared.identity') }}</span><span>{{ t('common.status') }}</span><span>{{ t('kilo.identity.requestSettings') }}</span><span>{{ t('common.actions') }}</span></div>
      <ul class="divide-y divide-gray-100 dark:divide-dark-700"><li v-for="item in visible" :key="item.id" class="grid grid-cols-[1.5rem_1fr] items-start gap-3 p-4 xl:grid-cols-[2rem_1.5fr_1fr_1fr_1fr]">
        <input type="checkbox" class="mt-1 h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500" :aria-label="t('kilo.prepared.select', { name: item.name })" :checked="selectedIds.includes(item.id)" :disabled="disabled || !canActivateKiloPrepared(item)" @change="toggle(item.id)" />
        <div class="min-w-0"><KiloAccountIdentity :view="accountView(item)" :disabled="disabled" @manage-source="emit('manage-source', $event)" /><p class="mt-2 text-xs text-gray-500 dark:text-dark-400">{{ item.auth_method === 'device' ? t('kilo.prepared.device') : t('kilo.prepared.apiKey') }}</p><p class="mt-1 text-xs text-gray-400 dark:text-dark-500">{{ formatDate(item.created_at) }}</p></div>
        <div class="col-start-2 space-y-2 xl:col-start-auto"><KiloStatusBadge :status="item.status" /><KiloPoolStatus :item="item" /><p v-if="item.status === 'ready' && !accountView(item).source" class="mt-2 text-xs leading-5 text-amber-700 dark:text-amber-300">{{ t('kilo.prepared.awaitingPair') }}</p><KiloQuotaCell v-if="accountView(item).source" class="mt-3" :gateway-id="item.account_id" :view="accountView(item)" /><p v-if="item.last_error" class="mt-2 break-words text-xs leading-5 text-red-600 dark:text-red-400">{{ item.last_error }}</p></div>
        <div class="col-start-2 min-w-0 space-y-2 text-xs leading-5 text-gray-600 dark:text-dark-300 xl:col-start-auto">
          <p>{{ item.account_id ? (accountView(item).source ? t('kilo.identity.linked') : t('kilo.identity.singleKeyHint')) : t('kilo.prepared.notActivated') }}</p>
          <details class="text-gray-500 dark:text-dark-400"><summary class="cursor-pointer rounded focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500">{{ t('kilo.identity.references') }}</summary><div class="mt-1 space-y-1"><p>{{ item.account_id ? t('kilo.prepared.managedId', { id: item.account_id }) : t('kilo.prepared.notActivated') }}</p><p>{{ item.source_account_id ? t('kilo.prepared.sourceId', { id: item.source_account_id }) : t('kilo.prepared.noSource') }}</p></div></details>
          <p>{{ item.login_proxy_id ? t('kilo.routing.loginIdentity', { name: item.login_proxy_name || '', id: item.login_proxy_id }) : t('kilo.routing.loginMissing') }}</p>
          <p>{{ t('kilo.routing.requestSummary', { count: kiloRoutingOptions(item.routing_options).proxies_per_account, perProxy: kiloRoutingOptions(item.routing_options).proxy_concurrency, total: kiloRoutingOptions(item.routing_options).account_concurrency }) }}</p>
          <div v-if="latestJobs.get(item.id)" class="flex flex-wrap items-center gap-2"><span>{{ t('kilo.prepared.latestJob', { id: latestJobs.get(item.id)!.id }) }}</span><KiloStatusBadge :status="latestJobs.get(item.id)!.status" /></div>
        </div>
        <div class="col-start-2 flex flex-wrap gap-2 xl:col-start-auto"><button type="button" class="btn btn-secondary btn-sm" :disabled="disabled || identityRefreshId != null || !item.has_credential" :aria-busy="identityRefreshId === item.id" :title="t('kilo.pool.refreshIdentityHint')" data-testid="kilo-refresh-identity" @click="emit('refreshIdentity', item)">{{ t(identityRefreshId === item.id ? 'kilo.pool.refreshingIdentity' : 'kilo.pool.refreshIdentity') }}</button><button v-if="isReusableKiloPrepared(item)" type="button" class="btn btn-primary btn-sm" :disabled="disabled" data-testid="kilo-fill-slot" @click="emit('fillSlot', item)">{{ t('kilo.pool.fillSlot') }}</button><button v-if="canUnbindKiloPrepared(item)" type="button" class="btn btn-secondary btn-sm" :disabled="disabled" data-testid="kilo-unbind" @click="emit('unbind', item)">{{ t('kilo.pool.unbind') }}</button><button v-if="canActivateKiloPrepared(item) && !accountView(item).source" type="button" class="btn btn-primary btn-sm" :disabled="disabled" @click="emit('pair', item)">{{ t('kilo.prepared.pair') }}</button><button type="button" class="btn btn-secondary btn-sm" :disabled="disabled || item.status === 'binding'" @click="emit('routing', item)">{{ t('kilo.routing.action') }}</button><button v-if="failedJob(item)?.job_kind === 'onboard_source'" type="button" class="btn btn-secondary btn-sm" :disabled="disabled || item.status === 'binding'" data-testid="kilo-prepared-change-proxy" @click="emit('changeProxy', failedJob(item)!)">{{ t('kilo.jobs.changeProxy') }}</button><button v-if="failedJob(item)" type="button" class="btn btn-secondary btn-sm text-red-600 dark:text-red-400" :disabled="disabled || item.status === 'binding'" data-testid="kilo-prepared-delete" @click="emit('delete', item)">{{ t('kilo.cleanup.deletePrepared') }}</button><button type="button" class="btn btn-secondary btn-sm" :disabled="disabled || !item.has_credential || item.status !== 'ready'" @click="emit('organization', item)">{{ t('kilo.organization.action') }}</button><button v-if="item.account_id" type="button" class="btn btn-secondary btn-sm" :disabled="disabled" @click="emit('egress', item)">{{ t('kilo.egress.action') }}</button><KiloModelsButton v-if="item.account_id && accountView(item).source && ['bound', 'disabled'].includes(item.status)" :prepared-id="item.id" :disabled="disabled" /><button v-if="item.account_id && ['bound', 'disabled'].includes(item.status)" type="button" class="btn btn-secondary btn-sm text-red-600 dark:text-red-400" :disabled="disabled" @click="emit('release', item)">{{ t('kilo.release.action') }}</button></div>
      </li></ul>
    </div>
    <div v-if="filtered.length" class="flex flex-wrap items-center justify-between gap-3 text-xs text-gray-500 dark:text-dark-400"><span>{{ t('kilo.pagination', { page: currentPage, pages: pageCount, count: filtered.length }) }}</span><div class="flex gap-2"><button type="button" class="btn btn-secondary btn-sm" :disabled="currentPage <= 1" @click="page = currentPage - 1">{{ t('common.back') }}</button><button type="button" class="btn btn-secondary btn-sm" :disabled="currentPage >= pageCount" @click="page = currentPage + 1">{{ t('common.next') }}</button></div></div>
  </section>
</template>
