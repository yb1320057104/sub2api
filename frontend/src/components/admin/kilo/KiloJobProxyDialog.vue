<script setup lang="ts">
import { onBeforeUnmount, shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import KiloProxySelect from './KiloProxySelect.vue'
import { changeJobLoginProxy, isKiloRequestCanceled, kiloErrorMessage, type KiloJob } from '@/api/admin/kilo'

const props = defineProps<{ job: KiloJob }>()
const emit = defineEmits<{ close: []; saved: [] }>()
const { t } = useI18n()
const proxyId = shallowRef<number | null>(props.job.login_proxy_id ?? null)
const saving = shallowRef(false)
const error = shallowRef('')
let controller: AbortController | undefined

async function save() {
  if (saving.value || !proxyId.value || proxyId.value === props.job.login_proxy_id) return
  saving.value = true
  error.value = ''
  const current = new AbortController()
  controller = current
  try {
    await changeJobLoginProxy(props.job.id, proxyId.value, current.signal)
    if (!current.signal.aborted) emit('saved')
  } catch (cause) {
    if (!current.signal.aborted && !isKiloRequestCanceled(cause)) error.value = kiloErrorMessage(cause, t('kilo.errors.loginProxyChange'))
  } finally {
    if (controller === current) saving.value = false
  }
}
onBeforeUnmount(() => controller?.abort())
</script>

<template>
  <BaseDialog :show="true" :title="t('kilo.jobs.proxyTitle', { id: job.id })" width="narrow" :show-close-button="!saving" :close-on-escape="!saving" @close="!saving && emit('close')">
    <form id="kilo-job-proxy-form" class="space-y-4" @submit.prevent="save">
      <p class="text-sm leading-6 text-gray-600 dark:text-dark-300">{{ t('kilo.jobs.proxyHint') }}</p>
      <p v-if="job.login_proxy_id" class="text-xs text-gray-500 dark:text-dark-400">{{ t('kilo.jobs.currentProxy', { id: job.login_proxy_id }) }}</p>
      <div><label for="kilo-job-login-proxy" class="mb-2 block text-sm font-medium">{{ t('kilo.jobs.newProxy') }}</label><KiloProxySelect id="kilo-job-login-proxy" v-model="proxyId" :account-id="job.account_id" :disabled="saving" preflight /></div>
      <p v-if="error" class="text-sm text-red-600 dark:text-red-400" role="alert">{{ error }}</p>
    </form>
    <template #footer><button type="button" class="btn btn-secondary" :disabled="saving" @click="emit('close')">{{ t('common.cancel') }}</button><button type="submit" form="kilo-job-proxy-form" class="btn btn-primary" :disabled="saving || !proxyId || proxyId === job.login_proxy_id" data-testid="kilo-confirm-login-proxy">{{ t(saving ? 'common.saving' : 'common.save') }}</button></template>
  </BaseDialog>
</template>
