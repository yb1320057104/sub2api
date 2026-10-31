<template>
  <section class="space-y-4">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <form class="flex flex-wrap items-center gap-2" @submit.prevent="search">
        <input v-model="draft" class="input w-64 max-w-full" :placeholder="t('authorization.searchEmail')" :aria-label="t('authorization.searchEmail')" maxlength="254" />
        <Select v-model="status" class="w-44" :options="filters" :aria-label="t('authorization.filter')" />
        <button class="btn btn-secondary" type="submit">{{ t('authorization.search') }}</button>
      </form>
      <button class="btn btn-secondary" type="button" :disabled="loading" @click="load()">{{ t('authorization.refresh') }}</button>
    </div>
    <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
    <div class="overflow-x-auto rounded-2xl border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800" :aria-busy="loading">
      <table class="w-full min-w-[760px] text-left text-sm">
        <thead class="border-b border-gray-200 bg-gray-50 text-xs text-gray-500 dark:border-dark-700 dark:bg-dark-900/50 dark:text-dark-400"><tr>
          <th class="px-4 py-3">{{ t(kind === 'proxies' ? 'authorization.proxyEndpoint' : 'authorization.account') }}</th>
          <th class="px-4 py-3">{{ t('authorization.status') }}</th>
          <th class="px-4 py-3">{{ t(kind === 'history' ? 'authorization.credentials' : 'authorization.details') }}</th>
          <th class="px-4 py-3">{{ t('authorization.time') }}</th>
          <th class="px-4 py-3 text-right">{{ t('authorization.actions') }}</th>
        </tr></thead>
        <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
          <tr v-for="row in rows" :key="row.id" class="align-top">
            <td class="max-w-72 px-4 py-4"><p class="truncate font-medium text-gray-900 dark:text-white" :title="row.name || row.endpoint || row.email">{{ row.name || row.endpoint || row.email }}</p><p v-if="row.name" class="mt-1 break-all text-xs text-gray-500 dark:text-dark-400">{{ row.email || '—' }}</p><div v-if="row.groups?.length" class="mt-2 flex max-w-64 flex-wrap gap-1"><span v-for="group in row.groups" :key="group" class="rounded-md bg-primary-50 px-1.5 py-0.5 text-[11px] text-primary-700 dark:bg-primary-950/50 dark:text-primary-300">{{ group }}</span></div><p class="mt-1 text-[11px] text-gray-400">#{{ row.id }}<span v-if="row.source"> · {{ t('authorization.source.' + row.source) }}</span><span v-if="row.source === 'reauthorization'" class="ml-1 rounded bg-amber-50 px-1.5 py-0.5 text-[11px] font-medium text-amber-700 dark:bg-amber-950/50 dark:text-amber-300">{{ date(row.created_at) }}</span></p></td>
            <td class="px-4 py-4"><span class="inline-block max-w-56 rounded-lg px-2 py-1 text-xs font-medium" :class="tone(row.status)">{{ state(row.status) }}</span><p v-if="kind === 'jobs' && row.stage" class="mt-2 text-xs text-gray-500 dark:text-dark-400">{{ stageLabel(row.stage) }}</p><p v-if="row.http_status" class="mt-2 font-mono text-xs text-gray-500">HTTP {{ row.http_status }}</p></td>
            <td class="max-w-80 px-4 py-4 text-xs leading-5 text-gray-600 dark:text-dark-300">
              <template v-if="kind === 'jobs' && row.authorization_completed"><p class="mb-1 font-medium text-emerald-700 dark:text-emerald-300">{{ t('authorization.credentialsAuthorized') }}</p><p v-if="typeof row.intelligence_passed === 'boolean'">{{ t(row.intelligence_passed ? 'authorization.gatePassed' : 'authorization.gateFailed') }}<span v-if="row.intelligence_answer != null"> · {{ t('authorization.gateAnswer', { answer: row.intelligence_answer }) }}</span></p><p class="mb-2">{{ t('authorization.finalGroups') }}：{{ row.groups?.length ? row.groups.join('、') : t('authorization.ungrouped') }}</p></template>
              <div v-if="kind === 'jobs' && row.intelligence_results?.length" class="mb-3 space-y-2">
                <p class="font-medium text-gray-700 dark:text-dark-200">{{ t('authorization.modelResults') }}</p>
                <div v-for="result in row.intelligence_results" :key="result.model" class="rounded-lg border border-gray-200 p-2 dark:border-dark-600">
                  <p class="break-words font-medium">{{ result.model }} / {{ result.effort || t('accountCandy.defaultEffort') }}</p>
                  <p v-if="typeof result.passed === 'boolean'" :class="result.passed ? 'text-emerald-700 dark:text-emerald-300' : 'text-red-600 dark:text-red-400'">{{ t(result.passed ? 'authorization.gatePassed' : 'authorization.gateFailed') }}<span v-if="result.answer != null"> · {{ t('authorization.gateAnswer', { answer: result.answer }) }}</span></p>
                  <p v-else>{{ t('authorization.modelStates.' + (result.status === 'running' && row.status !== 'running' ? (row.status === 'cancelled' ? 'cancelled' : 'interrupted') : result.status)) }}</p>
                  <p v-if="result.error_message" class="break-words text-amber-700 dark:text-amber-300">{{ result.error_message }}</p>
                  <p v-if="result.status !== 'running'" class="text-gray-400">{{ t('authorization.modelDuration', { seconds: (result.duration_ms / 1000).toFixed(1) }) }}</p>
                </div>
              </div>
              <template v-if="kind === 'history'"><p>{{ t('authorization.password') }} · ••••••••</p><p>2FA · {{ row.has_twofa ? '••••••••' : t('authorization.incomplete') }}</p><p v-if="row.status === 'banned'" class="mt-2 text-red-600 dark:text-red-400">{{ row.message || t('authorization.bannedHint') }}</p></template>
              <template v-else-if="kind === 'accounts' && row.status === 'missing_credentials'"><p>{{ t('authorization.missingHint') }}</p></template>
              <template v-else-if="kind === 'accounts' && row.status === 'credentials_completed'"><p>{{ t('authorization.supplemented') }}</p></template>
              <template v-else><p class="whitespace-pre-wrap break-words">{{ row.job_message || row.message || '—' }}</p><p v-if="kind === 'accounts' && row.status !== 'unsupported'" class="mt-1 text-gray-400">{{ t(row.has_history ? 'authorization.historyReady' : 'authorization.historyMissing') }}</p></template>
            </td>
            <td class="whitespace-nowrap px-4 py-4 text-xs tabular-nums text-gray-500 dark:text-dark-400">{{ date(row.checked_at || row.last_checked_at || row.created_at) }}<p v-if="row.authorized_at" class="mt-1">{{ t('authorization.authorizedAt') }}<br />{{ date(row.authorized_at) }}</p></td>
            <td class="px-4 py-4 text-right"><div class="flex justify-end gap-3 whitespace-nowrap text-xs font-semibold">
              <template v-if="kind === 'proxies'"><button type="button" class="text-primary-600 disabled:opacity-40 dark:text-primary-300" :disabled="busy.has(row.id)" @click="action(row, 'check')">{{ t(busy.has(row.id) ? 'authorization.checking' : 'authorization.checkProxy') }}</button><button type="button" class="text-gray-500 disabled:opacity-40" :disabled="busy.has(row.id)" @click="action(row, 'disable')">{{ t('authorization.disable') }}</button></template>
              <button v-if="kind === 'jobs' && ['queued', 'running'].includes(row.status)" type="button" class="text-red-600 disabled:opacity-40 dark:text-red-400" :disabled="busy.has(row.id)" @click="action(row, 'cancel')">{{ t('authorization.cancel') }}</button>
              <button v-if="kind === 'history'" type="button" class="text-primary-600 disabled:opacity-40 dark:text-primary-300" :disabled="busy.has(row.id)" @click="reveal(row)">{{ t('authorization.viewCredentials') }}</button>
              <button v-if="kind === 'history'" type="button" class="text-primary-600 disabled:opacity-40 dark:text-primary-300" :disabled="busy.has(row.id)" @click="editCredential(row)">{{ t('authorization.updateCredentials') }}</button>
              <button v-if="kind === 'history'" type="button" class="text-red-600 disabled:opacity-40 dark:text-red-400" :disabled="busy.has(row.id)" @click="openDelete(row)">{{ t('authorization.deleteCredentials') }}</button>
              <button v-if="kind === 'accounts' && row.status !== 'unsupported' && !row.has_history" type="button" class="rounded-lg bg-primary-50 px-2 py-1.5 text-primary-700 dark:bg-primary-950/50 dark:text-primary-300" @click="openSupplement(row)">{{ t('authorization.supplement') }}</button>
            </div></td>
          </tr>
          <tr v-if="!rows.length"><td colspan="5" class="px-5 py-12 text-center text-sm text-gray-500">{{ t(loading ? 'common.loading' : 'authorization.empty.' + kind) }}</td></tr>
        </tbody>
      </table>
    </div>
    <div class="flex flex-wrap items-center justify-between gap-3 text-xs text-gray-500 dark:text-dark-400"><span>{{ t('authorization.total', { count: total }) }}</span><nav class="flex items-center gap-3" :aria-label="t('authorization.pagination')"><button type="button" class="btn btn-secondary btn-sm" :disabled="page <= 1" @click="page--">{{ t('pagination.previous') }}</button><span class="tabular-nums">{{ page }} / {{ pages }}</span><button type="button" class="btn btn-secondary btn-sm" :disabled="page >= pages" @click="page++">{{ t('pagination.next') }}</button></nav></div>
    <BaseDialog :show="!!supplementTarget" :title="t(credentialMode === 'edit' ? 'authorization.updateCredentials' : 'authorization.supplement')" @close="closeSupplement">
      <form id="authorization-supplement" class="space-y-4" autocomplete="off" @submit.prevent="saveSupplement">
        <p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t(credentialMode === 'edit' ? 'authorization.editCredentialsHint' : 'authorization.supplementHint') }}</p>
        <label class="block"><span class="input-label">{{ t('authorization.email') }}</span><input v-model="supplement.email" class="input" type="email" required :readonly="!!supplementTarget?.email" maxlength="254" /></label>
        <label class="block"><span class="input-label">{{ t('authorization.password') }}</span><input v-model="supplement.password" class="input" type="password" autocomplete="new-password" required maxlength="1024" /></label>
        <label class="block"><span class="input-label">{{ t('authorization.twofa') }}</span><input v-model="supplement.twofa" class="input font-mono" type="password" autocomplete="off" :required="credentialMode === 'supplement'" maxlength="256" /></label>
        <p v-if="dialogError" role="alert" class="text-sm text-red-600">{{ dialogError }}</p>
      </form>
      <template #footer><button type="button" class="btn btn-secondary" @click="closeSupplement">{{ t('common.cancel') }}</button><button type="submit" form="authorization-supplement" class="btn btn-primary" :disabled="supplementSaving">{{ t(credentialMode === 'edit' ? 'common.save' : 'authorization.saveCredentials') }}</button></template>
    </BaseDialog>
    <BaseDialog :show="!!revealed" :title="t('authorization.viewCredentials')" @close="clearReveal">
      <div v-if="revealed" class="space-y-4"><p class="break-all font-medium">{{ revealed.email }}</p><label class="block"><span class="input-label">{{ t('authorization.password') }}</span><input :value="revealed.password" readonly :type="showSecret ? 'text' : 'password'" class="input" /></label><label class="block"><span class="input-label">{{ t('authorization.twofa') }}</span><input :value="revealed.twofa" readonly :type="showSecret ? 'text' : 'password'" class="input font-mono" /></label><button type="button" class="text-sm text-primary-600" @click="showSecret = !showSecret">{{ t(showSecret ? 'authorization.hide' : 'authorization.show') }}</button></div>
    </BaseDialog>
    <BaseDialog :show="!!deleteTarget" :title="t('authorization.deleteCredentials')" @close="closeDelete">
      <p class="text-sm leading-6 text-gray-600 dark:text-dark-300">{{ t('authorization.deleteCredentialsHint', { email: deleteTarget?.email || '' }) }}</p>
      <p v-if="deleteError" role="alert" class="mt-3 text-sm text-red-600">{{ deleteError }}</p>
      <template #footer><button type="button" class="btn btn-secondary" :disabled="deleting" @click="closeDelete">{{ t('common.cancel') }}</button><button type="button" class="btn btn-danger" :disabled="deleting" @click="deleteCredential">{{ t('authorization.deleteCredentials') }}</button></template>
    </BaseDialog>
    <TotpStepUpDialog :controller="stepUp" />
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useIntervalFn } from '@vueuse/core'
import Select from '@/components/common/Select.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { useStepUp, isStepUpCancelled } from '@/composables/useStepUp'
import { accountAuthorizationAPI, type AuthorizationList, type AuthorizationRecord, type AuthorizationSecret } from '@/api/admin/accountAuthorization'
import { formatDateTime } from '@/utils/format'
import { extractApiErrorMessage } from '@/utils/apiError'
import { useAppStore } from '@/stores/app'
const props = withDefaults(defineProps<{ kind: AuthorizationList; refreshKey?: number }>(), { refreshKey: 0 })
const { t, te } = useI18n()
const app = useAppStore()
const stepUp = useStepUp()
const rows = ref<AuthorizationRecord[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const pages = computed(() => Math.max(1, Math.ceil(total.value / pageSize)))
const draft = ref('')
const query = ref('')
const status = ref('all')
const loading = ref(false)
const error = ref('')
const busy = ref(new Set<number>())
const supplementTarget = ref<AuthorizationRecord | null>(null)
const supplement = ref<AuthorizationSecret>({ email: '', password: '', twofa: '' })
const supplementSaving = ref(false)
const credentialMode = ref<'supplement' | 'edit'>('supplement')
const deleteTarget = ref<AuthorizationRecord | null>(null)
const deleteError = ref('')
const deleting = ref(false)
const dialogError = ref('')
const revealed = ref<AuthorizationSecret | null>(null)
const showSecret = ref(false)
let disposed = false
let secretVersion = 0
let request: AbortController | null = null
const filterNames = computed(() => props.kind === 'accounts' ? ['all', 'authorized', 'expired', 'banned', 'missing_credentials', 'credentials_completed', 'queued', 'authorizing', 'error', 'unknown', 'unsupported'] : props.kind === 'history' ? ['all', 'saved', 'banned'] : props.kind === 'proxies' ? ['all', 'unknown', 'passed', 'failed'] : ['all', 'queued', 'running', 'completed', 'banned', 'failed', 'needs_input', 'cancelled'])
const state = (value: string) => props.kind === 'proxies' && value === 'unknown' ? t('authorization.proxyUnchecked') : te('authorization.states.' + value) ? t('authorization.states.' + value) : value
const filters = computed(() => filterNames.value.map(value => ({ value, label: state(value) })))
const date = (value?: string) => value ? formatDateTime(value) : '—'
const stageLabel = (value: string) => te('authorization.stages.' + value) ? t('authorization.stages.' + value) : t('authorization.currentStage', { stage: value })
function tone(value: string) { return ['authorized', 'passed', 'completed', 'saved'].includes(value) ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300' : ['expired', 'banned', 'failed', 'missing_credentials'].includes(value) ? 'bg-red-50 text-red-700 dark:bg-red-950/30 dark:text-red-300' : 'bg-primary-50 text-primary-700 dark:bg-primary-950/40 dark:text-primary-300' }
async function load(silent = false) {
  if (disposed || (silent && loading.value)) return
  request?.abort(); const current = new AbortController(); request = current; loading.value = true
  try { const data = await accountAuthorizationAPI.list(props.kind, { page: page.value, page_size: pageSize, search: query.value, status: status.value }, current.signal); if (disposed || current.signal.aborted) return; total.value = data.total; if (page.value > pages.value) { page.value = pages.value; return }; rows.value = data.items; error.value = '' }
  catch (e) { if (!disposed && !current.signal.aborted) error.value = extractApiErrorMessage(e, t('authorization.loadFailed')) }
  finally { if (!disposed && !current.signal.aborted) loading.value = false }
}
function search() { query.value = draft.value.trim(); if (page.value !== 1) page.value = 1; else void load() }
function mark(id: number, active: boolean) { const next = new Set(busy.value); if (active) next.add(id); else next.delete(id); busy.value = next }
async function action(row: AuthorizationRecord, operation: 'check' | 'disable' | 'cancel') {
  mark(row.id, true)
  try { if (operation === 'check') await accountAuthorizationAPI.checkProxy(row.id); else if (operation === 'disable') await accountAuthorizationAPI.disableProxy(row.id); else await accountAuthorizationAPI.cancel(row.id); if (!disposed) await load() }
  catch (e) { if (!disposed) app.showError(extractApiErrorMessage(e, t('authorization.operationFailed'))) }
  finally { mark(row.id, false) }
}
async function reveal(row: AuthorizationRecord) {
  const version = ++secretVersion
  mark(row.id, true)
  try { const value = await stepUp.run(() => accountAuthorizationAPI.reveal(row.id)); if (!disposed && !document.hidden && version === secretVersion && props.kind === 'history') revealed.value = value }
  catch (e) { if (!disposed && !isStepUpCancelled(e)) app.showError(extractApiErrorMessage(e, t('authorization.operationFailed'))) }
  finally { mark(row.id, false) }
}
function clearReveal() { secretVersion++; revealed.value = null; showSecret.value = false }
function openSupplement(row: AuthorizationRecord) { clearReveal(); credentialMode.value = 'supplement'; supplementTarget.value = row; supplement.value = { email: row.email, password: '', twofa: '' }; dialogError.value = '' }
function closeSupplement() { if (supplementSaving.value) return; secretVersion++; supplementTarget.value = null; supplement.value = { email: '', password: '', twofa: '' }; dialogError.value = '' }
async function editCredential(row: AuthorizationRecord) {
  clearReveal()
  const version = ++secretVersion
  mark(row.id, true)
  try {
    const value = await stepUp.run(() => accountAuthorizationAPI.reveal(row.id))
    if (!disposed && !document.hidden && version === secretVersion && props.kind === 'history') {
      credentialMode.value = 'edit'; supplementTarget.value = row; supplement.value = value; dialogError.value = ''
    }
  } catch (e) { if (!disposed && !isStepUpCancelled(e)) app.showError(extractApiErrorMessage(e, t('authorization.operationFailed'))) }
  finally { mark(row.id, false) }
}
async function saveSupplement() {
  const target = supplementTarget.value
  if (!target || supplementSaving.value) return
  supplementSaving.value = true
  const value = { ...supplement.value }
  const editing = credentialMode.value === 'edit'
  try {
    if (editing) await stepUp.run(() => accountAuthorizationAPI.updateCredential(target.id, { password: value.password, twofa: value.twofa, revision: target.revision || '' }))
    else await accountAuthorizationAPI.supplement(target.id, value)
    if (!disposed) { supplementSaving.value = false; closeSupplement(); app.showSuccess(t(editing ? 'authorization.credentialsUpdated' : 'authorization.supplemented')); await load() }
  } catch (e) { if (!disposed && !isStepUpCancelled(e)) dialogError.value = extractApiErrorMessage(e, t('authorization.saveFailed')) }
  finally { value.password = ''; value.twofa = ''; supplementSaving.value = false }
}
function openDelete(row: AuthorizationRecord) { clearReveal(); deleteTarget.value = row; deleteError.value = '' }
function closeDelete() { if (!deleting.value) { deleteTarget.value = null; deleteError.value = '' } }
async function deleteCredential() {
  const target = deleteTarget.value
  if (!target || deleting.value) return
  deleting.value = true
  try {
    await stepUp.run(() => accountAuthorizationAPI.deleteCredential(target.id, target.revision || ''))
    if (!disposed) { deleting.value = false; closeDelete(); app.showSuccess(t('authorization.credentialsDeleted')); await load() }
  } catch (e) { if (!disposed && !isStepUpCancelled(e)) deleteError.value = extractApiErrorMessage(e, t('authorization.operationFailed')) }
  finally { deleting.value = false }
}

function visibility() { if (document.hidden) { clearReveal(); supplement.value.password = ''; supplement.value.twofa = '' } else void load(true) }
watch(() => props.kind, () => { request?.abort(); rows.value = []; total.value = 0; page.value = 1; status.value = 'all'; query.value = ''; draft.value = ''; deleteTarget.value = null; clearReveal(); stepUp.onCancel(); supplementTarget.value = null; supplement.value = { email: '', password: '', twofa: '' }; void load() })
watch(status, () => { if (page.value !== 1) page.value = 1; else void load() })
watch([page, () => props.refreshKey], () => { void load() })
useIntervalFn(() => { if (!document.hidden && !supplementTarget.value && !revealed.value && !deleteTarget.value) void load(true) }, 5000)
onMounted(() => { document.addEventListener('visibilitychange', visibility); void load() })
onUnmounted(() => { disposed = true; request?.abort(); document.removeEventListener('visibilitychange', visibility); clearReveal(); supplement.value = { email: '', password: '', twofa: '' }; stepUp.onCancel() })
</script>
