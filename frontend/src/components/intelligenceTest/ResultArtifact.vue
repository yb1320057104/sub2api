<template>
  <div ref="container" class="min-h-44">
    <template v-if="artifact">
      <div class="group relative">
        <ArtifactPreview :artifact="artifact" :title="t('intelligenceTest.preview')" />
        <button
          type="button"
          class="absolute inset-0 flex cursor-zoom-in items-end justify-end p-2 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary-500"
          :aria-label="t('intelligenceTest.enlargePreview')"
          :title="t('intelligenceTest.enlargePreview')"
          aria-haspopup="dialog"
          :aria-expanded="previewOpen"
          @click="previewOpen = true"
        >
          <span class="inline-flex items-center gap-1 rounded-md bg-white/90 px-2 py-1 text-xs font-medium text-gray-600 shadow-sm group-hover:text-primary-600 dark:bg-dark-800/90 dark:text-dark-200">
            <svg class="h-3.5 w-3.5" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M7 3H3v4M13 3h4v4M17 13v4h-4M7 17H3v-4" stroke-linecap="round" stroke-linejoin="round" /></svg>
            {{ t('intelligenceTest.enlargePreview') }}
          </span>
        </button>
      </div>
      <BaseDialog
        :show="previewOpen"
        :title="t('intelligenceTest.expandedPreview')"
        width="full"
        :close-on-click-outside="true"
        @close="previewOpen = false"
      >
        <div ref="previewContent" tabindex="-1">
          <ArtifactPreview :artifact="artifact" :title="t('intelligenceTest.expandedPreview')" expanded />
        </div>
      </BaseDialog>
    </template>
    <div v-else class="flex min-h-44 flex-col items-center justify-center gap-3 p-3 text-center text-xs leading-5 text-gray-400">
      <template v-if="failed">
        <p role="alert">{{ t('intelligenceTest.loadFailed') }}</p>
        <button type="button" class="btn btn-secondary" @click="load">{{ t('intelligenceTest.retry') }}</button>
      </template>
      <p v-else role="status">{{ t('intelligenceTest.loading') }}</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { intelligenceTestAPI } from '@/api/intelligenceTest'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ArtifactPreview from './ArtifactPreview.vue'

const props = defineProps<{
  groupId: number
  resultId: number
  loadArtifact?: (groupId: number, resultId: number, signal?: AbortSignal) => Promise<{ artifact: string }>
}>()
const { t } = useI18n()
const container = ref<HTMLElement | null>(null)
const previewContent = ref<HTMLElement | null>(null)
const artifact = ref('')
const previewOpen = ref(false)
const failed = ref(false)
const loading = ref(false)
let observer: IntersectionObserver | undefined
let request: AbortController | undefined
let disposed = false
let previewDialog: HTMLElement | null = null
let previewFocusRevision = 0

function detachPreviewFocusTrap() {
  previewDialog?.removeEventListener('keydown', trapPreviewFocus)
  previewDialog = null
}

function trapPreviewFocus(event: KeyboardEvent) {
  if (event.key !== 'Tab' || !previewOpen.value || !previewDialog) return
  const focusable = Array.from(previewDialog.querySelectorAll<HTMLElement>(
    'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]',
  )).filter(element => element.tabIndex >= 0 && element.getClientRects().length > 0)
  const first = focusable[0]
  const last = focusable[focusable.length - 1]
  if (!first || !last) {
    event.preventDefault()
    previewContent.value?.focus()
    return
  }
  const active = document.activeElement
  const atBoundary = event.shiftKey ? active === first : active === last
  const outsideTabOrder = !focusable.some(element => element === active)
  if (atBoundary || outsideTabOrder) {
    event.preventDefault()
    const target = event.shiftKey ? last : first
    target.focus()
  }
}

watch(previewOpen, async (open) => {
  const revision = ++previewFocusRevision
  detachPreviewFocusTrap()
  if (!open) return
  await nextTick()
  if (disposed || !previewOpen.value || revision !== previewFocusRevision) return
  previewDialog = previewContent.value?.closest<HTMLElement>('[role="dialog"]') ?? null
  previewDialog?.addEventListener('keydown', trapPreviewFocus)
})

async function load() {
  if (disposed || loading.value || artifact.value) return
  loading.value = true
  failed.value = false
  const controller = new AbortController()
  request = controller
  try {
    const response = await (props.loadArtifact ?? intelligenceTestAPI.artifact)(props.groupId, props.resultId, controller.signal)
    if (disposed || controller.signal.aborted) return
    artifact.value = response.artifact
    if (!artifact.value) failed.value = true
  } catch {
    if (!disposed && !controller.signal.aborted) failed.value = true
  } finally {
    if (!disposed) loading.value = false
  }
}

onMounted(() => {
  if (!('IntersectionObserver' in window)) {
    void load()
    return
  }
  observer = new IntersectionObserver(entries => {
    if (entries.some(entry => entry.isIntersecting)) {
      observer?.disconnect()
      void load()
    }
  }, { rootMargin: '150px' })
  if (container.value) observer.observe(container.value)
})

onBeforeUnmount(() => {
  disposed = true
  previewFocusRevision++
  detachPreviewFocusTrap()
  observer?.disconnect()
  request?.abort()
})
</script>
