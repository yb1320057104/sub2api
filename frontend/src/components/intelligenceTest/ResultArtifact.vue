<template>
  <div ref="container" class="min-h-44">
    <ArtifactPreview v-if="artifact" :artifact="artifact" :title="t('intelligenceTest.preview')" />
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
let disposed = false

async function load() {
  if (disposed || loading.value || artifact.value) return
  loading.value = true
  failed.value = false
  const controller = new AbortController()
  request = controller
  try {
    const response = await intelligenceTestAPI.artifact(props.groupId, props.resultId, controller.signal)
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
  observer?.disconnect()
  request?.abort()
})
</script>
