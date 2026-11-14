<template>
    <div class="mx-auto max-w-[1600px] space-y-6">
      <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }} <button type="button" class="underline" :disabled="busy" @click="refreshWorkbench">{{ t('authorization.refreshSettings') }}</button></p>
      <p v-if="config && !config.encryption_key_configured" class="rounded-xl bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-950/30 dark:text-amber-200">{{ t('authorization.encryptionMissing') }}</p>
      <section v-if="tab === 'workbench'" id="authorization-panel-workbench" role="tabpanel" aria-labelledby="guard-tab-workbench" class="space-y-6">
        <form class="grid gap-5 rounded-2xl border border-gray-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-800 lg:grid-cols-[minmax(0,1.5fr)_minmax(300px,1fr)]" autocomplete="off" @submit.prevent="submit">
          <div class="space-y-3"><label class="block"><span class="input-label">{{ t('authorization.accountInput') }}</span><textarea v-model="form.accounts" class="input min-h-64 font-mono text-sm" :disabled="busy || !ready" :placeholder="t('authorization.accountPlaceholder')" :aria-label="t('authorization.accountInput')" autocomplete="off" spellcheck="false" required /></label><p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('authorization.accountInputHint') }}</p><p v-if="config && !config.browser_available" class="rounded-xl bg-amber-50 p-3 text-xs leading-5 text-amber-800 dark:bg-amber-950/30 dark:text-amber-200">{{ t('authorization.browserMissing') }}</p>
            <div v-if="form.intelligence_gate" class="space-y-2 rounded-xl bg-primary-50 p-3 text-xs leading-5 text-primary-800 dark:bg-primary-950/30 dark:text-primary-200">
              <p class="font-semibold">{{ t('authorization.gateTitle') }}</p>
              <p>{{ t('authorization.gateHint') }}</p>
              <p class="font-medium">{{ t('authorization.gateModels', { count: gateModels.length }) }}</p>
              <p v-for="model in gateModels" :key="model.model">{{ model.model }} / {{ model.effort || t('accountCandy.defaultEffort') }}</p>
              <p class="font-medium">{{ t('accountCandy.groupRules') }}</p>
              <ol v-if="gateRules.length" class="list-inside list-decimal space-y-1">
                <li v-for="rule in gateRules" :key="rule.source_group_id" class="break-words">{{ t('authorization.groupRuleSummary', { source: groupName(rule.source_group_id), pass: rule.pass_group_ids.map(groupName).join(' / '), fail: groupName(rule.fail_group_id) }) }}</li>
              </ol>
              <p v-else>{{ t('accountCandy.noRules') }}</p>
              <p>{{ t('authorization.gateRuleHint') }}</p>
              <button type="button" class="font-medium underline" :disabled="busy || !ready" @click="showCandySettings = true">{{ t('accountCandy.settings') }}</button>
            </div>
          </div>
          <fieldset :disabled="busy || !ready" class="space-y-4">
            <details class="space-y-3 rounded-xl border border-gray-200 p-3 dark:border-dark-600" data-testid="authorization-advanced-settings">
              <summary class="cursor-pointer text-sm font-medium">{{ t('tokenGuardV2.advancedFirstAuthorization') }}</summary>
            <div class="grid grid-cols-2 gap-4"><label><span class="input-label">{{ t('authorization.authorizationConcurrency') }}</span><input v-model.number="form.authorization_concurrency" class="input" type="number" min="1" max="50" step="1" required /></label><label><span class="input-label">{{ t('authorization.accountConcurrency') }}</span><input v-model.number="form.account_concurrency" class="input" type="number" min="1" max="1000" step="1" required /></label></div>
            <label class="block"><span class="input-label">{{ t('authorization.priority') }}</span><input v-model.number="form.account_priority" class="input" type="number" min="0" max="1000" step="1" required /></label>
            <div class="space-y-3 rounded-xl border border-gray-200 p-3 dark:border-dark-600">
              <label class="block"><span class="input-label">{{ t('authorization.accountProxyMode') }}</span><Select :model-value="form.account_proxy_mode" @update:model-value="form.account_proxy_mode = $event === 'managed' ? 'managed' : 'direct'" :aria-label="t('authorization.accountProxyMode')" :disabled="busy || !ready" :options="[{ value: 'direct', label: t('authorization.accountDirect') }, { value: 'managed', label: t('authorization.accountManaged') }]" /></label>
              <div v-if="form.account_proxy_mode === 'managed'" class="space-y-3" data-testid="authorization-managed-proxy-settings">
                <div class="grid grid-cols-2 gap-3">
                  <label class="block"><span class="input-label">{{ t('authorization.proxiesPerAccount') }}</span><input v-model.number="form.proxies_per_account" class="input" type="number" min="1" max="100" step="1" :aria-label="t('authorization.proxiesPerAccount')" required /></label>
                  <label class="block"><span class="input-label">{{ t('authorization.proxyConcurrency') }}</span><input v-model.number="form.proxy_concurrency" class="input" type="number" min="1" max="10000" step="1" :aria-label="t('authorization.proxyConcurrency')" required /></label>
                </div>
                <label class="block"><span class="input-label">{{ t('authorization.maxAccountsPerProxy') }}</span><input v-model.number="form.max_accounts_per_proxy" class="input" type="number" min="1" max="10000" step="1" :aria-label="t('authorization.maxAccountsPerProxy')" required /></label>
                <p class="rounded-lg bg-amber-50 px-3 py-2 text-xs leading-5 text-amber-800 dark:bg-amber-950/30 dark:text-amber-200">{{ t('authorization.proxyGlobalLimitsHint') }}</p>
              </div>
              <p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('authorization.accountProxyHint') }}</p>
            </div>
            </details>
            <fieldset class="min-w-0"><legend class="input-label">{{ t('authorization.passGroups') }}</legend><div class="max-h-36 space-y-2 overflow-y-auto rounded-xl border border-gray-200 p-3 dark:border-dark-600"><label v-for="group in groups" :key="group.id" class="flex cursor-pointer items-center gap-2 text-sm"><input v-model="form.pass_group_ids" type="checkbox" :value="group.id" class="rounded border-gray-300 text-primary-600" /><span class="break-all">{{ group.name }}</span></label><p v-if="!groups.length" class="text-xs text-gray-500">{{ t('authorization.noGroups') }}</p></div></fieldset>
            <div class="space-y-2 rounded-xl border border-gray-200 p-3 dark:border-dark-600">
              <label class="flex items-center justify-between gap-3 text-sm font-medium text-gray-800 dark:text-dark-100"><span>{{ t('authorization.gateEnabled') }}</span><Toggle v-model="form.intelligence_gate" :disabled="busy || !ready" :aria-label="t('authorization.gateEnabled')" /></label>
              <p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t(form.intelligence_gate ? 'authorization.gateEnabledHint' : 'authorization.gateDisabledHint') }}</p>
            </div>
            <p v-if="removedPassGroupIds.length" role="status" class="rounded-xl bg-amber-50 p-3 text-xs leading-5 text-amber-800 dark:bg-amber-950/30 dark:text-amber-200">{{ t('authorization.passGroupsRemoved', { ids: removedPassGroupIds.map(id => '#' + id).join(', ') }) }}</p>
            <p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('authorization.importHint') }}</p>
            <p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('authorization.saveWorkbenchHint') }}</p>
            <button type="button" class="btn btn-secondary w-full" :disabled="busy || !ready" @click="saveWorkbench">{{ t(savingWorkbench ? 'accountCandy.saving' : 'authorization.saveWorkbench') }}</button>
            <button type="submit" class="btn btn-primary w-full" :disabled="busy || !ready">{{ t(submitting ? 'authorization.submitting' : 'authorization.submit') }}</button>
          </fieldset>
        </form>
        <div class="flex items-center justify-between"><h2 class="font-semibold text-gray-900 dark:text-white">{{ t('authorization.jobs') }}</h2><span class="text-xs text-gray-500">{{ t('authorization.refreshHint') }}</span></div>
        <AuthorizationRecords kind="jobs" :refresh-key="refreshKey" />
      </section>
      <section v-else-if="tab === 'proxies'" id="authorization-panel-proxies" role="tabpanel" aria-labelledby="guard-tab-proxies" class="space-y-5">
        <p class="text-sm leading-6 text-gray-500 dark:text-dark-400">{{ t('tokenGuardV2.authorizationProxyPoolHint') }}</p>
        <form class="space-y-4 rounded-2xl border border-gray-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-800" autocomplete="off" @submit.prevent="addProxies"><label class="block"><span class="input-label">{{ t('authorization.proxyInput') }}</span><textarea v-model="proxyText" class="input min-h-36 font-mono text-sm" placeholder="socks5://username:password@proxy.example:1080" :aria-label="t('authorization.proxyInput')" autocomplete="off" spellcheck="false" required /></label><div class="flex flex-wrap items-center justify-between gap-4"><p class="max-w-3xl text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('authorization.proxyHint') }}</p><button class="btn btn-primary" :disabled="proxySaving">{{ t('authorization.saveProxies') }}</button></div></form>
        <AuthorizationRecords kind="proxies" :refresh-key="refreshKey" />
      </section>
      <section v-else id="authorization-panel-history" role="tabpanel" aria-labelledby="guard-tab-history" class="space-y-4"><p class="text-sm leading-6 text-gray-500 dark:text-dark-400">{{ t('authorization.historyHint') }}</p><AuthorizationRecords kind="history" /></section>
      <AccountCandyTestSettings :show="showCandySettings" @close="showCandySettings = false" @saved="load" />
    </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import Toggle from '@/components/common/Toggle.vue'
import AuthorizationRecords from '@/components/admin/account/AuthorizationRecords.vue'
import AccountCandyTestSettings from '@/components/admin/account/AccountCandyTestSettings.vue'
import { accountAuthorizationAPI, type AuthorizationConfig, type AuthorizationSubmission } from '@/api/admin/accountAuthorization'
import { apiClient } from '@/api/client'
import { useAppStore } from '@/stores/app'
import { extractApiErrorCode, extractApiErrorMessage } from '@/utils/apiError'
import type { AdminGroup } from '@/types'
const { t } = useI18n()
const app = useAppStore()
const props = defineProps<{ section: 'workbench' | 'proxies' | 'history' }>()
const emit = defineEmits<{ saved: [] }>()
const tab = computed(() => props.section)
const config = ref<AuthorizationConfig | null>(null)
const groups = ref<AdminGroup[]>([])
const ready = ref(false)
const removedPassGroupIds = ref<number[]>([])
let loadVersion = 0
const error = ref('')
const submitting = ref(false)
const savingWorkbench = ref(false)
const refreshingWorkbench = ref(false)
const busy = computed(() => submitting.value || savingWorkbench.value || refreshingWorkbench.value)
const workbenchConflict = ref(false)
const proxySaving = ref(false)
const proxyText = ref('')
const refreshKey = ref(0)
const controller = new AbortController()
const showCandySettings = ref(false)
const gateModels = computed(() => config.value?.intelligence_models ?? (config.value?.intelligence_model ? [{ model: config.value.intelligence_model, effort: config.value.intelligence_effort, enabled: true }] : []))
const gateRules = computed(() => config.value?.intelligence_group_rules ?? [])
const groupName = (id: number) => groups.value.find(group => group.id === id)?.name ?? t('accountCandy.unavailableGroup', { id })
const form = ref<AuthorizationSubmission>({ accounts: '', intelligence_gate: false, workbench_revision: '', authorization_concurrency: 3, account_concurrency: 20, account_priority: 1, account_proxy_mode: 'direct', max_accounts_per_proxy: 3, proxies_per_account: 1, proxy_concurrency: 3, group_ids: [], pass_group_ids: [] })

function applyWorkbench(value: AuthorizationConfig) {
  form.value = { ...form.value, intelligence_gate: value.intelligence_gate, workbench_revision: value.workbench_revision, authorization_concurrency: value.workbench_authorization_concurrency ?? 3, account_concurrency: value.workbench_account_concurrency ?? 20, account_priority: value.workbench_account_priority ?? 1, account_proxy_mode: value.account_proxy_mode || 'direct', max_accounts_per_proxy: value.max_accounts_per_proxy ?? 3, proxies_per_account: value.proxies_per_account ?? 1, proxy_concurrency: value.proxy_concurrency ?? 3, group_ids: [], pass_group_ids: [...(value.pass_group_ids || [])] }
  workbenchConflict.value = false
}

async function load(resetWorkbench = false): Promise<boolean> {
  const version = ++loadVersion
  try {
    const [value, groupResponse] = await Promise.all([
      accountAuthorizationAPI.config(controller.signal),
      apiClient.get<AdminGroup[]>('/admin/groups/all', { params: { platform: 'openai' }, signal: controller.signal })
    ])
    if (controller.signal.aborted || version !== loadVersion) return false
    config.value = value
    groups.value = groupResponse.data.filter(group => group.platform === 'openai' && group.status === 'active')
    if (!ready.value || resetWorkbench) applyWorkbench(value)
    else if (form.value.workbench_revision !== value.workbench_revision) workbenchConflict.value = true
    // Deleted choices are no longer rendered as checkboxes, but can remain in
    // the saved/draft array. Preserve valid choices and require review after removal.
    const availableIDs = new Set(groups.value.map(group => group.id))
    const removed = form.value.pass_group_ids.filter(id => !availableIDs.has(id))
    form.value.pass_group_ids = form.value.pass_group_ids.filter(id => availableIDs.has(id))
    if (removed.length) removedPassGroupIds.value = [...new Set(removed)]
    ready.value = true
    error.value = workbenchConflict.value ? t('authorization.workbenchChanged') : ''
    return removed.length === 0 && !workbenchConflict.value
  } catch (e) {
    if (!controller.signal.aborted && version === loadVersion) error.value = extractApiErrorMessage(e, t('authorization.loadFailed'))
    return false
  }
}

async function refreshWorkbench() {
  if (busy.value) return
  refreshingWorkbench.value = true
  try { await load(true) }
  finally { refreshingWorkbench.value = false }
}

function validWorkbench(): boolean {
  const value = form.value
  if (![value.authorization_concurrency, value.account_concurrency, value.account_priority, value.max_accounts_per_proxy, value.proxies_per_account, value.proxy_concurrency].every(Number.isInteger) ||
    value.authorization_concurrency < 1 || value.authorization_concurrency > 50 || value.account_concurrency < 1 || value.account_concurrency > 1000 ||
    value.account_priority < 0 || value.account_priority > 1000 || (value.max_accounts_per_proxy ?? 0) < 1 || (value.max_accounts_per_proxy ?? 0) > 10000 ||
    (value.proxies_per_account ?? 0) < 1 || (value.proxies_per_account ?? 0) > 100 || (value.proxy_concurrency ?? 0) < 1 || (value.proxy_concurrency ?? 0) > 10000) {
    app.showError(t('authorization.invalidWorkbench'))
    return false
  }
  return true
}

function operationError(cause: unknown) {
  if (extractApiErrorCode(cause) === 'AUTHORIZATION_WORKBENCH_CHANGED') {
    workbenchConflict.value = true
    error.value = t('authorization.workbenchChanged')
  } else app.showError(extractApiErrorMessage(cause, t('authorization.operationFailed')))
}

async function saveWorkbench() {
  if (!ready.value || busy.value || !validWorkbench()) return
  savingWorkbench.value = true
  try {
    if (!await load()) return
    const value = await accountAuthorizationAPI.saveWorkbench({ ...form.value, accounts: '' })
    if (controller.signal.aborted) return
    config.value = value
    applyWorkbench(value)
    removedPassGroupIds.value = []
    app.showSuccess(t('authorization.workbenchSaved'))
  } catch (cause) { if (!controller.signal.aborted) operationError(cause) }
  finally { savingWorkbench.value = false }
}

async function submit() {
  if (!ready.value || busy.value || !validWorkbench()) return
  submitting.value = true
  let submitted: AuthorizationSubmission | null = null
  try {
    // Recheck a long-lived page before posting. If choices changed, this click
    // only refreshes the form; a new explicit click is needed to submit a task.
    if (!await load()) return
    if (!form.value.pass_group_ids.length) { app.showError(t('authorization.gateGroupsRequired')); return }
    submitted = { ...form.value }
    form.value.accounts = ''
    await accountAuthorizationAPI.submit(submitted)
    if (!controller.signal.aborted) {
      removedPassGroupIds.value = []
      refreshKey.value++
      emit('saved')
      app.showSuccess(t('authorization.submitted'))
      await load(true)
    }
  } catch (e) { if (!controller.signal.aborted) operationError(e) }
  finally { if (submitted) submitted.accounts = ''; submitting.value = false }
}
async function addProxies() {
  if (proxySaving.value) return
  proxySaving.value = true
  let submitted = proxyText.value
  proxyText.value = ''
  try { await accountAuthorizationAPI.addProxies(submitted); if (!controller.signal.aborted) { refreshKey.value++; app.showSuccess(t('authorization.saved')) } }
  catch (e) { if (!controller.signal.aborted) app.showError(extractApiErrorMessage(e, t('authorization.saveFailed'))) }
  finally { submitted = ''; proxySaving.value = false }
}
function clearSecrets() { form.value.accounts = ''; proxyText.value = '' }
function visibility() { if (document.hidden) clearSecrets() }
watch(tab, () => { clearSecrets(); showCandySettings.value = false })
onMounted(() => { void load(); document.addEventListener('visibilitychange', visibility) })
onUnmounted(() => { controller.abort(); clearSecrets(); document.removeEventListener('visibilitychange', visibility) })
</script>
