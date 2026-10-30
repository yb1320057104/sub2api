<template>
  <section v-if="visible" id="home-intelligence-test" class="scroll-mt-8" tabindex="-1" aria-labelledby="home-intelligence-title">
    <header class="mb-7 flex flex-wrap items-end justify-between gap-5">
      <div>
        <p class="mb-2 text-xs font-semibold tracking-widest text-primary-600 dark:text-primary-400">{{ t('intelligenceTest.homeEyebrow') }}</p>
        <h2 id="home-intelligence-title" class="text-2xl font-semibold tracking-tight text-gray-900 sm:text-3xl dark:text-white">{{ t('intelligenceTest.title') }}</h2>
        <p class="mt-3 max-w-2xl text-sm leading-6 text-gray-500 dark:text-dark-400">{{ t('intelligenceTest.homeDescription') }}</p>
      </div>
      <div class="inline-flex max-w-full rounded-2xl bg-primary-100/60 p-1 dark:bg-dark-800" role="tablist" :aria-label="t('intelligenceTest.modeLabel')">
        <button
          v-for="item in modes"
          :id="`home-intelligence-mode-${item}`"
          :key="item"
          type="button"
          role="tab"
          :aria-selected="mode === item"
          aria-controls="home-intelligence-results"
          :tabindex="mode === item ? 0 : -1"
          class="rounded-xl px-5 py-2.5 text-sm font-semibold transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500 motion-reduce:transition-none"
          :class="mode === item ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-300' : 'text-gray-500 hover:text-primary-600 dark:text-dark-400'"
          @click="selectMode(item)"
          @keydown="moveMode($event)"
        >{{ t(`intelligenceTest.${item}`) }}</button>
      </div>
    </header>

    <div class="mb-5 flex gap-2 overflow-x-auto border-b border-primary-100 pb-3 dark:border-dark-700" role="tablist" :aria-label="t('intelligenceTest.groupLabel')">
      <button
        v-for="group in resultGroups"
        :id="`home-intelligence-group-${group.id}`"
        :key="group.id"
        type="button"
        role="tab"
        :aria-selected="selectedGroup === group.id"
        aria-controls="home-intelligence-results"
        :tabindex="selectedGroup === group.id ? 0 : -1"
        class="max-w-xs shrink-0 break-words rounded-full border px-4 py-2 text-left text-sm transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500 motion-reduce:transition-none"
        :class="selectedGroup === group.id ? 'border-primary-200 bg-white font-medium text-primary-700 shadow-sm dark:border-primary-800 dark:bg-dark-800 dark:text-primary-300' : 'border-transparent text-gray-500 hover:bg-primary-50 dark:text-dark-400 dark:hover:bg-dark-800'"
        @click="selectGroup(group.id)"
        @keydown="moveGroup($event)"
      >{{ group.name }}</button>
    </div>

    <div id="home-intelligence-results" role="tabpanel" :aria-labelledby="`home-intelligence-mode-${mode} home-intelligence-group-${selectedGroup}`" :aria-busy="loading">
      <p v-if="loading && !results.length" class="py-8 text-center text-sm text-gray-400" role="status">{{ t('intelligenceTest.loading') }}</p>
      <template v-else>
        <div class="mb-4 flex flex-wrap items-center justify-between gap-2 text-xs text-gray-400 dark:text-dark-400">
          <span>{{ t('intelligenceTest.homeLatest', { count: results.length }) }}</span>
          <span class="inline-flex items-center gap-2"><span class="h-1.5 w-1.5 rounded-full bg-emerald-500" aria-hidden="true" />{{ t('intelligenceTest.autoRefresh') }}</span>
        </div>
        <div class="grid grid-cols-1 items-start gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-6">
          <article v-for="result in results" :key="`${result.group_id}-${result.mode}-${result.id}`" class="min-w-0 overflow-hidden rounded-2xl border border-primary-100 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800">
            <div class="space-y-2 border-b border-gray-100 px-3 py-3 dark:border-dark-700">
              <span class="inline-flex rounded-full px-2.5 py-1 text-xs font-medium" :class="statusClass(result)">{{ resultLabel(result) }}</span>
              <time :datetime="result.started_at" :title="t('intelligenceTest.startedAt')" class="block break-words text-xs leading-5 tabular-nums text-gray-400">{{ formatTime(result.started_at) }}</time>
              <dl class="space-y-1 text-xs leading-5">
                <div>
                  <dt class="mr-1 inline text-gray-400">{{ t('intelligenceTest.model') }}</dt>
                  <dd class="inline break-all font-mono text-gray-600 dark:text-dark-300">{{ result.model ?? t('intelligenceTest.notRecorded') }}</dd>
                </div>
                <div>
                  <dt class="mr-1 inline text-gray-400">{{ t('intelligenceTest.effort') }}</dt>
                  <dd class="inline break-all text-gray-600 dark:text-dark-300">{{ result.effort == null ? t('intelligenceTest.notRecorded') : result.effort === '' ? t('intelligenceTest.effortDefault') : result.effort }}</dd>
                </div>
              </dl>
            </div>
            <p v-if="result.empty_pool" class="px-3 py-5 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('intelligenceTest.emptyPoolHint') }}</p>
            <template v-else-if="result.mode === 'svg'">
              <ResultArtifact v-if="result.has_artifact" :group-id="result.group_id" :result-id="result.id" :load-artifact="publicIntelligenceTestAPI.artifact" />
              <p v-else class="flex min-h-44 items-center justify-center px-3 text-center text-xs leading-5 text-gray-400">{{ t('intelligenceTest.missingArtifact') }}</p>
            </template>
            <dl class="border-t border-gray-100 px-3 py-3 text-xs dark:border-dark-700">
              <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
                <dt class="text-gray-400">{{ t('intelligenceTest.duration') }}</dt>
                <dd class="font-mono text-gray-600 dark:text-dark-300">{{ formatDuration(result) }}</dd>
              </div>
            </dl>
            <ResultDetails
              v-if="result.mode === 'candy' && !result.empty_pool"
              :group-id="result.group_id"
              :result-id="result.id"
              :mode="result.mode"
              :status="result.status"
              :show-error="false"
              :load-details="publicIntelligenceTestAPI.details"
              class="min-w-0 px-3 pb-3"
            />
          </article>
        </div>
      </template>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { IntelligenceTestMode, IntelligenceTestResult } from '@/api/intelligenceTest'
import { publicIntelligenceTestAPI, type PublicIntelligenceTestGroups } from '@/api/publicIntelligenceTest'
import ResultArtifact from '@/components/intelligenceTest/ResultArtifact.vue'
import ResultDetails from '@/components/intelligenceTest/ResultDetails.vue'

const emit = defineEmits<{ 'visibility-change': [visible: boolean] }>()
const { t, locale } = useI18n()
const metadata = ref<PublicIntelligenceTestGroups>({ modes: [], groups: [] })
const modes = computed(() => metadata.value.modes.filter(item => item === 'svg' || item === 'candy'))
const mode = ref<IntelligenceTestMode>('svg')
const selectedGroup = ref<number | null>(null)
const resultGroups = computed(() => metadata.value.groups.filter(group => group.modes.includes(mode.value)))
const results = ref<IntelligenceTestResult[]>([])
const loading = ref(false)
const hasShownResults = ref(false)
const visible = computed(() => modes.value.length > 0 && resultGroups.value.length > 0 && (results.value.length > 0 || (hasShownResults.value && loading.value)))
let metadataRequest: AbortController | undefined
let resultRequest: AbortController | undefined
let timer: ReturnType<typeof setTimeout> | undefined
let disposed = false

function clearResults() {
  resultRequest?.abort()
  resultRequest = undefined
  results.value = []
  loading.value = false
}

async function loadResults(clear = false) {
  resultRequest?.abort()
  if (clear) results.value = []
  const groupID = selectedGroup.value
  const requestedMode = mode.value
  if (disposed || document.hidden || groupID == null || !resultGroups.value.some(group => group.id === groupID)) {
    loading.value = false
    return
  }
  const controller = new AbortController()
  resultRequest = controller
  loading.value = true
  const isCurrent = () => !disposed && !controller.signal.aborted && resultRequest === controller && selectedGroup.value === groupID && mode.value === requestedMode
  try {
    const response = await publicIntelligenceTestAPI.results(groupID, requestedMode, controller.signal)
    if (!isCurrent()) return
    results.value = response.filter(result => result.status === 'completed' && result.group_id === groupID && result.mode === requestedMode).slice(0, 30)
    if (results.value.length) hasShownResults.value = true
  } catch {
    // A disabled/removed group or an unavailable public read should not leave
    // an old showcase on the homepage. The next visible-page poll can recover.
    if (isCurrent()) results.value = []
  } finally {
    if (isCurrent()) loading.value = false
    if (resultRequest === controller) resultRequest = undefined
  }
}

async function refresh() {
  if (disposed || document.hidden) return
  metadataRequest?.abort()
  const controller = new AbortController()
  metadataRequest = controller
  try {
    const response = await publicIntelligenceTestAPI.groups(controller.signal)
    if (disposed || controller.signal.aborted || metadataRequest !== controller) return
    const previousGroup = selectedGroup.value
    const previousMode = mode.value
    metadata.value = response
    if (!modes.value.includes(mode.value)) mode.value = modes.value[0] ?? 'svg'
    if (!resultGroups.value.some(group => group.id === selectedGroup.value)) selectedGroup.value = resultGroups.value[0]?.id ?? null
    if (!modes.value.length || selectedGroup.value == null) {
      clearResults()
      return
    }
    await loadResults(previousGroup !== selectedGroup.value || previousMode !== mode.value)
  } catch {
    if (!disposed && !controller.signal.aborted && metadataRequest === controller) {
      metadata.value = { modes: [], groups: [] }
      clearResults()
    }
  } finally {
    if (metadataRequest === controller) metadataRequest = undefined
  }
}

function scheduleRefresh() {
  if (timer) clearTimeout(timer)
  if (!disposed && !document.hidden) timer = setTimeout(() => { void refresh().finally(scheduleRefresh) }, 60000)
}

function handleVisibilityChange() {
  if (document.hidden) {
    if (timer) clearTimeout(timer)
    metadataRequest?.abort()
    resultRequest?.abort()
    loading.value = false
    return
  }
  void refresh().finally(scheduleRefresh)
}

function selectMode(value: IntelligenceTestMode) {
  if (mode.value === value) return
  mode.value = value
  if (!resultGroups.value.some(group => group.id === selectedGroup.value)) selectedGroup.value = resultGroups.value[0]?.id ?? null
  void loadResults(true)
}

function selectGroup(value: number) {
  if (selectedGroup.value === value) return
  selectedGroup.value = value
  void loadResults(true)
}

function tabIndex(event: KeyboardEvent, current: number, count: number) {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key) || !count) return null
  event.preventDefault()
  if (event.key === 'Home') return 0
  if (event.key === 'End') return count - 1
  return (current + (event.key === 'ArrowRight' ? 1 : -1) + count) % count
}

function moveMode(event: KeyboardEvent) {
  const index = tabIndex(event, modes.value.indexOf(mode.value), modes.value.length)
  if (index == null) return
  selectMode(modes.value[index])
  void nextTick(() => document.getElementById(`home-intelligence-mode-${mode.value}`)?.focus())
}

function moveGroup(event: KeyboardEvent) {
  const index = tabIndex(event, resultGroups.value.findIndex(group => group.id === selectedGroup.value), resultGroups.value.length)
  if (index == null) return
  selectGroup(resultGroups.value[index].id)
  void nextTick(() => document.getElementById(`home-intelligence-group-${selectedGroup.value}`)?.focus())
}

function resultLabel(result: IntelligenceTestResult) {
  if (result.empty_pool) return t('intelligenceTest.emptyPool')
  if (result.mode === 'svg') return t('intelligenceTest.completed')
  return t(result.passed === true ? 'intelligenceTest.passed' : result.passed === false ? 'intelligenceTest.notPassed' : 'intelligenceTest.notRecorded')
}

function statusClass(result: IntelligenceTestResult) {
  if (result.empty_pool) return 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-dark-300'
  if (result.mode === 'candy' && result.passed == null) return 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-dark-300'
  if (result.mode === 'candy' && !result.passed) return 'bg-red-50 text-red-600 dark:bg-red-950/30 dark:text-red-400'
  return 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/30 dark:text-emerald-400'
}

function formatTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '—' : new Intl.DateTimeFormat(locale.value === 'zh' ? 'zh-CN' : 'en-US', {
    year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
  }).format(date)
}

function formatDuration(result: IntelligenceTestResult) {
  return result.duration_ms == null ? '—' : t('intelligenceTest.seconds', { value: (Math.max(0, result.duration_ms) / 1000).toFixed(1) })
}

watch(visible, value => emit('visibility-change', value), { immediate: true })

onMounted(() => {
  document.addEventListener('visibilitychange', handleVisibilityChange)
  void refresh().finally(scheduleRefresh)
})

onBeforeUnmount(() => {
  disposed = true
  metadataRequest?.abort()
  resultRequest?.abort()
  if (timer) clearTimeout(timer)
  document.removeEventListener('visibilitychange', handleVisibilityChange)
  emit('visibility-change', false)
})
</script>
