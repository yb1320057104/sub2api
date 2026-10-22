<template>
  <div ref="container" class="min-h-[320px] sm:min-h-[380px]">
    <ArtifactPreview v-if="artifact" :artifact="artifact" :title="t('intelligenceTest.preview')" />
    <div v-else class="flex min-h-[320px] flex-col items-center justify-center gap-3 p-6 text-sm text-gray-400 sm:min-h-[380px]">
      <template v-if="failed">
        <p role="alert">{{ t('intelligenceTest.loadFailed') }}</p>
        <button type="button" class="btn btn-secondary" @click="load">{{ t('intelligenceTest.retry') }}</button>
      </template>
      <p v-else role="status">{{ t('intelligenceTest.loading') }}</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { intelligenceTestAPI } from '@/api/intelligenceTest'
import ArtifactPreview from './ArtifactPreview.vue'

const props = defineProps<{ groupId: number; resultId: number }>()
const { t } = useI18n()
const container = ref<HTMLElement | null>(null)
const artifact = ref('')
const failed = ref(false)
const loading = ref(false)
let observer: IntersectionObserver | undefined
let request: AbortController | undefined

async function load() {
  if (loading.value || artifact.value) return
  loading.value = true
  failed.value = false
  const controller = new AbortController()
  request = controller
  try {
    const response = await intelligenceTestAPI.artifact(props.groupId, props.resultId, controller.signal)
    if (controller.signal.aborted) return
    artifact.value = response.artifact
    if (!artifact.value) failed.value = true
  } catch {
    if (!controller.signal.aborted) failed.value = true
  } finally {
    loading.value = false
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
  observer?.disconnect()
  request?.abort()
})
</script>
