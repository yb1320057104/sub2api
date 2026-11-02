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
        </div>
        <section class="space-y-3">
          <div class="flex items-center justify-between gap-3">
            <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('accountCandy.groupRules') }}</h3>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="saving || config.group_rules.length >= groups.length" @click="addRule">{{ t('accountCandy.addRule') }}</button>
          </div>
          <p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('accountCandy.ruleOrderHint') }}</p>
          <p v-if="!config.group_rules.length" class="rounded-xl bg-gray-50 p-3 text-sm text-gray-500 dark:bg-dark-800 dark:text-dark-400">{{ t('accountCandy.noRules') }}</p>
          <div v-for="(rule, index) in config.group_rules" :key="index" class="space-y-3 rounded-xl border border-gray-200 p-3 dark:border-dark-700">
            <div class="flex items-center justify-between gap-3">
              <span class="text-xs font-semibold text-gray-600 dark:text-dark-300">{{ t('accountCandy.ruleNumber', { number: index + 1 }) }}</span>
              <div class="flex items-center gap-1">
                <button type="button" class="rounded-lg px-2 py-1 text-gray-500 hover:bg-gray-100 disabled:opacity-30 dark:hover:bg-dark-700" :disabled="saving || index === 0" :aria-label="t('accountCandy.moveRuleUp')" :title="t('accountCandy.moveRuleUp')" @click="moveRule(index, -1)">↑</button>
                <button type="button" class="rounded-lg px-2 py-1 text-gray-500 hover:bg-gray-100 disabled:opacity-30 dark:hover:bg-dark-700" :disabled="saving || index === config.group_rules.length - 1" :aria-label="t('accountCandy.moveRuleDown')" :title="t('accountCandy.moveRuleDown')" @click="moveRule(index, 1)">↓</button>
                <button type="button" class="rounded-lg px-2 py-1 text-gray-500 hover:bg-red-50 hover:text-red-600" :disabled="saving" :aria-label="t('accountCandy.removeRule')" :title="t('accountCandy.removeRule')" @click="config.group_rules.splice(index, 1)">×</button>
              </div>
            </div>
            <div class="grid gap-3 sm:grid-cols-3">
              <label class="min-w-0"><span class="input-label">{{ t('accountCandy.sourceGroup') }}</span><Select v-model="rule.source_group_id" :options="groupOptions(rule.source_group_id)" :disabled="saving" :aria-label="t('accountCandy.sourceGroup')" searchable /></label>
              <fieldset class="min-w-0">
                <legend class="input-label">{{ t('accountCandy.passGroup') }}</legend>
                <div class="max-h-40 space-y-2 overflow-y-auto rounded-xl border border-gray-200 p-3 dark:border-dark-700">
                  <label v-for="group in passGroupOptions(rule.pass_group_ids)" :key="group.id" class="flex cursor-pointer items-start gap-2 text-sm" :class="group.unavailable ? 'text-amber-700 dark:text-amber-300' : 'text-gray-700 dark:text-dark-200'">
                    <input v-model="rule.pass_group_ids" type="checkbox" :value="group.id" class="mt-0.5 rounded border-gray-300 text-primary-600" />
                    <span class="break-all">{{ group.name }}</span>
                  </label>
                  <p v-if="!passGroupOptions(rule.pass_group_ids).length" class="text-xs text-gray-500 dark:text-dark-400">{{ t('accountCandy.noAvailableGroups') }}</p>
                </div>
                <p class="mt-2 break-words text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('accountCandy.selectedPassGroups', { groups: passGroupSummary(rule.pass_group_ids) }) }}</p>
              </fieldset>
              <label class="min-w-0"><span class="input-label">{{ t('accountCandy.failGroup') }}</span><Select v-model="rule.fail_group_id" :options="groupOptions(rule.fail_group_id)" :disabled="saving" :aria-label="t('accountCandy.failGroup')" searchable /></label>
            </div>
          </div>
        </section>
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
          <div class="flex flex-wrap items-center justify-between gap-2 text-xs leading-5">
            <p class="text-gray-500 dark:text-dark-400">{{ t('accountCandy.modelCatalogHint') }}</p>
            <button type="button" class="font-medium text-primary-600 dark:text-primary-300" :disabled="saving || loadingModels" @click="loadModels()">{{ t(loadingModels ? 'accountCandy.loading' : 'accountCandy.refreshModels') }}</button>
          </div>
          <p v-if="modelsError" class="text-xs text-amber-700 dark:text-amber-300" role="status">{{ t('accountCandy.modelsUnavailable') }}</p>
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
const availableModels = ref<string[]>([])
const loadingModels = ref(false)
const modelsError = ref(false)
const effortOptions = computed(() => [{ value: '', label: t('accountCandy.defaultEffort') }, ...efforts.map(value => ({ value, label: value }))])
const modelOptions = computed(() => [...new Set([...availableModels.value, ...(config.value?.models ?? []).map(item => item.model).filter(Boolean)])].map(model => ({ value: model, label: model })))
const groupOptions = (selectedID: number) => [
  { value: 0, label: t('accountCandy.selectGroup') },
  ...(selectedID > 0 && !groups.value.some(group => group.id === selectedID) ? [{ value: selectedID, label: t('accountCandy.unavailableGroup', { id: selectedID }), disabled: true }] : []),
  ...groups.value.map(group => ({ value: group.id, label: group.name })),
]
const passGroupOptions = (selectedIDs: number[]) => [
  ...selectedIDs.filter(id => !groups.value.some(group => group.id === id)).map(id => ({ id, name: t('accountCandy.unavailableGroup', { id }), unavailable: true })),
  ...groups.value.map(group => ({ id: group.id, name: group.name, unavailable: false })),
]
const passGroupSummary = (ids: number[]) => ids.length
  ? ids.map(id => groups.value.find(group => group.id === id)?.name ?? t('accountCandy.unavailableGroup', { id })).join(' / ')
  : t('accountCandy.selectGroup')
let controller: AbortController | null = null

async function loadModels(signal = controller?.signal) {
  loadingModels.value = true
  modelsError.value = false
  try {
    const models = await accountCandyAPI.models(signal)
    if (!signal?.aborted) availableModels.value = models
  } catch {
    if (!signal?.aborted) modelsError.value = true
  } finally {
    if (!signal?.aborted) loadingModels.value = false
  }
}

function addRule() {
  if (!config.value) return
  const used = new Set(config.value.group_rules.map(rule => rule.source_group_id))
  const source = groups.value.find(group => !used.has(group.id))
  if (source) config.value.group_rules.push({ source_group_id: source.id, pass_group_ids: [], fail_group_id: 0 })
}

function moveRule(index: number, direction: number) {
  const rules = config.value?.group_rules
  const next = index + direction
  if (!rules || next < 0 || next >= rules.length) return
  const [rule] = rules.splice(index, 1)
  if (rule) rules.splice(next, 0, rule)
}

async function load() {
  controller?.abort()
  const request = new AbortController()
  controller = request
  loading.value = true
  error.value = ''
  config.value = null
  void loadModels(request.signal)
  try {
    const [settings, response] = await Promise.all([
      accountCandyAPI.config(request.signal),
      apiClient.get<AdminGroup[]>('/admin/groups/all', { params: { platform: 'openai' }, signal: request.signal }),
    ])
    if (request.signal.aborted) return
    groups.value = response.data.filter(group => group.platform === 'openai' && group.status === 'active')
    config.value = { ...settings, group_rules: (settings.group_rules ?? []).map(rule => ({ ...rule, pass_group_ids: [...(rule.pass_group_ids ?? [])] })) }
  } catch (cause) {
    if (!request.signal.aborted) error.value = extractApiErrorMessage(cause, t('accountCandy.loadFailed'))
  } finally {
    if (!request.signal.aborted) loading.value = false
  }
}

async function save() {
  if (!config.value || saving.value) return
  const draft = { ...config.value, group_rules: config.value.group_rules.map(rule => ({ ...rule, pass_group_ids: [...rule.pass_group_ids] })), models: config.value.models.map(model => ({ ...model, model: model.model.trim() })) }
  const availableIDs = new Set(groups.value.map(group => group.id))
  if (!draft.models.length || draft.models.length > 8 || draft.models.some(model => !model.model || model.model.length > 200) ||
    new Set(draft.models.map(model => model.model)).size !== draft.models.length || !draft.models.some(model => model.enabled) ||
    !Number.isInteger(draft.interval_minutes) || draft.interval_minutes < 1 || draft.interval_minutes > 1440 ||
    draft.group_rules.some(rule => !rule.pass_group_ids.length || new Set(rule.pass_group_ids).size !== rule.pass_group_ids.length ||
      ![rule.source_group_id, ...rule.pass_group_ids, rule.fail_group_id].every(id => Number.isInteger(id) && availableIDs.has(id))) ||
    new Set(draft.group_rules.map(rule => rule.source_group_id)).size !== draft.group_rules.length) {
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
