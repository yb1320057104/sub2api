<template>
  <AppLayout>
    <div class="mx-auto max-w-[1440px] space-y-6">
      <header class="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 class="text-2xl font-semibold tracking-tight text-gray-900 dark:text-white">{{ t('intelligenceTest.title') }}</h1>
          <p class="mt-2 text-sm leading-6 text-gray-500 dark:text-dark-400">{{ t('intelligenceTest.description') }}</p>
        </div>
      </header>


      <p v-if="auth.isAdmin && status.loaded && !status.enabled" class="rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 text-sm leading-6 text-amber-800 dark:border-amber-900 dark:bg-amber-950/30 dark:text-amber-300">{{ t('intelligenceTest.disabledHint') }}</p>

      <section class="space-y-5">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div class="inline-flex rounded-2xl bg-primary-100/60 p-1 dark:bg-dark-800" role="tablist" :aria-label="t('intelligenceTest.modeLabel')">
            <button v-for="item in modes" :id="`intelligence-mode-${item}`" :key="item" type="button" role="tab" :aria-selected="mode === item" aria-controls="intelligence-results" :tabindex="mode === item ? 0 : -1" class="rounded-xl px-7 py-2.5 text-sm font-semibold transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500 motion-reduce:transition-none" :class="mode === item ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-300' : 'text-gray-500 hover:text-primary-600 dark:text-dark-400'" @click="mode = item" @keydown="moveMode($event)">{{ t(`intelligenceTest.${item}`) }}</button>
          </div>
          <p class="flex items-center gap-2 text-xs text-gray-400 dark:text-dark-500"><span class="h-1.5 w-1.5 rounded-full" :class="status.enabled ? 'bg-emerald-400' : 'bg-gray-300'" />{{ t(status.enabled ? 'intelligenceTest.autoRefresh' : 'intelligenceTest.disabled') }}</p>
        </div>

        <div v-if="resultGroups.length" class="flex gap-2 overflow-x-auto border-b border-gray-200 pb-3 dark:border-dark-700" role="tablist" :aria-label="t('intelligenceTest.groupLabel')">
          <button v-for="group in resultGroups" :id="`intelligence-group-${group.id}`" :key="group.id" type="button" role="tab" :aria-selected="selectedGroup === group.id" aria-controls="intelligence-results" :tabindex="selectedGroup === group.id ? 0 : -1" class="shrink-0 rounded-full border px-4 py-2 text-sm transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500 motion-reduce:transition-none" :class="selectedGroup === group.id ? 'border-primary-200 bg-white font-medium text-primary-700 shadow-sm dark:border-primary-800 dark:bg-dark-800 dark:text-primary-300' : 'border-transparent text-gray-500 hover:bg-white/60 dark:text-dark-400 dark:hover:bg-dark-800'" @click="selectedGroup = group.id" @keydown="moveGroup($event)">{{ group.name }}</button>
        </div>

        <div id="intelligence-results" role="tabpanel" :aria-labelledby="`intelligence-mode-${mode}${selectedGroup ? ` intelligence-group-${selectedGroup}` : ''}`" :aria-busy="loading">
          <div v-if="readError" class="mb-4 flex flex-wrap items-center justify-between gap-3 rounded-xl border border-red-200 bg-red-50 px-4 py-3 dark:border-red-900 dark:bg-red-950/20">
            <p class="text-sm text-red-700 dark:text-red-400" role="alert">{{ t('intelligenceTest.loadFailed') }}</p>
            <button type="button" class="text-sm font-medium text-red-700 underline underline-offset-4 dark:text-red-400" @click="reload">{{ t('intelligenceTest.retry') }}</button>
          </div>
          <div v-if="loading && !results.length" class="card px-6 py-16 text-center text-sm text-gray-400" role="status">{{ t('intelligenceTest.loading') }}</div>
          <div v-else-if="!resultGroups.length || !results.length" class="card px-6 py-16 text-center">
            <svg class="mx-auto mb-5 h-12 w-12 text-primary-300 dark:text-primary-700" viewBox="0 0 48 48" fill="none" aria-hidden="true"><rect x="7" y="10" width="34" height="28" rx="8" stroke="currentColor" stroke-width="2"/><path d="M15 28l6-7 6 4 6-9M16 16h1M31 32h2" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
            <h2 class="font-medium text-gray-700 dark:text-dark-200">{{ t(resultGroups.length ? 'intelligenceTest.empty' : 'intelligenceTest.noGroups') }}</h2>
            <p class="mt-2 text-sm leading-6 text-gray-400">{{ t(resultGroups.length ? 'intelligenceTest.emptyHint' : 'intelligenceTest.noGroupsHint') }}</p>
          </div>
          <template v-else>
            <div class="mb-4 flex flex-wrap items-center justify-between gap-2 text-xs text-gray-400"><span>{{ t('intelligenceTest.latest') }}</span><span v-if="mode === 'svg'">{{ t('intelligenceTest.previewHint') }}</span></div>
            <div class="grid grid-cols-1 items-start gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-6">
              <article v-for="result in results" :key="`${selectedGroup}-${mode}-${result.id}`" class="card min-w-0 overflow-hidden">
                <div class="space-y-2 border-b border-gray-100 px-3 py-3 dark:border-dark-700">
                  <span class="inline-flex items-center gap-2 rounded-full px-3 py-1 text-xs font-medium" :class="statusClass(result)">
                    <span v-if="result.status === 'running'" class="h-1.5 w-1.5 animate-pulse rounded-full bg-primary-500 motion-reduce:animate-none" />
                    <svg v-else-if="result.status === 'completed' && (mode === 'svg' || result.passed === true)" class="h-3.5 w-3.5" viewBox="0 0 16 16" fill="none" aria-hidden="true"><path d="m3 8 3 3 7-7" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" /></svg>
                    {{ resultLabel(result) }}
                  </span>
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
                <template v-if="mode === 'svg'">
                  <ResultArtifact v-if="result.status === 'completed' && result.has_artifact" :group-id="result.group_id" :result-id="result.id" />
                  <div v-else class="flex h-44 items-center justify-center bg-gray-50/60 px-3 text-center text-xs leading-5 text-gray-400 dark:bg-dark-900/40" role="status">{{ result.status === 'completed' ? t('intelligenceTest.missingArtifact') : resultLabel(result) }}</div>
                </template>
                <dl class="border-t border-gray-100 px-3 py-2 text-xs dark:border-dark-700">
                  <div class="flex flex-wrap items-center gap-x-2 gap-y-1"><dt class="text-gray-400">{{ t('intelligenceTest.duration') }}</dt><dd class="font-mono text-gray-600 dark:text-dark-300">{{ formatDuration(result) }}</dd></div>
                </dl>
                <ResultDetails
                  v-if="result.status !== 'running' && (mode === 'candy' || (auth.isAdmin && result.status === 'failed'))"
                  :group-id="result.group_id"
                  :result-id="result.id"
                  :mode="mode"
                  :status="result.status"
                  :show-error="auth.isAdmin"
                  class="min-w-0 px-3 pb-3"
                />
              </article>
            </div>
          </template>
        </div>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import ResultArtifact from '@/components/intelligenceTest/ResultArtifact.vue'
import ResultDetails from '@/components/intelligenceTest/ResultDetails.vue'
import { intelligenceTestAPI, type IntelligenceTestGroup, type IntelligenceTestMode, type IntelligenceTestResult } from '@/api/intelligenceTest'
import { useAuthStore } from '@/stores/auth'
import { useIntelligenceTestStore } from '@/stores/intelligenceTest'

const { t, locale } = useI18n()
const router = useRouter()
const auth = useAuthStore()
const status = useIntelligenceTestStore()
const modes: IntelligenceTestMode[] = ['svg', 'candy']
const mode = ref<IntelligenceTestMode>('svg')
const selectedGroup = ref<number | null>(null)
const groups = ref<IntelligenceTestGroup[]>([])
const resultGroups = computed(() => groups.value.filter(group => group.selected === true))
const results = ref<IntelligenceTestResult[]>([])
const loading = ref(true)
const readError = ref(false)
let resultRequest: AbortController | undefined
let timer: ReturnType<typeof setTimeout> | undefined
let disposed = false
let groupsLoadedAt = 0
let groupsRequestID = 0
let resultsGeneration = 0

async function loadGroups() {
  const requestID = ++groupsRequestID
  try {
    const response = await intelligenceTestAPI.groups()
    if (disposed || requestID !== groupsRequestID) return
    groups.value = response
    groupsLoadedAt = Date.now()
    if (!resultGroups.value.some(group => group.id === selectedGroup.value)) {
      resultRequest?.abort()
      results.value = []
      selectedGroup.value = resultGroups.value[0]?.id ?? null
    }
    if (!selectedGroup.value) {
      results.value = []
      loading.value = false
    }
  } catch {
    if (!disposed && requestID === groupsRequestID) {
      readError.value = true
      loading.value = false
    }
  }
}

async function loadResults(clear = false) {
  resultRequest?.abort()
  if (clear) results.value = []
  const groupID = selectedGroup.value
  if (disposed) return
  if (!groupID || !resultGroups.value.some(group => group.id === groupID)) {
    results.value = []
    loading.value = false
    return
  }
  const requestedMode = mode.value
  const generation = resultsGeneration
  const controller = new AbortController()
  resultRequest = controller
  loading.value = true
  const isCurrentRequest = () =>
    !controller.signal.aborted && !disposed && generation === resultsGeneration &&
    selectedGroup.value === groupID && mode.value === requestedMode &&
    resultGroups.value.some(group => group.id === groupID)
  try {
    const response = await intelligenceTestAPI.results(groupID, requestedMode, controller.signal)
    if (!isCurrentRequest()) return
    results.value = response.slice(0, 30)
    readError.value = false
  } catch {
    if (isCurrentRequest()) readError.value = true
  } finally {
    if (isCurrentRequest()) loading.value = false
  }
}

async function reload() {
  const generation = resultsGeneration
  const enabled = await status.refresh()
  if (disposed || generation !== resultsGeneration) return
  if (!enabled && !auth.isAdmin) {
    results.value = []
    groups.value = []
    await router.replace('/dashboard')
    return
  }
  await loadGroups()
  if (disposed || generation !== resultsGeneration) return
  await loadResults()
}

async function poll() {
  const generation = resultsGeneration
  try {
    if (!document.hidden && !disposed) {
      await status.refresh()
      if (disposed || generation !== resultsGeneration) return
      if (!status.enabled && !auth.isAdmin) {
        results.value = []
        await router.replace('/dashboard')
        return
      }
      if (Date.now() - groupsLoadedAt >= 60000) await loadGroups()
      if (disposed || generation !== resultsGeneration) return
      await loadResults()
    }
  } finally {
    if (!disposed) timer = setTimeout(poll, 15000)
  }
}

function resultLabel(result: IntelligenceTestResult) {
  if (result.status === 'running') return t('intelligenceTest.running')
  if (result.status === 'failed') return t('intelligenceTest.failed')
  if (mode.value === 'svg') return t('intelligenceTest.completed')
  return t(result.passed === true ? 'intelligenceTest.passed' : result.passed === false ? 'intelligenceTest.notPassed' : 'intelligenceTest.failed')
}

function statusClass(result: IntelligenceTestResult) {
  if (result.status === 'running') return 'bg-primary-50 text-primary-600 dark:bg-primary-950/40 dark:text-primary-300'
  if (result.status === 'failed' || (mode.value === 'candy' && result.passed === undefined)) return 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-dark-300'
  if (mode.value === 'candy' && !result.passed) return 'bg-red-50 text-red-600 dark:bg-red-950/30 dark:text-red-400'
  return 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/30 dark:text-emerald-400'
}

function formatTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '—' : new Intl.DateTimeFormat(locale.value === 'zh' ? 'zh-CN' : 'en-US', {
    year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
  }).format(date)
}

function formatDuration(result: IntelligenceTestResult) {
  if (result.status === 'running' || result.duration_ms == null) return '—'
  return t('intelligenceTest.seconds', { value: (Math.max(0, result.duration_ms) / 1000).toFixed(1) })
}

function tabIndex(event: KeyboardEvent, current: number, count: number) {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key) || !count) return null
  event.preventDefault()
  if (event.key === 'Home') return 0
  if (event.key === 'End') return count - 1
  return (current + (event.key === 'ArrowRight' ? 1 : -1) + count) % count
}

function moveMode(event: KeyboardEvent) {
  const index = tabIndex(event, modes.indexOf(mode.value), modes.length)
  if (index == null) return
  mode.value = modes[index]
  void nextTick(() => document.getElementById(`intelligence-mode-${mode.value}`)?.focus())
}

function moveGroup(event: KeyboardEvent) {
  const index = tabIndex(event, resultGroups.value.findIndex(group => group.id === selectedGroup.value), resultGroups.value.length)
  if (index == null) return
  selectedGroup.value = resultGroups.value[index].id
  void nextTick(() => document.getElementById(`intelligence-group-${selectedGroup.value}`)?.focus())
}

watch([selectedGroup, mode], () => void loadResults(true))
watch(() => status.enabled, (enabled) => {
  if (!enabled && !auth.isAdmin) {
    resultRequest?.abort()
    results.value = []
    void router.replace('/dashboard')
  }
})

onMounted(async () => {
  await reload()
  if (!disposed) timer = setTimeout(poll, 15000)
})

onBeforeUnmount(() => {
  disposed = true
  resultRequest?.abort()
  if (timer) clearTimeout(timer)
})
</script>
