<script setup lang="ts">
import { onBeforeUnmount, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { isKiloRequestCanceled, kiloErrorMessage, preflightProxy, type KiloProxyPreflight } from '@/api/admin/kilo'

const props = defineProps<{ proxyId: number | null | undefined; disabled?: boolean }>()
const { t } = useI18n()
const result = shallowRef<KiloProxyPreflight | null>(null)
const checking = shallowRef(false)
const error = shallowRef('')
let controller: AbortController | undefined

watch(() => props.proxyId, () => {
  controller?.abort()
  result.value = null
  error.value = ''
  checking.value = false
})

async function check() {
  if (!props.proxyId || props.disabled || checking.value) return
  const current = new AbortController()
  controller = current
  checking.value = true
  result.value = null
  error.value = ''
  try {
    const value = await preflightProxy(props.proxyId, current.signal)
    if (!current.signal.aborted) result.value = value
  } catch (cause) {
    if (!current.signal.aborted && !isKiloRequestCanceled(cause)) error.value = kiloErrorMessage(cause, t('kilo.errors.proxyPreflight'))
  } finally {
    if (controller === current) checking.value = false
  }
}
onBeforeUnmount(() => controller?.abort())
</script>

<template>
  <div class="mt-3 space-y-2" data-testid="kilo-proxy-preflight">
    <button type="button" class="btn btn-secondary btn-sm" :disabled="disabled || !proxyId || checking" :aria-busy="checking" data-testid="kilo-proxy-check" @click="check">{{ t(checking ? 'kilo.preflight.checking' : 'kilo.preflight.action') }}</button>
    <p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('kilo.preflight.hint') }}</p>
    <p v-if="error" class="text-xs leading-5 text-red-600 dark:text-red-400" role="alert">{{ error }}</p>
    <div v-if="result" class="space-y-2 rounded-lg p-3" :class="result.reachable ? 'bg-emerald-50 dark:bg-emerald-900/20' : 'bg-red-50 dark:bg-red-900/20'" :role="result.reachable ? 'status' : 'alert'" data-testid="kilo-proxy-check-result">
      <p class="text-sm font-medium" :class="result.reachable ? 'text-emerald-700 dark:text-emerald-300' : 'text-red-700 dark:text-red-300'">{{ t(result.reachable ? 'kilo.preflight.available' : 'kilo.preflight.unavailable') }}</p>
      <ul class="space-y-2 text-xs leading-5">
        <li v-for="item in result.checks" :key="item.target" class="break-words">
          <span class="font-medium">{{ t(`kilo.preflight.target.${item.target}`) }}</span>
          <span :class="item.reachable ? 'text-emerald-700 dark:text-emerald-300' : 'text-red-700 dark:text-red-300'"> · {{ t(item.reachable ? 'kilo.preflight.passed' : 'kilo.preflight.failed') }}</span>
          <span class="text-gray-500 dark:text-dark-400"> · {{ t('kilo.preflight.latency', { ms: item.latency_ms }) }}<template v-if="item.status"> · {{ t('kilo.preflight.httpStatus', { status: item.status }) }}</template></span>
          <span v-if="item.challenge" class="block text-amber-700 dark:text-amber-300">{{ t('kilo.preflight.challenge') }}</span>
          <span v-if="item.error" class="block text-red-600 dark:text-red-400">{{ item.error }}</span>
        </li>
      </ul>
    </div>
  </div>
</template>
