<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Toggle from '@/components/common/Toggle.vue'
import KiloProxySelect from './KiloProxySelect.vue'
import KiloStatusBadge from './KiloStatusBadge.vue'
import * as api from '@/api/admin/kilo'
import type { KiloDeviceStatus } from '@/api/admin/kilo'

const props = defineProps<{ show: boolean; encryptionReady: boolean }>()
const emit = defineEmits<{ close: []; authorized: [] }>()
const { t, locale } = useI18n()
const name = shallowRef('')
const proxyId = shallowRef<number | null>(null)
const session = shallowRef<KiloDeviceStatus | null>(null)
const busy = shallowRef(false)
const polling = shallowRef(false)
const autoPolling = shallowRef(true)
const error = shallowRef('')
let timer: ReturnType<typeof setTimeout> | undefined
let controller: AbortController | undefined
let pollController: AbortController | undefined
let generation = 0
const verificationUrl = computed(() => api.kiloVerificationUrl(session.value?.verification_url))
const expiresAt = computed(() => session.value?.expires_at ? new Date(session.value.expires_at).toLocaleString(locale.value) : '')
const pending = computed(() => session.value?.status === 'pending')
function stop() {
  if (timer) clearTimeout(timer)
  timer = undefined
  pollController?.abort()
}
function schedule() {
  if (timer) clearTimeout(timer)
  if (props.show && pending.value && autoPolling.value && !error.value && document.visibilityState === 'visible') timer = setTimeout(() => { void poll() }, 5000)
}
function setAutoPolling(value: boolean) {
  autoPolling.value = value
  if (value) schedule()
  else stop()
}
async function poll() {
  if (!props.show || !session.value || !pending.value || polling.value || document.visibilityState !== 'visible') return
  if (timer) clearTimeout(timer)
  timer = undefined
  const currentGeneration = generation
  pollController = new AbortController()
  const signal = pollController.signal
  polling.value = true
  error.value = ''
  try {
    const result = await api.pollDevice(session.value.id, signal)
    if (currentGeneration !== generation || signal.aborted) return
    session.value = { ...session.value, ...result }
    if (result.status === 'approved') emit('authorized')
  } catch (cause) {
    if (currentGeneration === generation && !signal.aborted && !api.isKiloRequestCanceled(cause)) error.value = api.kiloErrorMessage(cause, t('kilo.errors.devicePoll'))
  } finally {
    if (currentGeneration === generation) { polling.value = false; schedule() }
  }
}
async function start() {
  if (busy.value || !name.value.trim() || !proxyId.value || !props.encryptionReady) return
  generation++
  stop()
  controller = new AbortController()
  const signal = controller.signal
  const currentGeneration = generation
  busy.value = true
  error.value = ''
  session.value = null
  try {
    const result = await api.startDevice({ name: name.value.trim(), proxy_id: proxyId.value }, signal)
    if (currentGeneration !== generation || signal.aborted) return
    session.value = result
    if (result.status === 'approved') emit('authorized')
    schedule()
  } catch (cause) {
    if (currentGeneration === generation && !signal.aborted && !api.isKiloRequestCanceled(cause)) error.value = api.kiloErrorMessage(cause, t('kilo.errors.deviceStart'))
  } finally { busy.value = false }
}
watch(() => props.show, show => {
  generation++
  stop()
  controller?.abort()
  session.value = null
  error.value = ''
  polling.value = false
  if (show) { name.value = ''; proxyId.value = null; autoPolling.value = true }
})
async function close() {
  if (busy.value) return
  generation++
  stop()
  polling.value = false
  if (session.value?.status === 'pending') {
    busy.value = true
    error.value = ''
    controller = new AbortController()
    try {
      await api.cancelDevice(session.value.id, controller.signal)
    } catch (cause) {
      if (!api.isKiloRequestCanceled(cause)) error.value = api.kiloErrorMessage(cause, t('kilo.errors.deviceCancel'))
      return
    } finally { busy.value = false }
  }
  emit('close')
}
function visibilityChanged() {
  if (document.visibilityState === 'hidden') stop()
  else schedule()
}
onMounted(() => document.addEventListener('visibilitychange', visibilityChanged))
onBeforeUnmount(() => {
  generation++
  stop()
  controller?.abort()
  document.removeEventListener('visibilitychange', visibilityChanged)
  if (session.value?.status === 'pending') void api.cancelDevice(session.value.id).catch(() => undefined)
})
</script>

<template>
  <BaseDialog :show="show" :title="t('kilo.device.title')" width="normal" :close-on-escape="!busy" :show-close-button="!busy" @close="close">
    <form id="kilo-device-form" class="space-y-4" @submit.prevent="start">
      <p class="text-sm leading-6 text-gray-600 dark:text-dark-300">{{ t('kilo.device.description') }}</p>
      <p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('kilo.routing.loginHint') }}</p>
      <p v-if="!encryptionReady" class="text-sm text-amber-700 dark:text-amber-300">{{ t('kilo.encryptionRequired') }}</p>
      <template v-if="!session">
        <div><label for="kilo-device-name" class="mb-2 block text-sm font-medium">{{ t('common.name') }}</label><input id="kilo-device-name" v-model="name" class="input w-full" maxlength="200" required :disabled="busy" :placeholder="t('kilo.device.namePlaceholder')" autocomplete="off" /></div>
        <div><label for="kilo-device-proxy" class="mb-2 block text-sm font-medium">{{ t('kilo.routing.loginProxy') }}</label><KiloProxySelect v-if="show" id="kilo-device-proxy" v-model="proxyId" :disabled="busy" /><p class="mt-2 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('kilo.device.proxyHint') }}</p></div>
      </template>
      <div v-else class="space-y-4 rounded-xl border border-gray-200 p-4 dark:border-dark-700" aria-live="polite">
        <div class="flex flex-wrap items-center justify-between gap-2"><span class="text-sm font-medium">{{ name }}</span><KiloStatusBadge :status="session.status" /></div>
        <template v-if="pending">
          <p class="text-sm leading-6 text-gray-600 dark:text-dark-300">{{ t('kilo.device.instructions') }}</p>
          <div><span class="text-xs text-gray-500 dark:text-dark-400">{{ t('kilo.device.userCode') }}</span><p class="mt-1 select-all break-all rounded-lg bg-gray-50 p-3 font-mono text-xl tracking-wider dark:bg-dark-900" translate="no">{{ session.user_code || t('common.notAvailable') }}</p></div>
          <a v-if="verificationUrl" :href="verificationUrl" target="_blank" rel="noopener noreferrer" class="btn btn-primary inline-flex">{{ t('kilo.device.open') }}</a>
          <p v-else class="text-sm text-red-600 dark:text-red-400">{{ t('kilo.device.invalidUrl') }}</p>
          <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('kilo.device.expires', { time: expiresAt }) }}</p>
          <div class="flex flex-wrap items-center justify-between gap-3"><button type="button" class="btn btn-secondary btn-sm" :disabled="polling || busy" @click="poll">{{ polling ? t('common.verifying') : t('kilo.device.check') }}</button><label class="flex items-center gap-2 text-xs text-gray-500 dark:text-dark-400"><Toggle :model-value="autoPolling" :aria-label="t('kilo.device.autoCheck')" :disabled="busy" @update:model-value="setAutoPolling" /><span>{{ t('kilo.device.autoCheck') }}</span></label></div>
          <p v-if="!autoPolling" class="text-xs text-gray-500 dark:text-dark-400">{{ t('kilo.device.paused') }}</p>
        </template>
        <p v-else-if="session.status === 'approved'" class="text-sm leading-6 text-emerald-700 dark:text-emerald-300">{{ t('kilo.device.approved') }}</p>
        <p v-else class="text-sm text-gray-600 dark:text-dark-300">{{ t('kilo.device.restartHint') }}</p>
      </div>
      <p v-if="session && pending" class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('kilo.device.closeHint') }}</p>
      <p v-if="error" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300" role="alert">{{ error }}</p>
    </form>
    <template #footer><button type="button" class="btn btn-secondary" :disabled="busy" @click="close">{{ busy ? t('common.processing') : t('common.close') }}</button><button v-if="!session" type="submit" form="kilo-device-form" class="btn btn-primary" :disabled="busy || !encryptionReady || !name.trim() || !proxyId">{{ busy ? t('common.submitting') : t('kilo.device.start') }}</button><button v-else-if="session.status !== 'approved' && !pending" type="button" class="btn btn-primary" :disabled="busy" @click="session = null">{{ t('kilo.device.restart') }}</button></template>
  </BaseDialog>
</template>
