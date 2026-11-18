<script setup lang="ts">
import { onBeforeUnmount, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import KiloProxySelect from './KiloProxySelect.vue'
import KiloStatusBadge from './KiloStatusBadge.vue'
import * as api from '@/api/admin/kilo'
import type { KiloEgress, KiloPrepared } from '@/api/admin/kilo'

const props = defineProps<{ prepared: KiloPrepared | null }>()
const emit = defineEmits<{ close: [] }>()
const { t, locale } = useI18n()
const items = shallowRef<KiloEgress[]>([])
const proxyId = shallowRef<number | null>(null)
const loading = shallowRef(false)
const busy = shallowRef(false)
const error = shallowRef('')
const notice = shallowRef('')
const confirmReleaseId = shallowRef<number | null>(null)
let controller: AbortController | undefined
async function load() {
  const accountId = props.prepared?.account_id
  if (!accountId || loading.value) return
  controller = new AbortController()
  const signal = controller.signal
  loading.value = true
  error.value = ''
  try {
    const result = await api.getEgress(accountId, signal)
    if (!signal.aborted) items.value = result
  } catch (cause) {
    if (!signal.aborted && !api.isKiloRequestCanceled(cause)) error.value = api.kiloErrorMessage(cause, t('kilo.errors.egressLoad'))
  } finally { loading.value = false }
}
watch(() => props.prepared, item => {
  controller?.abort(); items.value = []; proxyId.value = null; confirmReleaseId.value = null; error.value = ''; notice.value = ''
  if (item?.account_id) void load()
}, { immediate: true })
async function add() {
  if (!props.prepared?.account_id || !proxyId.value || busy.value || loading.value) return
  busy.value = true
  error.value = ''; notice.value = ''
  controller = new AbortController()
  try {
    await api.addEgress(props.prepared.account_id, proxyId.value, controller.signal)
    proxyId.value = null
    notice.value = t('kilo.egress.added')
    await load()
  } catch (cause) {
    if (!api.isKiloRequestCanceled(cause)) error.value = api.kiloErrorMessage(cause, t('kilo.errors.egressAdd'))
  } finally { busy.value = false }
}
async function retryVerification(item: KiloEgress) {
  const accountId = props.prepared?.account_id
  if (!accountId || busy.value || loading.value) return
  busy.value = true
  error.value = ''; notice.value = ''
  controller = new AbortController()
  try {
    await api.addEgress(accountId, item.proxy_id, controller.signal)
    await load()
  } catch (cause) {
    if (!api.isKiloRequestCanceled(cause)) {
      const message = api.kiloErrorMessage(cause, t('kilo.errors.egressAdd'))
      await load()
      error.value = message
    }
  } finally { busy.value = false }
}
async function release() {
  if (!confirmReleaseId.value || busy.value || loading.value) return
  busy.value = true
  error.value = ''; notice.value = ''
  controller = new AbortController()
  try {
    await api.releaseEgress(confirmReleaseId.value, controller.signal)
    confirmReleaseId.value = null
    notice.value = t('kilo.egress.released')
    await load()
  } catch (cause) {
    if (!api.isKiloRequestCanceled(cause)) {
      const message = api.kiloErrorMessage(cause, t('kilo.errors.egressRelease'))
      await load()
      error.value = message
    }
  } finally { busy.value = false }
}
function formatTime(value: string | null) { return value ? new Date(value).toLocaleString(locale.value) : t('common.notAvailable') }
onBeforeUnmount(() => controller?.abort())
</script>

<template>
  <BaseDialog :show="!!prepared" :title="t('kilo.egress.title', { name: prepared?.name ?? '' })" width="wide" :show-close-button="!busy" :close-on-escape="!busy" @close="!busy && emit('close')">
    <div class="space-y-4">
      <p class="rounded-lg bg-primary-50 p-3 text-sm leading-6 text-primary-800 dark:bg-primary-900/20 dark:text-primary-200">{{ t('kilo.egressRule') }} {{ t('kilo.egress.description') }}</p>
      <form class="flex flex-col gap-3 sm:flex-row sm:items-end" @submit.prevent="add"><div class="min-w-0 flex-1"><label for="kilo-add-proxy" class="mb-2 block text-sm font-medium">{{ t('kilo.routing.requestProxy') }}</label><KiloProxySelect v-if="prepared" :key="prepared.account_id ?? prepared.id" id="kilo-add-proxy" v-model="proxyId" :account-id="prepared.account_id" :disabled="busy || loading" /></div><button type="submit" class="btn btn-primary shrink-0" :disabled="!proxyId || busy || loading">{{ busy ? t('common.processing') : t('kilo.egress.add') }}</button></form>
      <div class="flex items-center justify-between gap-3"><h3 class="text-sm font-medium">{{ t('kilo.egress.assigned') }}</h3><button type="button" class="btn btn-secondary btn-sm" :disabled="busy || loading" @click="load">{{ loading ? t('common.loading') : t('common.refresh') }}</button></div>
      <p v-if="error" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300" role="alert">{{ error }}</p>
      <p v-if="notice" class="text-sm text-emerald-700 dark:text-emerald-300" role="status">{{ notice }}</p>
      <p v-if="!items.length" class="py-6 text-center text-sm text-gray-500 dark:text-dark-400">{{ loading ? t('common.loading') : t('kilo.egress.empty') }}</p>
      <ul class="space-y-3"><li v-for="item in items" :key="item.id" class="rounded-lg border border-gray-200 p-3 dark:border-dark-700"><div class="flex flex-wrap items-start justify-between gap-3"><div class="min-w-0 space-y-1"><p class="break-all font-mono text-sm">{{ item.egress_ip || t('kilo.egress.unverified') }}</p><p class="text-xs text-gray-500 dark:text-dark-400">{{ t('kilo.egress.proxyId', { id: item.proxy_id }) }} · {{ t('kilo.egress.verifiedAt', { time: formatTime(item.verified_at) }) }}</p></div><KiloStatusBadge :status="item.state" /></div><div class="mt-3 flex flex-wrap items-center justify-between gap-3"><p class="text-xs text-gray-600 dark:text-dark-300">{{ t('kilo.egress.capacity', { active: item.in_flight, max: item.max_concurrency }) }}</p><button v-if="['failed', 'unverified'].includes(item.state)" type="button" class="btn btn-secondary btn-sm" :disabled="busy || loading" @click="retryVerification(item)">{{ t('kilo.egress.retry') }}</button><span v-if="item.proxy_id === prepared?.login_proxy_id" class="text-xs text-primary-700 dark:text-primary-300">{{ t('kilo.routing.fixedLogin') }}</span><button v-if="item.state !== 'released' && item.proxy_id !== prepared?.login_proxy_id" type="button" class="btn btn-secondary btn-sm text-red-600 dark:text-red-400" :disabled="busy || loading" @click="confirmReleaseId = item.id">{{ item.state === 'draining' ? t('kilo.egress.finishRelease') : t('kilo.egress.release') }}</button></div><p v-if="item.last_error" class="mt-2 break-words text-xs text-red-600 dark:text-red-400">{{ item.last_error }}</p></li></ul>
      <div v-if="confirmReleaseId" class="space-y-3 rounded-lg border border-amber-200 bg-amber-50 p-3 dark:border-amber-800 dark:bg-amber-900/20" role="alert"><p class="text-sm leading-6 text-amber-800 dark:text-amber-300">{{ t('kilo.egress.releaseConfirmation') }}</p><div class="flex gap-2"><button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="confirmReleaseId = null">{{ t('common.cancel') }}</button><button type="button" class="btn btn-primary btn-sm" :disabled="busy" @click="release">{{ busy ? t('common.processing') : t('kilo.egress.confirmRelease') }}</button></div></div>
    </div>
    <template #footer><button type="button" class="btn btn-secondary" :disabled="busy" @click="emit('close')">{{ t('common.close') }}</button></template>
  </BaseDialog>
</template>
