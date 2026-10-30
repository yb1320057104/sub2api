<template>
  <BaseDialog
    :show="show"
    :title="t('accountCandy.settings')"
    width="wide"
    :show-close-button="!saving"
    :close-on-escape="!saving"
    @close="close"
  >
    <div v-if="loading" class="py-8 text-center text-sm text-gray-500">{{ t('accountCandy.loading') }}</div>
    <div v-else-if="!config" class="space-y-3 py-5 text-center">
      <p class="text-sm text-red-600 dark:text-red-400" role="alert">{{ error }}</p>
      <button type="button" class="btn btn-secondary" @click="load">{{ t('accountCandy.retry') }}</button>
    </div>
    <form v-else id="account-candy-settings" class="space-y-5" @submit.prevent="save">
      <fieldset :disabled="saving" class="space-y-5">
        <div class="rounded-xl bg-primary-50 p-4 dark:bg-primary-950/30">
          <p class="text-sm font-medium text-gray-900 dark:text-white">{{ t('accountCandy.description') }}</p>
          <p class="mt-2 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('accountCandy.supported') }}</p>
        </div>
        <label class="flex items-center justify-between gap-4 text-sm font-medium text-gray-800 dark:text-dark-100">
          <span>{{ t('accountCandy.enabled') }}</span>
          <Toggle v-model="config.enabled" :aria-label="t('accountCandy.enabled')" />
        </label>
        <div class="grid gap-4 sm:grid-cols-2">
          <label class="block">
            <span class="input-label">{{ t('accountCandy.interval') }}</span>
            <input v-model.number="config.interval_minutes" class="input" type="number" min="1" max="1440" step="1" required />
          </label>
          <label class="block">
            <span class="input-label">{{ t('accountCandy.targetGroup') }}</span>
            <Select v-model="config.target_group_id" :options="targetOptions" :disabled="saving" searchable />
          </label>
        </div>
        <section class="space-y-3">
          <div class="flex items-center justify-between gap-3">
            <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('radar.models') }} <span class="font-mono text-gray-400">{{ config.models.length }}/8</span></h3>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="saving || config.models.length >= 8" @click="config.models.push({ model: '', effort: '', enabled: true })">{{ t('radar.addModel') }}</button>
          </div>
          <div v-for="(model, index) in config.models" :key="index" class="grid items-end gap-3 rounded-xl border border-gray-200 p-3 dark:border-dark-700 sm:grid-cols-[1fr_160px_auto]">
            <label class="min-w-0"><span class="input-label">{{ t('accountCandy.model') }} {{ index + 1 }}</span><Select v-model="model.model" :options="modelOptions" :disabled="saving" searchable creatable :aria-label="t('accountCandy.model')" /></label>
            <label><span class="input-label">{{ t('accountCandy.effort') }}</span><Select v-model="model.effort" :options="effortOptions" :disabled="saving" /></label>
            <div class="flex items-center gap-3 pb-1"><Toggle v-model="model.enabled" :disabled="saving" :aria-label="t('radar.modelEnabled')" /><button type="button" class="rounded-lg p-2 text-gray-500 hover:bg-red-50 hover:text-red-600 disabled:opacity-30" :disabled="saving || config.models.length === 1" :aria-label="t('radar.removeModel')" @click="config.models.splice(index, 1)">×</button></div>
          </div>
          <p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('radar.modelRule') }}</p>
        </section>
        <div class="space-y-2 border-t border-gray-100 pt-4 text-xs leading-5 text-gray-500 dark:border-dark-700 dark:text-dark-400">
          <p>{{ t('accountCandy.targetHint') }}</p>
          <p>{{ t('accountCandy.noTargetHint') }}</p>
        </div>
        <p v-if="error" class="text-sm text-red-600 dark:text-red-400" role="alert">{{ error }}</p>
      </fieldset>
    </form>
    <template #footer>
      <button type="button" class="btn btn-secondary" :disabled="saving" @click="close">{{ t('common.cancel') }}</button>
      <button v-if="config && !loading" type="submit" form="account-candy-settings" class="btn btn-primary" :disabled="saving">{{ t(saving ? 'accountCandy.saving' : 'accountCandy.save') }}</button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Toggle from '@/components/common/Toggle.vue'
import Select from '@/components/common/Select.vue'
import { apiClient } from '@/api/client'
import { accountCandyAPI, type AccountCandyConfig } from '@/api/admin/accountCandy'
import { extractApiErrorMessage } from '@/utils/apiError'
import { useAppStore } from '@/stores/app'
import type { AdminGroup } from '@/types'

const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{ close: []; saved: [] }>()
const { t } = useI18n()
const app = useAppStore()
const efforts = ['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max', 'ultra', 'persistent']
const config = ref<AccountCandyConfig | null>(null)
const groups = ref<AdminGroup[]>([])
const loading = ref(false)
const saving = ref(false)
const error = ref('')
const missingTarget = computed(() => config.value?.target_group_id != null && !groups.value.some(group => group.id === config.value?.target_group_id))
const effortOptions = computed(() => [{ value: '', label: t('accountCandy.defaultEffort') }, ...efforts.map(value => ({ value, label: value }))])
const modelOptions = computed(() => (config.value?.models ?? []).filter(item => item.model).map(item => ({ value: item.model, label: item.model })))
const targetOptions = computed(() => [
  { value: null, label: t('accountCandy.noTarget') },
  ...(missingTarget.value ? [{ value: config.value?.target_group_id ?? null, label: t('accountCandy.groupId', { id: config.value?.target_group_id }), disabled: true }] : []),
  ...groups.value.map(group => ({ value: group.id, label: group.name })),
])
let controller: AbortController | null = null

async function load() {
  controller?.abort()
  const request = new AbortController()
  controller = request
  loading.value = true
  error.value = ''
  config.value = null
  try {
    const [settings, response] = await Promise.all([
      accountCandyAPI.config(request.signal),
      apiClient.get<AdminGroup[]>('/admin/groups/all', { params: { platform: 'openai' }, signal: request.signal }),
    ])
    if (request.signal.aborted) return
    groups.value = response.data.filter(group => group.platform === 'openai' && group.status === 'active')
    config.value = { ...settings }
  } catch (cause) {
    if (!request.signal.aborted) error.value = extractApiErrorMessage(cause, t('accountCandy.loadFailed'))
  } finally {
    if (!request.signal.aborted) loading.value = false
  }
}

async function save() {
  if (!config.value || saving.value) return
  const draft = { ...config.value, models: config.value.models.map(model => ({ ...model, model: model.model.trim() })) }
  if (!draft.models.length || draft.models.length > 8 || draft.models.some(model => !model.model || model.model.length > 200) ||
    new Set(draft.models.map(model => model.model)).size !== draft.models.length || !draft.models.some(model => model.enabled) ||
    !Number.isInteger(draft.interval_minutes) || draft.interval_minutes < 1 || draft.interval_minutes > 1440 ||
    (draft.enabled && draft.target_group_id == null) || missingTarget.value) {
    error.value = t('accountCandy.invalidConfig')
    return
  }
  saving.value = true
  error.value = ''
  try {
    config.value = await accountCandyAPI.saveConfig(draft)
    app.showSuccess(t('accountCandy.saved'))
    emit('saved')
    emit('close')
  } catch (cause) {
    error.value = extractApiErrorMessage(cause, t('accountCandy.saveFailed'))
  } finally {
    saving.value = false
  }
}

function close() {
  if (!saving.value) emit('close')
}

watch(() => props.show, (show) => {
  if (show) void load()
  else controller?.abort()
}, { immediate: true })
onUnmounted(() => controller?.abort())
</script>
