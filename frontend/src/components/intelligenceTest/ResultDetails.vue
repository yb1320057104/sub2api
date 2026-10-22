<template>
  <section ref="container" class="border-t border-gray-100 pt-3 dark:border-dark-700" :aria-label="t(mode === 'candy' ? 'intelligenceTest.candyDetails' : 'intelligenceTest.failureReason')">
    <div v-if="readFailed" class="flex flex-wrap items-center justify-between gap-3 text-xs">
      <p class="text-red-600 dark:text-red-400" role="alert">{{ t('intelligenceTest.detailsLoadFailed') }}</p>
      <button type="button" class="font-medium text-primary-600 underline underline-offset-4 dark:text-primary-400" @click="load">{{ t('intelligenceTest.retry') }}</button>
    </div>
    <p v-else-if="!details" class="text-xs text-gray-400" role="status">{{ t(status === 'running' ? 'intelligenceTest.running' : 'intelligenceTest.detailsLoading') }}</p>
    <template v-else>
      <template v-if="mode === 'candy'">
        <p v-if="details.error_code === 'historical_details_unavailable' && !details.prompt && !details.answer" class="text-xs leading-5 text-gray-400">{{ t('intelligenceTest.historicalDetailsUnavailable') }}</p>
        <dl v-else class="space-y-3">
          <div>
            <dt class="mb-2 text-xs font-medium text-gray-500 dark:text-dark-400">{{ t('intelligenceTest.sentPrompt') }}</dt>
            <dd v-if="details.prompt" class="max-h-24 overflow-y-auto whitespace-pre-wrap break-words rounded-lg bg-gray-50 px-2 py-2 text-xs leading-5 text-gray-600 dark:bg-dark-900/50 dark:text-dark-300">{{ details.prompt }}</dd>
            <dd v-else class="text-xs leading-5 text-gray-400">{{ t('intelligenceTest.promptUnavailable') }}</dd>
          </div>
          <div>
            <dt class="mb-2 text-xs font-medium text-gray-500 dark:text-dark-400">{{ t('intelligenceTest.finalAnswer') }}</dt>
            <dd v-if="details.answer" class="max-h-36 overflow-y-auto whitespace-pre-wrap break-words text-xs leading-5 text-gray-700 dark:text-dark-200">{{ details.answer }}</dd>
            <dd v-else class="text-xs leading-5 text-gray-400">{{ t(status === 'failed' ? 'intelligenceTest.noFinalAnswer' : 'intelligenceTest.answerUnavailable') }}</dd>
          </div>
        </dl>
      </template>
      <div v-if="showError && status === 'failed'" :class="mode === 'candy' ? 'mt-3' : ''">
        <h3 class="mb-2 text-xs font-medium text-gray-500 dark:text-dark-400">{{ t('intelligenceTest.failureReason') }}</h3>
        <div v-if="details.error_code && details.error_code !== 'historical_details_unavailable'" class="rounded-lg bg-amber-50 px-2 py-2 text-xs text-amber-800 dark:bg-amber-950/20 dark:text-amber-300">
          <p class="break-words font-mono text-xs">{{ details.error_code }}</p>
          <p v-if="details.error_detail" class="mt-2 max-h-28 overflow-y-auto whitespace-pre-wrap break-words leading-5">{{ details.error_detail }}</p>
        </div>
        <p v-else class="text-xs leading-5 text-gray-400">{{ t('intelligenceTest.failureReasonUnavailable') }}</p>
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { intelligenceTestAPI, type IntelligenceTestDetails, type IntelligenceTestMode, type IntelligenceTestResult } from '@/api/intelligenceTest'

const props = defineProps<{
  groupId: number
  resultId: number
  mode: IntelligenceTestMode
  status: IntelligenceTestResult['status']
  showError: boolean
}>()
const { t } = useI18n()
const container = ref<HTMLElement | null>(null)
const details = ref<IntelligenceTestDetails | null>(null)
const readFailed = ref(false)
let visible = false
let disposed = false
let observer: IntersectionObserver | undefined
let request: AbortController | undefined

async function load() {
  if (!visible || disposed || props.status === 'running' || request || details.value) return
  const controller = new AbortController()
  request = controller
  readFailed.value = false
  try {
    const response = await intelligenceTestAPI.details(props.groupId, props.resultId, controller.signal)
    if (!disposed && !controller.signal.aborted) details.value = response
  } catch {
    if (!disposed && !controller.signal.aborted) readFailed.value = true
  } finally {
    if (request === controller) request = undefined
  }
}

watch(() => [props.groupId, props.resultId, props.status] as const, () => {
  request?.abort()
  request = undefined
  details.value = null
  readFailed.value = false
  void load()
})

onMounted(() => {
  if (!('IntersectionObserver' in window)) {
    visible = true
    void load()
    return
  }
  observer = new IntersectionObserver(entries => {
    if (entries.some(entry => entry.isIntersecting)) {
      visible = true
      observer?.disconnect()
      void load()
    }
  }, { rootMargin: '150px' })
  if (container.value) observer.observe(container.value)
})

onBeforeUnmount(() => {
  disposed = true
  observer?.disconnect()
  request?.abort()
})
</script>
