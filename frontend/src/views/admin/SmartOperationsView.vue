<template>
  <component :is="selectedView" v-if="selectedView" :key="tool" />
  <AppLayout v-else>
    <SmartOpsNav />
    <section class="rounded-2xl border border-gray-200 bg-white p-6 dark:border-dark-700 dark:bg-dark-800" :aria-busy="!loadFailed">
      <template v-if="loadFailed">
        <p role="alert" class="text-sm text-red-600 dark:text-red-400">{{ t('smartOps.loadFailed') }}</p>
        <div class="mt-4 flex gap-3">
          <button type="button" class="btn btn-primary" @click="loadTool">{{ t('smartOps.retry') }}</button>
          <button type="button" class="btn btn-secondary" @click="reloadPage">{{ t('smartOps.reload') }}</button>
        </div>
      </template>
      <p v-else role="status" class="text-sm text-gray-500">{{ t('smartOps.loading') }}</p>
    </section>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, shallowRef, ref, watch, type Component } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import SmartOpsNav from '@/components/admin/operations/SmartOpsNav.vue'
import { smartOperationTool, type SmartOperationTool } from '@/utils/smartOperations'

const route = useRoute()
const { t } = useI18n()
const tool = computed(() => smartOperationTool(route.query.tool))
const loaders: Record<SmartOperationTool, () => Promise<{ default: Component }>> = {
  radar: () => import('./RadarObservationView.vue'),
  credentials: () => import('./ops/TokenGuardV2View.vue'),
  notifications: () => import('./SmartNotificationsView.vue'),
  quality: () => import('./AccountQualityView.vue'),
  'auto-config': () => import('./AutoConfigView.vue'),
  priority: () => import('./PrioritySchedulingView.vue'),
  channels: () => import('./UpstreamChannelOperationsView.vue'),
  profit: () => import('./ProfitOperationsView.vue'),
  experiments: () => import('./ControlledExperimentsView.vue'),
  'external-guard': () => import('./ops/TokenGuardView.vue'),
  pelican: () => import('./PelicanTestsView.vue'),
  'cache-tests': () => import('./GroupCacheTestView.vue'),
  captures: () => import('./RequestCaptureView.vue'),
  ai: () => import('./AIOpsView.vue'),
  harvest: () => import('./HarvestFlowView.vue'),
  lottery: () => import('./LotteryView.vue'),
}
const selectedView = shallowRef<Component | null>(null)
const loadFailed = ref(false)
let generation = 0

async function loadTool() {
  const current = ++generation
  selectedView.value = null
  loadFailed.value = false
  try {
    const view = await loaders[tool.value]()
    if (generation === current) selectedView.value = view.default
  } catch {
    if (generation === current) loadFailed.value = true
  }
}

function reloadPage() { window.location.reload() }

// Each tool owns its layout and cleanup. Only changing tools unmounts it;
// changing a tab/filter retains the active tool's form state.
watch(tool, () => { void loadTool() }, { immediate: true })
onBeforeUnmount(() => { generation++ })
</script>
