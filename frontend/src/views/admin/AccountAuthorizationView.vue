<template>
  <AppLayout>
    <div class="mx-auto max-w-[1600px] space-y-6">
      <header class="flex flex-wrap items-start justify-between gap-4"><div><h1 class="text-2xl font-semibold tracking-tight text-gray-900 dark:text-white">{{ t('authorization.title') }}</h1><p class="mt-2 max-w-3xl text-sm leading-6 text-gray-500 dark:text-dark-400">{{ t('authorization.description') }}</p></div><RouterLink to="/admin/radar?tab=authorization" class="btn btn-secondary">{{ t('authorization.openInspection') }} →</RouterLink></header>
      <div class="border-b border-gray-200 pb-4 dark:border-dark-700"><nav class="inline-flex max-w-full flex-wrap rounded-2xl bg-primary-100/60 p-1 dark:bg-dark-800" role="tablist" :aria-label="t('authorization.title')"><button v-for="item in tabs" :id="'authorization-tab-' + item" :key="item" type="button" role="tab" :aria-selected="tab === item" :aria-controls="'authorization-panel-' + item" :tabindex="tab === item ? 0 : -1" class="rounded-xl px-5 py-2.5 text-sm font-semibold focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500" :class="tab === item ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-300' : 'text-gray-500 dark:text-dark-400'" @click="tab = item" @keydown.left.prevent="switchTab(-1)" @keydown.right.prevent="switchTab(1)">{{ t('authorization.tabs.' + item) }}</button></nav></div>
      <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }} <button type="button" class="underline" @click="load">{{ t('authorization.refresh') }}</button></p>
      <p v-if="config && !config.encryption_key_configured" class="rounded-xl bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-950/30 dark:text-amber-200">{{ t('authorization.encryptionMissing') }}</p>
      <section v-if="tab === 'workbench'" id="authorization-panel-workbench" role="tabpanel" aria-labelledby="authorization-tab-workbench" class="space-y-6">
        <form class="grid gap-5 rounded-2xl border border-gray-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-800 lg:grid-cols-[minmax(0,1.5fr)_minmax(300px,1fr)]" autocomplete="off" @submit.prevent="submit">
          <div class="space-y-3"><label class="block"><span class="input-label">{{ t('authorization.accountInput') }}</span><textarea v-model="form.accounts" class="input min-h-64 font-mono text-sm" :placeholder="t('authorization.accountPlaceholder')" :aria-label="t('authorization.accountInput')" autocomplete="off" spellcheck="false" required /></label><p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('authorization.accountInputHint') }}</p><p v-if="config && !config.browser_available" class="rounded-xl bg-amber-50 p-3 text-xs leading-5 text-amber-800 dark:bg-amber-950/30 dark:text-amber-200">{{ t('authorization.browserMissing') }}</p></div>
          <fieldset :disabled="submitting || !ready" class="space-y-4">
            <div class="grid grid-cols-2 gap-4"><label><span class="input-label">{{ t('authorization.authorizationConcurrency') }}</span><input v-model.number="form.authorization_concurrency" class="input" type="number" min="1" max="50" step="1" required /></label><label><span class="input-label">{{ t('authorization.accountConcurrency') }}</span><input v-model.number="form.account_concurrency" class="input" type="number" min="1" max="1000" step="1" required /></label></div>
            <label class="block"><span class="input-label">{{ t('authorization.priority') }}</span><input v-model.number="form.account_priority" class="input" type="number" min="0" max="1000" step="1" required /></label>
            <div class="space-y-3 rounded-xl border border-gray-200 p-3 dark:border-dark-600">
              <label class="block"><span class="input-label">{{ t('authorization.accountProxyMode') }}</span><Select :model-value="form.account_proxy_mode" @update:model-value="form.account_proxy_mode = $event === 'managed' ? 'managed' : 'direct'" :aria-label="t('authorization.accountProxyMode')" :disabled="submitting || !ready" :options="[{ value: 'direct', label: t('authorization.accountDirect') }, { value: 'managed', label: t('authorization.accountManaged') }]" /></label>
              <label v-if="form.account_proxy_mode === 'managed'" class="block"><span class="input-label">{{ t('authorization.maxAccountsPerProxy') }}</span><input v-model.number="form.max_accounts_per_proxy" class="input" type="number" min="1" max="1000" step="1" required /></label>
              <p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('authorization.accountProxyHint') }}</p>
            </div>
            <div class="rounded-xl bg-primary-50 p-3 text-xs leading-5 text-primary-800 dark:bg-primary-950/30 dark:text-primary-200"><p class="font-semibold">{{ t('authorization.gateTitle') }}</p><p>{{ t('authorization.gateHint') }}</p><p class="mt-2 font-medium">{{ t('authorization.gateModels', { count: gateModels.length }) }}</p><p v-for="model in gateModels" :key="model.model">{{ model.model }} / {{ model.effort || t('accountCandy.defaultEffort') }}</p></div>
            <div class="grid gap-4 sm:grid-cols-2">
              <fieldset class="min-w-0"><legend class="input-label">{{ t('authorization.passGroups') }}</legend><div class="max-h-36 space-y-2 overflow-y-auto rounded-xl border border-gray-200 p-3 dark:border-dark-600"><label v-for="group in groups" :key="group.id" class="flex cursor-pointer items-center gap-2 text-sm"><input v-model="form.pass_group_ids" type="checkbox" :value="group.id" class="rounded border-gray-300 text-primary-600" /><span class="break-all">{{ group.name }}</span></label><p v-if="!groups.length" class="text-xs text-gray-500">{{ t('authorization.noGroups') }}</p></div></fieldset>
              <div class="min-w-0"><p class="input-label">{{ t('authorization.failGroups') }}</p><div class="rounded-xl border border-gray-200 p-3 text-sm dark:border-dark-600"><p class="break-all">{{ failureGroupName }}</p><p class="mt-2 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('authorization.failureGroupHint') }}</p><button type="button" class="mt-2 text-xs font-medium text-primary-600 dark:text-primary-300" @click="showCandySettings = true">{{ t('accountCandy.settings') }}</button></div></div>
            </div>
            <p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('authorization.importHint') }}</p>
            <button type="submit" class="btn btn-primary w-full" :disabled="submitting || !ready">{{ t(submitting ? 'authorization.submitting' : 'authorization.submit') }}</button>
          </fieldset>
        </form>
        <div class="flex items-center justify-between"><h2 class="font-semibold text-gray-900 dark:text-white">{{ t('authorization.jobs') }}</h2><span class="text-xs text-gray-500">{{ t('authorization.refreshHint') }}</span></div>
        <AuthorizationRecords kind="jobs" :refresh-key="refreshKey" />
      </section>
      <section v-else-if="tab === 'proxies'" id="authorization-panel-proxies" role="tabpanel" aria-labelledby="authorization-tab-proxies" class="space-y-5">
        <form class="space-y-4 rounded-2xl border border-gray-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-800" autocomplete="off" @submit.prevent="addProxies"><label class="block"><span class="input-label">{{ t('authorization.proxyInput') }}</span><textarea v-model="proxyText" class="input min-h-36 font-mono text-sm" placeholder="socks5://username:password@proxy.example:1080" :aria-label="t('authorization.proxyInput')" autocomplete="off" spellcheck="false" required /></label><div class="flex flex-wrap items-center justify-between gap-4"><p class="max-w-3xl text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('authorization.proxyHint') }}</p><button class="btn btn-primary" :disabled="proxySaving">{{ t('authorization.saveProxies') }}</button></div></form>
        <AuthorizationRecords kind="proxies" :refresh-key="refreshKey" />
      </section>
      <section v-else id="authorization-panel-history" role="tabpanel" aria-labelledby="authorization-tab-history" class="space-y-4"><p class="text-sm leading-6 text-gray-500 dark:text-dark-400">{{ t('authorization.historyHint') }}</p><AuthorizationRecords kind="history" /></section>
      <AccountCandyTestSettings :show="showCandySettings" @close="showCandySettings = false" @saved="load" />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import Select from '@/components/common/Select.vue'
import AuthorizationRecords from '@/components/admin/account/AuthorizationRecords.vue'
import AccountCandyTestSettings from '@/components/admin/account/AccountCandyTestSettings.vue'
import { accountAuthorizationAPI, type AuthorizationConfig, type AuthorizationSubmission } from '@/api/admin/accountAuthorization'
import { apiClient } from '@/api/client'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { AdminGroup } from '@/types'
const { t } = useI18n()
const app = useAppStore()
const route = useRoute()
const router = useRouter()
const tabs = ['workbench', 'proxies', 'history'] as const
const tab = ref<typeof tabs[number]>(route.query.tab === 'proxies' ? 'proxies' : route.query.tab === 'history' ? 'history' : 'workbench')
const config = ref<AuthorizationConfig | null>(null)
const groups = ref<AdminGroup[]>([])
const ready = ref(false)
const error = ref('')
const submitting = ref(false)
const proxySaving = ref(false)
const proxyText = ref('')
const refreshKey = ref(0)
const controller = new AbortController()
const showCandySettings = ref(false)
const gateModels = computed(() => config.value?.intelligence_models ?? (config.value?.intelligence_model ? [{ model: config.value.intelligence_model, effort: config.value.intelligence_effort, enabled: true }] : []))
const failureGroupName = computed(() => { const id = config.value?.fail_group_ids?.[0]; return id ? (groups.value.find(group => group.id === id)?.name || '#' + id) : t('authorization.failureGroupMissing') })
const form = ref<AuthorizationSubmission>({ accounts: '', authorization_concurrency: 3, account_concurrency: 3, account_priority: 50, account_proxy_mode: 'direct', max_accounts_per_proxy: 1, group_ids: [], pass_group_ids: [] })
async function load() {
  try { const [value, groupResponse] = await Promise.all([accountAuthorizationAPI.config(controller.signal), apiClient.get<AdminGroup[]>('/admin/groups/all', { params: { platform: 'openai' }, signal: controller.signal })]); if (controller.signal.aborted) return; config.value = value; groups.value = groupResponse.data.filter(group => group.platform === 'openai' && group.status === 'active'); if (!ready.value) form.value = { ...form.value, authorization_concurrency: value.authorization_concurrency, account_concurrency: value.account_concurrency, account_priority: value.account_priority, account_proxy_mode: value.account_proxy_mode || 'direct', max_accounts_per_proxy: value.max_accounts_per_proxy || 1, group_ids: [], pass_group_ids: [...(value.pass_group_ids || [])] }; ready.value = true; error.value = '' }
  catch (e) { if (!controller.signal.aborted) error.value = extractApiErrorMessage(e, t('authorization.loadFailed')) }
}
async function submit() {
  if (!ready.value || submitting.value) return
  if (!form.value.pass_group_ids.length) { app.showError(t('authorization.gateGroupsRequired')); return }
  submitting.value = true
  try { await accountAuthorizationAPI.submit(form.value); form.value.accounts = ''; if (!controller.signal.aborted) { refreshKey.value++; app.showSuccess(t('authorization.submitted')) } }
  catch (e) { if (!controller.signal.aborted) app.showError(extractApiErrorMessage(e, t('authorization.operationFailed'))) }
  finally { submitting.value = false }
}
async function addProxies() {
  if (proxySaving.value) return
  proxySaving.value = true
  try { await accountAuthorizationAPI.addProxies(proxyText.value); proxyText.value = ''; if (!controller.signal.aborted) { refreshKey.value++; app.showSuccess(t('authorization.saved')) } }
  catch (e) { if (!controller.signal.aborted) app.showError(extractApiErrorMessage(e, t('authorization.saveFailed'))) }
  finally { proxySaving.value = false }
}
async function switchTab(direction: number) { tab.value = tabs[(tabs.indexOf(tab.value) + direction + tabs.length) % tabs.length]!; await nextTick(); document.getElementById('authorization-tab-' + tab.value)?.focus() }
watch(tab, value => { void router.replace({ query: { ...route.query, tab: value } }) })
watch(() => route.query.tab, value => { tab.value = value === 'proxies' ? 'proxies' : value === 'history' ? 'history' : 'workbench' })
onMounted(load)
onUnmounted(() => { controller.abort(); form.value.accounts = ''; proxyText.value = '' })
</script>
