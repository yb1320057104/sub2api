<template>
  <section class="card overflow-hidden" :aria-label="t('intelligenceTest.settings')">
    <div v-if="loading" class="p-6 text-sm text-gray-500" role="status">{{ t('intelligenceTest.loading') }}</div>
    <div v-else-if="!config" class="p-6">
      <p class="mb-3 text-sm text-red-600" role="alert">{{ t('intelligenceTest.configLoadFailed') }}</p>
      <button type="button" class="btn btn-secondary" @click="loadConfig">{{ t('intelligenceTest.retry') }}</button>
    </div>
    <form v-else @submit.prevent="save">
      <fieldset :disabled="saving" class="min-w-0">
      <div class="flex items-start justify-between gap-5 border-b border-gray-100 bg-primary-50/50 p-5 dark:border-dark-700 dark:bg-primary-950/20 sm:p-6">
        <div>
          <h2 id="intelligence-schedule-label" class="font-semibold text-gray-900 dark:text-white">{{ t('intelligenceTest.schedule') }}</h2>
          <p class="mt-1 max-w-2xl text-sm leading-6 text-gray-500 dark:text-dark-400">{{ t('intelligenceTest.scheduleHint') }}</p>
        </div>
        <Toggle v-model="config.enabled" aria-labelledby="intelligence-schedule-label" />
      </div>

      <div class="grid divide-y divide-gray-100 dark:divide-dark-700 xl:grid-cols-2 xl:divide-x xl:divide-y-0">
        <section v-for="mode in modes" :key="mode" class="min-w-0 space-y-5 p-5 sm:p-6">
          <div class="flex items-center justify-between gap-3">
            <h3 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t(`intelligenceTest.${mode}`) }}</h3>
            <label class="flex items-center gap-2 text-sm text-gray-500 dark:text-dark-400">
              {{ t('intelligenceTest.taskEnabled') }}
              <Toggle v-model="config[mode].enabled" :aria-label="`${t(`intelligenceTest.${mode}`)} ${t('intelligenceTest.taskEnabled')}`" />
            </label>
          </div>
          <label class="block">
            <span class="input-label">{{ t('intelligenceTest.prompt') }}</span>
            <textarea v-model="config[mode].prompt" class="input min-h-[180px] resize-y text-sm leading-6" required maxlength="100000" />
          </label>
          <div class="grid gap-4 sm:grid-cols-2">
            <label class="block">
              <span class="input-label">{{ t('intelligenceTest.model') }}</span>
              <input v-model="config[mode].model" class="input font-mono text-sm" type="text" maxlength="200" required autocomplete="off" />
            </label>
            <label class="block">
              <span class="input-label">{{ t('intelligenceTest.effort') }}</span>
              <select v-model="config[mode].effort" class="input">
                <option value="">{{ t('intelligenceTest.effortDefault') }}</option>
                <option v-for="effort in efforts" :key="effort" :value="effort">{{ effort }}</option>
              </select>
            </label>
            <label class="block">
              <span class="input-label">{{ t('intelligenceTest.interval') }}</span>
              <input v-model.number="config[mode].interval_minutes" class="input" type="number" min="1" max="1440" step="1" required />
            </label>
            <label v-if="mode === 'candy'" class="block">
              <span class="input-label">{{ t('intelligenceTest.expectedAnswer') }}</span>
              <input v-model.number="config.candy.expected_answer" class="input" type="number" min="0" max="1000000" step="1" required />
            </label>
          </div>
          <details v-if="groups.length" class="rounded-xl border border-gray-100 dark:border-dark-700">
            <summary class="cursor-pointer px-4 py-3 text-sm font-medium text-gray-700 dark:text-dark-200">{{ t('intelligenceTest.overrides') }}</summary>
            <div class="space-y-4 border-t border-gray-100 px-4 py-4 dark:border-dark-700">
              <p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('intelligenceTest.overridesHint') }}</p>
              <div v-for="group in groups" :key="group.id" class="space-y-2">
                <div class="flex flex-wrap items-center gap-2 text-sm font-medium text-gray-700 dark:text-dark-200">
                  {{ group.name }}
                  <span class="font-mono text-xs font-normal text-gray-400">{{ group.platform }}</span>
                </div>
                <div class="grid gap-2 sm:grid-cols-2">
                  <input :value="getOverride(mode, group.id)?.model ?? ''" :aria-label="`${group.name} ${t('intelligenceTest.model')}`" :placeholder="config[mode].model" class="input text-sm" type="text" maxlength="200" @input="setOverride(mode, group.id, 'model', ($event.target as HTMLInputElement).value)" />
                  <select :value="getOverride(mode, group.id)?.effort ?? ''" :aria-label="`${group.name} ${t('intelligenceTest.effort')}`" class="input text-sm" @change="setOverride(mode, group.id, 'effort', ($event.target as HTMLSelectElement).value)">
                    <option value="">{{ t('intelligenceTest.inherit') }}</option>
                    <option v-for="effort in efforts" :key="effort" :value="effort">{{ effort }}</option>
                  </select>
                </div>
              </div>
            </div>
          </details>
        </section>
      </div>

      <section class="border-t border-gray-100 p-5 dark:border-dark-700 sm:p-6">
        <div class="flex flex-col items-start justify-between gap-4 lg:flex-row">
          <div class="max-w-2xl">
            <h3 class="font-semibold text-gray-900 dark:text-white">{{ t('intelligenceTest.keyManagement') }}</h3>
            <p class="mt-2 text-sm leading-6 text-gray-500 dark:text-dark-400">{{ t('intelligenceTest.keyHint') }}</p>
            <p class="mt-1 text-xs leading-5 text-gray-400 dark:text-dark-500">{{ t('intelligenceTest.keyPrivate') }}</p>
            <p id="intelligence-group-selection-hint" class="mt-2 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('intelligenceTest.groupSelectionHint') }}</p>
          </div>
          <button type="button" class="btn btn-secondary shrink-0" :disabled="!groupsLoaded || syncing || saving" @click="syncKeys">{{ t(syncing ? 'intelligenceTest.syncing' : 'intelligenceTest.syncKeys') }}</button>
        </div>
        <div v-if="syncReport" class="mt-4 space-y-2 text-sm" role="status">
          <p class="text-gray-700 dark:text-dark-200">{{ t('intelligenceTest.syncResult', syncReport) }}</p>
          <p v-for="group in failedGroups" :key="group.group_id" class="text-amber-700 dark:text-amber-400">{{ group.group_name }}：{{ group.error }}</p>
        </div>
        <div class="mt-4 flex flex-wrap items-center gap-3 text-xs">
          <span class="text-gray-500 dark:text-dark-400" aria-live="polite">{{ t('intelligenceTest.selectedGroups', { count: selectedVisibleGroupIDs.length, total: groups.length }) }}</span>
          <button type="button" class="font-medium text-primary-600 hover:underline disabled:opacity-50 dark:text-primary-400" :disabled="syncing || saving || !groups.length" @click="selectAllGroups">{{ t('intelligenceTest.selectAllGroups') }}</button>
          <button type="button" class="font-medium text-gray-500 hover:underline disabled:opacity-50 dark:text-dark-400" :disabled="syncing || saving || !selectedVisibleGroupIDs.length" @click="clearGroupSelection">{{ t('intelligenceTest.clearGroupSelection') }}</button>
        </div>
        <div v-if="groups.length" class="mt-3 flex flex-wrap gap-2" role="group" :aria-label="t('intelligenceTest.groupLabel')" aria-describedby="intelligence-group-selection-hint">
          <label v-for="group in groups" :key="group.id" class="inline-flex cursor-pointer items-center gap-2 rounded-lg border px-3 py-2 text-xs" :class="selectedGroupIDs.includes(group.id) ? 'border-primary-200 bg-primary-50 dark:border-primary-800 dark:bg-primary-950/30' : 'border-transparent bg-gray-50 dark:bg-dark-800'">
            <input type="checkbox" class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500 disabled:cursor-not-allowed dark:border-dark-600" :checked="selectedGroupIDs.includes(group.id)" :disabled="syncing || saving" @change="toggleGroupSelection(group.id, ($event.target as HTMLInputElement).checked)" />
            <span class="text-gray-700 dark:text-dark-200">{{ group.name }}</span>
            <span class="h-1.5 w-1.5 rounded-full" :class="group.key_configured ? 'bg-emerald-500' : 'bg-amber-400'" aria-hidden="true" />
            <span class="text-gray-400">{{ t(group.key_configured ? 'intelligenceTest.keyReady' : 'intelligenceTest.keyMissing') }}</span>
          </label>
        </div>
      </section>
      <div class="flex flex-col items-start justify-between gap-3 border-t border-gray-100 px-5 py-4 dark:border-dark-700 sm:flex-row sm:items-center sm:px-6">
        <p class="text-sm text-red-600 dark:text-red-400" role="alert">{{ error }}</p>
        <button type="submit" class="btn btn-primary shrink-0" :disabled="saving || syncing">{{ t(saving ? 'intelligenceTest.saving' : 'intelligenceTest.save') }}</button>
      </div>
      </fieldset>
    </form>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Toggle from '@/components/common/Toggle.vue'
import { intelligenceTestAPI, type IntelligenceTestConfig, type IntelligenceTestGroup, type IntelligenceTestMode } from '@/api/intelligenceTest'
import { useAppStore } from '@/stores/app'
import { useIntelligenceTestStore } from '@/stores/intelligenceTest'

const props = defineProps<{ groups: IntelligenceTestGroup[]; groupsLoaded: boolean }>()
const emit = defineEmits<{ saved: []; keysSynced: [] }>()
const { t } = useI18n()
const app = useAppStore()
const status = useIntelligenceTestStore()
const modes: IntelligenceTestMode[] = ['svg', 'candy']
const efforts = ['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max', 'ultra', 'persistent']
const config = ref<IntelligenceTestConfig | null>(null)
const loading = ref(true)
const saving = ref(false)
const syncing = ref(false)
const error = ref('')
const syncReport = ref<Awaited<ReturnType<typeof intelligenceTestAPI.syncKeys>> | null>(null)
const failedGroups = computed(() => syncReport.value?.groups.filter(group => group.error) ?? [])
const selectedGroupIDs = ref<number[]>([])
const selectedVisibleGroupIDs = computed(() => props.groups.filter(group => selectedGroupIDs.value.includes(group.id)).map(group => group.id))
let groupSelectionInitialized = false

function alignGroupSelection(groups: IntelligenceTestGroup[]) {
  selectedGroupIDs.value = groups.filter(group => group.selected === true).map(group => group.id)
  groupSelectionInitialized = true
}

watch(() => [props.groupsLoaded, props.groups] as const, ([loaded, groups]) => {
  // Parent polling may refresh key validity, but must not overwrite a draft.
  if (loaded && !groupSelectionInitialized) alignGroupSelection(groups)
}, { immediate: true })

function toggleGroupSelection(groupID: number, selected: boolean) {
  groupSelectionInitialized = true
  selectedGroupIDs.value = selected
    ? [...new Set([...selectedGroupIDs.value, groupID])]
    : selectedGroupIDs.value.filter(id => id !== groupID)
}

function selectAllGroups() {
  groupSelectionInitialized = true
  selectedGroupIDs.value = props.groups.map(group => group.id)
}

function clearGroupSelection() {
  groupSelectionInitialized = true
  selectedGroupIDs.value = []
}

function normalized(value: IntelligenceTestConfig): IntelligenceTestConfig {
  return {
    ...value,
    svg: { ...value.svg, group_overrides: value.svg.group_overrides ?? [] },
    candy: { ...value.candy, expected_answer: value.candy.expected_answer ?? 21, group_overrides: value.candy.group_overrides ?? [] },
  }
}

async function loadConfig() {
  loading.value = true
  try {
    config.value = normalized(await intelligenceTestAPI.config())
    status.setEnabled(config.value.enabled)
  } catch {
    config.value = null
  } finally {
    loading.value = false
  }
}

function getOverride(mode: IntelligenceTestMode, groupID: number) {
  return config.value?.[mode].group_overrides.find(item => item.group_id === groupID)
}

function setOverride(mode: IntelligenceTestMode, groupID: number, field: 'model' | 'effort', value: string) {
  if (!config.value) return
  let override = getOverride(mode, groupID)
  if (!override) {
    override = { group_id: groupID, model: '', effort: '' }
    config.value[mode].group_overrides.push(override)
  }
  override[field] = value
}

async function save() {
  if (!config.value || saving.value) return
  error.value = ''
  const payload = normalized(config.value)
  for (const mode of modes) {
    const task = payload[mode]
    task.model = task.model.trim()
    task.group_overrides = task.group_overrides.map(item => ({ ...item, model: item.model.trim() })).filter(item => item.model || item.effort)
    if (!Number.isInteger(task.interval_minutes) || task.interval_minutes < 1 || task.interval_minutes > 1440 || !task.prompt.trim() || !task.model) {
      error.value = t('intelligenceTest.invalidConfig')
      return
    }
    if (new TextEncoder().encode(task.prompt).length > 100000 || !validModel(task.model) || task.group_overrides.some(item => item.model && !validModel(item.model))) {
      error.value = t('intelligenceTest.invalidLength')
      return
    }
  }
  if (typeof payload.candy.expected_answer !== 'number' || !Number.isInteger(payload.candy.expected_answer) || payload.candy.expected_answer < 0 || payload.candy.expected_answer > 1000000) {
    error.value = t('intelligenceTest.invalidAnswer')
    return
  }
  saving.value = true
  try {
    const saved = await intelligenceTestAPI.saveConfig(payload)
    config.value = normalized(saved)
    status.setEnabled(saved.enabled)
    app.showSuccess(t('intelligenceTest.saved'))
    emit('saved')
  } catch {
    error.value = t('intelligenceTest.configSaveFailed')
  } finally {
    saving.value = false
  }
}

function validModel(value: string) {
  return new TextEncoder().encode(value).length <= 200 && !Array.from(value).some(char => {
    const code = char.charCodeAt(0)
    return code < 32 || (code >= 127 && code <= 159)
  })
}

async function syncKeys() {
  if (!props.groupsLoaded || syncing.value) return
  groupSelectionInitialized = true
  syncing.value = true
  error.value = ''
  try {
    syncReport.value = await intelligenceTestAPI.syncKeys(selectedVisibleGroupIDs.value)
    emit('keysSynced')
    // Keep the submitted selection available for retry after partial failures.
    if (syncReport.value.failed === 0) {
      try {
        alignGroupSelection(await intelligenceTestAPI.groups())
      } catch {
        error.value = t('intelligenceTest.groupSelectionRefreshFailed')
      }
    }
  } catch {
    error.value = t('intelligenceTest.syncFailed')
  } finally {
    syncing.value = false
  }
}

onMounted(loadConfig)
</script>
