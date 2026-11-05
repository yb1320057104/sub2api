<template>
  <BaseDialog :show="show" :title="t('admin.proxyGroups.policyTitle')" width="normal" :close-on-escape="!busy" :close-on-click-outside="!busy" @close="close">
    <form id="group-proxy-policy" class="space-y-5" @submit.prevent="submit">
      <p class="text-sm text-gray-500">{{ t('admin.proxyGroups.selectedGroups', { count: ids.length }) }}</p>
      <div class="rounded-xl bg-primary-50 p-4 text-sm leading-6 text-primary-800 dark:bg-primary-900/20 dark:text-primary-200">{{ t('admin.proxyGroups.strictHint') }}</div>
      <label class="flex items-center justify-between gap-4"><span class="font-medium">{{ t('admin.proxyGroups.strictPriority') }}</span><Toggle v-model="strictPriority" :disabled="busy" /></label>
      <label class="flex items-start gap-2 text-sm"><input v-model="assignProxies" type="checkbox" :disabled="busy" class="mt-1 rounded" data-testid="policy-assign-proxies" /><span>{{ t('admin.proxyGroups.assignTogether') }}</span></label>
      <div v-if="strictPriority || assignProxies" class="space-y-4">
        <label class="input-label">{{ t('admin.proxyGroups.group') }}<Select v-model="groupID" class="mt-1" :options="options" :disabled="busy || loading" :placeholder="t('admin.proxyGroups.choose')" :aria-label="t('admin.proxyGroups.group')" /></label>
        <p v-if="selectedGroup" class="text-xs text-gray-500">{{ t('admin.proxyGroups.counts', { total: selectedGroup.proxy_count, active: selectedGroup.active_proxy_count }) }}</p>
        <div v-if="assignProxies" class="grid gap-4 sm:grid-cols-2">
          <label class="input-label">{{ t('admin.accounts.multiProxy.concurrency') }}<input v-model.number="concurrency" data-testid="policy-proxy-concurrency" type="number" min="1" max="10000" step="1" class="input mt-1" :disabled="busy" required /></label>
          <label class="input-label">{{ t('admin.proxyGroups.accountConcurrency') }}<input v-model.number="accountConcurrency" data-testid="policy-account-concurrency" type="number" min="1" max="10000" step="1" class="input mt-1" :disabled="busy" required /></label>
        </div>
        <ProxyPoolCapacityPreview v-if="assignProxies && usableGroup" :pool-size="selectedGroup!.proxy_count" :account-concurrency="accountConcurrency" :proxy-concurrency="concurrency" />
        <p v-if="assignProxies" class="text-xs leading-5 text-gray-500">{{ t('admin.proxyGroups.assignmentHint') }}</p>
        <p v-if="assignProxies && selectedGroup && !usableGroup" class="text-sm text-amber-700 dark:text-amber-300">{{ t('admin.proxyGroups.unusable') }}</p>
      </div>
      <p v-if="!strictPriority" class="text-xs text-gray-500">{{ t('admin.proxyGroups.disabledHint') }}</p>
      <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">{{ error }}</p>
    </form>
    <template #footer><div class="flex justify-end gap-2"><button class="btn btn-secondary" :disabled="busy" @click="close">{{ t('common.cancel') }}</button><button type="submit" form="group-proxy-policy" class="btn btn-primary" :disabled="busy || loading || !valid">{{ busy ? t('common.processing') : t('common.save') }}</button></div></template>
  </BaseDialog>
</template>
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import Toggle from '@/components/common/Toggle.vue'
import ProxyPoolCapacityPreview from '@/components/admin/proxy/ProxyPoolCapacityPreview.vue'
import { listProxyGroups, proxyGroupErrorMessage, updateGroupProxyPolicy, type GroupProxyPolicy, type ProxyGroup } from '@/api/admin/proxyGroups'
import { useAppStore } from '@/stores/app'

const props = defineProps<{ show: boolean; groupIds: number[]; initialPolicy?: GroupProxyPolicy }>()
const emit = defineEmits<{ close: []; saved: [ids: number[]] }>()
const { t } = useI18n()
const app = useAppStore()
const ids = ref<number[]>([])
const groups = ref<ProxyGroup[]>([])
const groupID = ref<number | null>(null)
const strictPriority = ref(true)
const assignProxies = ref(true)
const concurrency = ref(10)
const accountConcurrency = ref(100)
const busy = ref(false)
const loading = ref(false)
const error = ref('')
let loadVersion = 0
const options = computed(() => groups.value.map(group => ({ value: group.id, label: `${group.name} (${group.proxy_count})` })))
const selectedGroup = computed(() => groups.value.find(group => group.id === groupID.value))
const usableGroup = computed(() => !!selectedGroup.value && selectedGroup.value.proxy_count > 0 && selectedGroup.value.proxy_count <= 100 && selectedGroup.value.active_proxy_count === selectedGroup.value.proxy_count)
const valid = computed(() => ids.value.length > 0 && ids.value.length <= 500 && (!(strictPriority.value || assignProxies.value) || usableGroup.value) && (!assignProxies.value || [concurrency.value, accountConcurrency.value].every(value => Number.isInteger(value) && value >= 1 && value <= 10000)))
watch(() => props.show, async show => {
  const version = ++loadVersion
  if (!show) { loading.value = false; return }
  ids.value = [...new Set(props.groupIds)]; groupID.value = props.initialPolicy?.proxy_group_id ?? null
  strictPriority.value = props.initialPolicy?.strict_priority ?? true; assignProxies.value = true; concurrency.value = 10; accountConcurrency.value = 100; error.value = ''; groups.value = []; loading.value = true
  try { const result = await listProxyGroups(); if (version === loadVersion) groups.value = result }
  catch { if (version === loadVersion) error.value = t('admin.proxyGroups.loadFailed') }
  finally { if (version === loadVersion) loading.value = false }
}, { immediate: true })
function close() { if (!busy.value) emit('close') }
async function submit() {
  if (busy.value || loading.value || !valid.value) return
  const selectedIDs = [...ids.value]
  busy.value = true; error.value = ''
  try {
    const result = await updateGroupProxyPolicy({
      group_ids: selectedIDs,
      strict_priority: strictPriority.value,
      assign_proxies: assignProxies.value,
      ...((strictPriority.value || assignProxies.value) ? { proxy_group_id: groupID.value } : {}),
      ...(assignProxies.value ? { proxy_concurrency: concurrency.value, account_concurrency: accountConcurrency.value } : {})
    })
    app.showSuccess(t(assignProxies.value ? 'admin.proxyGroups.policyAssigned' : 'admin.proxyGroups.policySaved', { groups: result.group_count, accounts: result.account_count, proxies: result.proxy_count }))
    emit('saved', selectedIDs); emit('close')
  } catch (cause) { error.value = proxyGroupErrorMessage(cause, t('admin.proxyGroups.policyFailed')) }
  finally { busy.value = false }
}
</script>
