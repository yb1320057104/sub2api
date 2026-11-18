<script setup lang="ts">
import { onBeforeUnmount, shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'
import { refreshModels, isKiloRequestCanceled, kiloErrorMessage } from '@/api/admin/kilo'

const props = defineProps<{ preparedId: number; disabled: boolean }>()
const { t } = useI18n()
const busy = shallowRef(false)
const error = shallowRef('')
const modelCount = shallowRef<number | null>(null)
let controller: AbortController | undefined
async function refresh() {
  if (props.disabled || busy.value) return
  const request = new AbortController()
  controller = request
  busy.value = true
  error.value = ''
  try {
    const result = await refreshModels(props.preparedId, request.signal)
    if (!request.signal.aborted) modelCount.value = result.models.length
  } catch (cause) {
    if (!request.signal.aborted && !isKiloRequestCanceled(cause)) error.value = kiloErrorMessage(cause, t('kilo.models.failed'))
  } finally {
    if (controller === request) busy.value = false
  }
}
onBeforeUnmount(() => controller?.abort())
</script>

<template>
  <div class="min-w-0 space-y-1">
    <button type="button" class="btn btn-secondary btn-sm" :disabled="disabled || busy" :title="t('kilo.models.hint')" data-testid="kilo-refresh-models" @click="refresh">{{ busy ? t('kilo.models.loading') : t('kilo.models.action') }}</button>
    <p v-if="modelCount !== null" class="text-xs text-primary-700 dark:text-primary-300" role="status">{{ t('kilo.models.updated', { count: modelCount }) }}</p>
    <p v-if="error" class="max-w-xs break-words text-xs text-red-600 dark:text-red-400" role="alert">{{ error }}</p>
  </div>
</template>
