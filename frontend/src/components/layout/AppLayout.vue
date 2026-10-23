<template>
  <div
    class="app-layout min-h-screen bg-canvas dark:bg-dark-950"
    :style="{ '--app-sidebar-width': sidebarCollapsed ? '72px' : '16rem' }"
  >
    <!-- Sidebar -->
    <AppSidebar />

    <!-- Main Content Area -->
    <div
      class="relative min-h-screen transition-[margin-left] duration-300 motion-reduce:transition-none lg:ml-[var(--app-sidebar-width)]"
    >
      <!-- Header -->
      <AppHeader />

      <!-- Main Content -->
      <main class="p-4 md:p-6 lg:p-8">
        <slot />
      </main>
    </div>
  </div>
</template>

<script setup lang="ts">
import '@/styles/onboarding.css'
import { computed, onMounted } from 'vue'
import { useAppStore } from '@/stores'
import { useAuthStore } from '@/stores/auth'
import { useOnboardingTour } from '@/composables/useOnboardingTour'
import { useOnboardingStore } from '@/stores/onboarding'
import AppSidebar from './AppSidebar.vue'
import AppHeader from './AppHeader.vue'

const appStore = useAppStore()
const authStore = useAuthStore()
const sidebarCollapsed = computed(() => appStore.sidebarCollapsed)
const isAdmin = computed(() => authStore.user?.role === 'admin')

const { replayTour } = useOnboardingTour({
  storageKey: isAdmin.value ? 'admin_guide' : 'user_guide',
  autoStart: true
})

const onboardingStore = useOnboardingStore()

onMounted(() => {
  onboardingStore.setReplayCallback(replayTour)
})

defineExpose({ replayTour })
</script>

<style scoped>
.app-layout {
  --app-header-height: 4rem;
  --app-content-padding: 1rem;
}

@media (min-width: 768px) {
  .app-layout { --app-content-padding: 1.5rem; }
}

@media (min-width: 1024px) {
  .app-layout {
    --app-header-height: 5rem;
    --app-content-padding: 2rem;
  }
}
</style>
