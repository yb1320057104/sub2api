<template>
  <BaseDialog :show="show" :title="t('admin.accounts.multiProxy.assignTitle')" width="normal" :close-on-escape="!busy" :close-on-click-outside="!busy" @close="close">
    <form id="assign-account-proxies" class="space-y-5" @submit.prevent="submit">
      <div class="rounded-xl bg-primary-50 p-4 dark:bg-primary-900/20"><p class="text-sm font-medium text-primary-900 dark:text-primary-100">{{ t('admin.accounts.multiProxy.assignSelected', { count: ids.length }) }}</p><p class="mt-1 text-xs leading-5 text-primary-700 dark:text-primary-200">{{ t(mode === 'group' ? 'admin.proxyGroups.groupAssignHint' : 'admin.accounts.multiProxy.assignHint') }}</p></div>
      <div class="flex gap-4 text-sm">
        <label class="flex items-center gap-2"><input v-model="mode" value="available" type="radio" :disabled="busy" />{{ t('admin.proxyGroups.availableMode') }}</label>
        <label class="flex items-center gap-2"><input v-model="mode" value="group" type="radio" :disabled="busy" />{{ t('admin.proxyGroups.groupMode') }}</label>
      </div>
      <div v-if="mode === 'group'" class="space-y-3">
        <label class="input-label">{{ t('admin.proxyGroups.group') }}<Select v-model="groupID" class="mt-1" :options="groupOptions" :disabled="busy || groupsLoading" :placeholder="t('admin.proxyGroups.choose')" :aria-label="t('admin.proxyGroups.group')" /></label>
        <p class="text-xs leading-5 text-gray-500">{{ t('admin.proxyGroups.assignmentHint') }}</p>
        <p v-if="selectedGroup" class="text-xs text-gray-500">{{ t('admin.proxyGroups.counts', { total: selectedGroup.proxy_count, active: selectedGroup.active_proxy_count }) }}</p>
        <p v-if="selectedGroup && !usableGroup" class="text-sm text-amber-700 dark:text-amber-300">{{ t('admin.proxyGroups.unusable') }}</p>
      </div>
      <div class="grid gap-4" :class="mode === 'available' ? 'sm:grid-cols-3' : 'sm:grid-cols-2'">
        <label v-if="mode === 'available'" class="input-label">{{ t('admin.accounts.multiProxy.perAccount') }}<input v-model.number="form.proxies_per_account" data-testid="assign-proxies-per-account" type="number" min="1" max="100" step="1" required :disabled="busy" class="input mt-2" /><span class="mt-1 block text-xs font-normal text-gray-500">1–100</span></label>
        <label v-if="mode === 'available'" class="input-label">{{ t('admin.accounts.multiProxy.accountsLimit') }}<input v-model.number="form.max_accounts_per_proxy" data-testid="assign-proxy-max-accounts" type="number" min="1" max="10000" step="1" required :disabled="busy" class="input mt-2" /><span class="mt-1 block text-xs font-normal text-gray-500">{{ t('admin.accounts.multiProxy.onePerProxy') }}</span></label>
        <label class="input-label">{{ t('admin.accounts.multiProxy.concurrency') }}<input v-model.number="form.proxy_concurrency" data-testid="assign-proxy-concurrency" type="number" min="1" max="10000" step="1" required :disabled="busy" class="input mt-2" /><span class="mt-1 block text-xs font-normal text-gray-500">1–10000</span></label>
        <label v-if="mode === 'group'" class="input-label">{{ t('admin.proxyGroups.accountConcurrency') }}<input v-model.number="accountConcurrency" data-testid="assign-account-concurrency" type="number" min="1" max="10000" step="1" required :disabled="busy" class="input mt-2" /><span class="mt-1 block text-xs font-normal text-gray-500">1–10000</span></label>
      </div>
      <ProxyPoolCapacityPreview v-if="mode === 'group' && usableGroup" :pool-size="selectedGroup!.proxy_count" :account-concurrency="accountConcurrency" :proxy-concurrency="form.proxy_concurrency" />
      <div class="flex items-center justify-between rounded-lg border border-gray-200 px-4 py-3 text-sm dark:border-dark-600"><span class="text-gray-500">{{ t(mode === 'group' ? 'admin.proxyGroups.candidateBindings' : 'admin.accounts.multiProxy.assignBindings') }}</span><strong class="font-mono tabular-nums">{{ valid ? ids.length * (mode === 'group' ? selectedGroup!.proxy_count : form.proxies_per_account) : '—' }}</strong></div>
      <p class="text-xs leading-5 text-gray-500">{{ t(mode === 'group' ? 'admin.proxyGroups.groupAssignAtomic' : 'admin.accounts.multiProxy.assignAtomic') }}</p>
      <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">{{ error }}</p>
    </form>
    <template #footer><div class="flex justify-end gap-2"><button class="btn btn-secondary" :disabled="busy" @click="close">{{ t('common.cancel') }}</button><button class="btn btn-primary" type="submit" form="assign-account-proxies" :disabled="busy || !valid">{{ busy ? t('common.processing') : t('admin.accounts.multiProxy.assignSubmit') }}</button></div></template>
  </BaseDialog>
</template>
<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import ProxyPoolCapacityPreview from '@/components/admin/proxy/ProxyPoolCapacityPreview.vue'
import { assignProxies } from '@/api/admin/accounts'
import { assignAccountProxyGroup, listProxyGroups, type ProxyGroup } from '@/api/admin/proxyGroups'
import { extractApiErrorMessage } from '@/utils/apiError'
import { useAppStore } from '@/stores/app'
const props = defineProps<{ show: boolean; accountIds: number[] }>()
const emit = defineEmits<{ close: []; assigned: [] }>()
const { t } = useI18n()
const app = useAppStore()
const ids = ref<number[]>([])
const busy = ref(false)
const error = ref('')
const mode = ref<'available' | 'group'>('available')
const groupID = ref<number | null>(null)
const groups = ref<ProxyGroup[]>([])
const groupsLoading = ref(false)
const accountConcurrency = ref(100)
let loadVersion = 0
const groupOptions = computed(() => groups.value.map(group => ({ value: group.id, label: `${group.name} (${group.proxy_count})` })))
const selectedGroup = computed(() => groups.value.find(group => group.id === groupID.value))
const usableGroup = computed(() => !!selectedGroup.value && selectedGroup.value.proxy_count > 0 && selectedGroup.value.proxy_count <= 100 && selectedGroup.value.active_proxy_count === selectedGroup.value.proxy_count)
const form = reactive({ proxies_per_account: 1, max_accounts_per_proxy: 1, proxy_concurrency: 3 })
const valid = computed(() => ids.value.length > 0 && ids.value.length <= 5000 && Number.isInteger(form.proxy_concurrency) && form.proxy_concurrency >= 1 && form.proxy_concurrency <= 10000 && (mode.value === 'group' ? !groupsLoading.value && usableGroup.value && Number.isInteger(accountConcurrency.value) && accountConcurrency.value >= 1 && accountConcurrency.value <= 10000 : [form.proxies_per_account, form.max_accounts_per_proxy].every(value => Number.isInteger(value) && value >= 1 && value <= 10000) && form.proxies_per_account <= 100))
watch(() => props.show, show => {
  loadVersion++
  if (show) {
    ids.value = [...new Set(props.accountIds)]; error.value = ids.value.length > 5000 ? t('admin.accounts.multiProxy.assignTooMany') : ''
    mode.value = 'available'; groupID.value = null; groups.value = []; groupsLoading.value = false
    accountConcurrency.value = 100
    Object.assign(form, { proxies_per_account: 1, max_accounts_per_proxy: 1, proxy_concurrency: 3 })
  }
}, { immediate: true })
watch(mode, async value => {
  if (value !== 'group' || !props.show) return
  form.proxy_concurrency = 10
  const version = ++loadVersion
  groupsLoading.value = true; error.value = ''
  try { const result = await listProxyGroups(); if (version === loadVersion) groups.value = result }
  catch { if (version === loadVersion) error.value = t('admin.proxyGroups.loadFailed') }
  finally { if (version === loadVersion) groupsLoading.value = false }
})
function close() { if (!busy.value) emit('close') }
async function submit() {
  if (busy.value || !valid.value) return
  busy.value = true; error.value = ''
  try {
    const result = mode.value === 'group'
      ? await assignAccountProxyGroup({ account_ids: [...ids.value], proxy_group_id: groupID.value!, proxy_concurrency: form.proxy_concurrency, account_concurrency: accountConcurrency.value })
      : await assignProxies({ account_ids: [...ids.value], ...form })
    app.showSuccess(t('admin.accounts.multiProxy.assignSuccess', { accounts: result.account_count, proxies: result.proxy_count, bindings: result.binding_count }))
    emit('assigned'); emit('close')
  } catch (cause) { error.value = extractApiErrorMessage(cause, t('common.error')) }
  finally { busy.value = false }
}
</script>
