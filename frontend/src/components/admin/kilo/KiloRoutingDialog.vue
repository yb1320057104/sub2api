<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import KiloProxySelect from './KiloProxySelect.vue'
import KiloProxyPreflight from './KiloProxyPreflight.vue'
import { isKiloRequestCanceled, kiloErrorMessage, setRouting } from '@/api/admin/kilo'
import type { KiloPrepared, KiloProxyGroup } from '@/api/admin/kilo'
import { kiloRequestProxyOptions, kiloRoutingOptions, parseAutoReleaseStatuses } from './routing'

const props = defineProps<{ prepared: KiloPrepared; proxyGroups: KiloProxyGroup[]; choicesLoading: boolean; choicesError: string }>()
const emit = defineEmits<{ close: []; saved: []; reloadChoices: [] }>()
const { t } = useI18n()
const form = reactive({ loginProxyId: null as number | null, ...kiloRoutingOptions(), statuses: '' })
const saving = shallowRef(false)
const error = shallowRef('')
let controller: AbortController | undefined
const ready = computed(() => props.prepared.status === 'ready' && props.prepared.identity_status !== 'banned' && props.prepared.identity_status !== 'retired' && (!props.prepared.binding_state || props.prepared.binding_state === 'empty'))
const editable = computed(() => ready.value)
const loginFixed = computed(() => !!props.prepared.login_proxy_id)
const proxyOptions = computed(() => kiloRequestProxyOptions(props.proxyGroups, t, false))
const statuses = computed(() => parseAutoReleaseStatuses(form.statuses))
const validation = computed(() => {
  if (!editable.value) return t('kilo.routing.boundHint')
  if (statuses.value === null) return t('kilo.routing.invalidStatuses')
  if (props.choicesError) return props.choicesError
  if (!form.loginProxyId) return t('kilo.routing.loginRequired')
  if (!form.proxy_group_id) return t('kilo.batch.proxyRequired')
  if (!Number.isInteger(form.proxies_per_account) || form.proxies_per_account < 1 || form.proxies_per_account > 100 || !Number.isInteger(form.proxy_concurrency) || form.proxy_concurrency < 1 || form.proxy_concurrency > 10000 || !Number.isInteger(form.account_concurrency) || form.account_concurrency < 1 || form.account_concurrency > 10000 || !Number.isInteger(form.priority) || form.priority < 0 || form.priority > 10000) return t('kilo.batch.invalidNumbers')
  return ''
})
watch(() => props.prepared, prepared => {
  const options = kiloRoutingOptions(prepared.routing_options)
  Object.assign(form, options, { loginProxyId: prepared.login_proxy_id ?? null, statuses: options.auto_release_statuses.join(', ') })
  error.value = ''
}, { immediate: true })
async function save() {
  if (saving.value || validation.value || ready.value && props.choicesLoading || statuses.value === null) return
  saving.value = true
  error.value = ''
  controller = new AbortController()
  try {
    const input = {
      login_proxy_id: form.loginProxyId!, proxy_group_id: form.proxy_group_id,
      proxies_per_account: form.proxies_per_account, proxy_concurrency: form.proxy_concurrency,
      account_concurrency: form.account_concurrency, priority: form.priority,
      auto_release_statuses: statuses.value
    }
    await setRouting(props.prepared.id, input, controller.signal)
    emit('saved')
  } catch (cause) {
    if (!isKiloRequestCanceled(cause)) error.value = kiloErrorMessage(cause, t('kilo.errors.routingSave'))
  } finally { saving.value = false }
}
onBeforeUnmount(() => controller?.abort())
</script>

<template>
  <BaseDialog :show="true" :title="t('kilo.routing.title', { name: prepared.name })" width="wide" :show-close-button="!saving" :close-on-escape="!saving" @close="!saving && emit('close')">
    <form id="kilo-routing-form" class="space-y-5" @submit.prevent="save">
      <p class="text-sm leading-6 text-gray-600 dark:text-dark-300">{{ t('kilo.routing.description') }}</p>
      <p v-if="!ready" class="rounded-lg bg-amber-50 p-3 text-sm leading-6 text-amber-800 dark:bg-amber-900/20 dark:text-amber-300">{{ t('kilo.routing.boundHint') }}</p>
      <div><label for="kilo-routing-login" class="mb-2 block text-sm font-medium">{{ t('kilo.routing.loginProxy') }}</label><p v-if="loginFixed" class="text-sm text-gray-600 dark:text-dark-300">{{ t('kilo.routing.loginIdentity', { name: prepared.login_proxy_name || '', id: prepared.login_proxy_id }) }}</p><KiloProxySelect v-else id="kilo-routing-login" v-model="form.loginProxyId" :account-id="prepared.account_id" :disabled="saving || !ready" preflight /><KiloProxyPreflight v-if="loginFixed" :proxy-id="prepared.login_proxy_id" :disabled="saving" /><p class="mt-2 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('kilo.routing.loginHint') }}</p></div>
      <div class="grid gap-4 sm:grid-cols-2">
        <div class="sm:col-span-2"><label for="kilo-routing-group" class="mb-2 block text-sm font-medium">{{ t('kilo.batch.proxyGroup') }}</label><Select id="kilo-routing-group" v-model="form.proxy_group_id" :options="proxyOptions" :disabled="saving || !ready || choicesLoading" :aria-label="t('kilo.batch.proxyGroup')" :placeholder="t('kilo.batch.proxyPlaceholder')" /></div>
        <div><label for="kilo-routing-count" class="mb-2 block text-sm font-medium">{{ t('kilo.batch.proxiesPerAccount') }}</label><input id="kilo-routing-count" v-model.number="form.proxies_per_account" type="number" min="1" max="100" required class="input w-full" :disabled="saving || !ready" /></div>
        <div><label for="kilo-routing-per-ip" class="mb-2 block text-sm font-medium">{{ t('kilo.batch.proxyConcurrency') }}</label><input id="kilo-routing-per-ip" v-model.number="form.proxy_concurrency" type="number" min="1" max="10000" required class="input w-full" :disabled="saving || !ready" /></div>
        <div><label for="kilo-routing-total" class="mb-2 block text-sm font-medium">{{ t('kilo.batch.accountConcurrency') }}</label><input id="kilo-routing-total" v-model.number="form.account_concurrency" type="number" min="1" max="10000" required class="input w-full" :disabled="saving || !ready" /></div>
        <div><label for="kilo-routing-priority" class="mb-2 block text-sm font-medium">{{ t('kilo.batch.priority') }}</label><input id="kilo-routing-priority" v-model.number="form.priority" type="number" min="0" max="10000" required class="input w-full" :disabled="saving || !ready" /></div>
      </div>
      <p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('kilo.routing.requestHint') }}</p>
      <div><label for="kilo-routing-statuses" class="mb-2 block text-sm font-medium">{{ t('kilo.routing.autoReleaseStatuses') }}</label><input id="kilo-routing-statuses" v-model="form.statuses" type="text" class="input w-full" :disabled="saving || !editable" placeholder="401, 402, 403" /><p class="mt-2 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('kilo.routing.autoReleaseHint') }}</p></div>
      <p v-if="validation" class="text-sm text-amber-700 dark:text-amber-300" role="status">{{ validation }} <button v-if="choicesError" type="button" class="underline" :disabled="saving || choicesLoading" @click="emit('reloadChoices')">{{ t('common.refresh') }}</button></p>
      <p v-if="error" class="text-sm text-red-600 dark:text-red-400" role="alert">{{ error }}</p>
    </form>
    <template #footer><button type="button" class="btn btn-secondary" :disabled="saving" @click="emit('close')">{{ t('common.cancel') }}</button><button type="submit" form="kilo-routing-form" class="btn btn-primary" :disabled="saving || !!validation || ready && choicesLoading">{{ saving ? t('common.saving') : t('common.save') }}</button></template>
  </BaseDialog>
</template>
