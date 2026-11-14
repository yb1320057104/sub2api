<template>
  <div class="mb-5 space-y-3">
    <nav :aria-label="t('accountOps.smartTitle')" class="rounded-2xl border border-gray-200 bg-white p-3 dark:border-dark-700 dark:bg-dark-900">
      <div class="flex flex-wrap items-center gap-1">
        <span class="mr-2 px-2 text-xs font-medium text-gray-500">{{ t('smartOps.commonTools') }}</span>
        <div v-for="item in commonItems" :key="item.id" class="flex items-center">
          <RouterLink :to="smartOperationLocation(item.id)" :aria-current="activeTool === item.id ? 'page' : undefined" class="rounded-lg px-4 py-2 text-sm transition-colors" :class="linkClass(item.id)">{{ t(item.label) }}</RouterLink>
          <NewFeatureBadge v-if="item.noticeId" :notice-id="item.noticeId" :user-id="authStore.user?.id" class="mx-1" />
        </div>
      </div>
      <details v-if="advancedItems.length" :open="advancedOpen" class="mt-2 border-t border-gray-100 pt-2 dark:border-dark-800" @toggle="advancedOpen = ($event.target as HTMLDetailsElement).open">
        <summary class="cursor-pointer rounded-lg px-2 py-2 text-xs font-medium text-gray-500">{{ t('smartOps.advancedTools') }}</summary>
        <div class="mt-1 flex flex-wrap gap-1">
          <div v-for="item in advancedItems" :key="item.id" class="flex items-center">
            <RouterLink :to="smartOperationLocation(item.id)" :aria-current="activeTool === item.id ? 'page' : undefined" class="rounded-lg px-3 py-2 text-sm transition-colors" :class="linkClass(item.id)">{{ t(item.label) }}</RouterLink>
            <NewFeatureBadge v-if="item.noticeId" :notice-id="item.noticeId" :user-id="authStore.user?.id" class="mx-1" />
          </div>
        </div>
      </details>
    </nav>
    <SmartOpsGuide :tool="activeTool" />
  </div>
</template>
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAdminSettingsStore } from '@/stores/adminSettings'
import { useAuthStore } from '@/stores/auth'
import NewFeatureBadge from '@/components/common/NewFeatureBadge.vue'
import SmartOpsGuide from './SmartOpsGuide.vue'
import { smartOperationForRoute, smartOperationLocation, visibleSmartOperationTools, type SmartOperationTool } from '@/utils/smartOperations'
const { t } = useI18n()
const route = useRoute()
const adminSettingsStore = useAdminSettingsStore()
const authStore = useAuthStore()
const activeTool = computed(() => smartOperationForRoute(route))
const items = computed(() => visibleSmartOperationTools({
  hiddenPaths: adminSettingsStore.smartOpsHiddenMenuItems ?? [],
  requestCaptureEnabled: adminSettingsStore.requestCaptureEnabled,
  isAdmin: authStore.isAdmin,
  simpleMode: authStore.isSimpleMode,
}))
const commonItems = computed(() => items.value.filter(item => item.common))
const advancedItems = computed(() => items.value.filter(item => !item.common))
const advancedOpen = ref(false)
watch(activeTool, tool => { advancedOpen.value = advancedItems.value.some(item => item.id === tool) }, { immediate: true })
function linkClass(tool: SmartOperationTool) {
  return activeTool.value === tool
    ? 'bg-gray-900 font-medium text-white dark:bg-gray-100 dark:text-gray-900'
    : 'text-gray-500 hover:bg-gray-100 dark:hover:bg-dark-800'
}
</script>
