<template>
  <AppLayout>
    <div class="mx-auto max-w-[1600px] space-y-5">
      <SmartOpsNav />
      <h2 class="text-xl font-semibold">{{ t('smartOps.notifications.title') }}</h2>
      <nav class="flex flex-wrap gap-2" role="tablist" :aria-label="t('smartOps.notifications.title')">
        <button v-for="item in sections" :id="`notification-tab-${item}`" :key="item" type="button"
          role="tab" :aria-selected="section === item" :aria-controls="`notification-panel-${item}`"
          :tabindex="section === item ? 0 : -1" :data-testid="`notification-tab-${item}`"
          class="rounded-lg px-4 py-2 text-sm font-medium"
          :class="section === item ? 'bg-primary-50 text-primary-700 dark:bg-primary-950/40 dark:text-primary-300' : 'bg-gray-100 text-gray-600 dark:bg-dark-800 dark:text-gray-300'"
          @click="selectSection(item)" @keydown.left.prevent="switchSection" @keydown.right.prevent="switchSection">
          {{ t(`smartOps.notifications.${item}`) }}
        </button>
      </nav>
      <section :id="`notification-panel-${section}`" role="tabpanel" :aria-labelledby="`notification-tab-${section}`" class="space-y-4">
        <p class="text-sm leading-6 text-gray-500 dark:text-gray-400">{{ t(`smartOps.notifications.${section}Hint`) }}</p>
        <RadarEmailAlerts v-if="section === 'overview'" />
        <AccountOpsView v-else embedded />
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, nextTick } from 'vue'
import { useRoute, useRouter, type LocationQueryRaw } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import SmartOpsNav from '@/components/admin/operations/SmartOpsNav.vue'
import RadarEmailAlerts from '@/components/admin/account/RadarEmailAlerts.vue'
import AccountOpsView from './AccountOpsView.vue'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const sections = ['overview', 'accounts'] as const
type NotificationSection = typeof sections[number]
const section = computed<NotificationSection>(() => route.query.notification === 'accounts' ? 'accounts' : 'overview')

async function selectSection(value: NotificationSection) {
  if (value === section.value) return
  const query: LocationQueryRaw = { ...route.query, notification: value }
  delete query.tab
  await router.replace({ query })
}

async function switchSection() {
  await selectSection(section.value === 'overview' ? 'accounts' : 'overview')
  await nextTick()
  document.getElementById(`notification-tab-${section.value}`)?.focus()
}
</script>
