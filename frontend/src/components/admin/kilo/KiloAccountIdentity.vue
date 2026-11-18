<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import KiloStatusBadge from './KiloStatusBadge.vue'
import { openAIPlanTypeLabel } from '@/utils/planType'
import type { KiloAccountView } from '@/types'

const props = withDefaults(defineProps<{
  view: KiloAccountView
  canManageSource?: boolean
  disabled?: boolean
  sourceLoading?: boolean
}>(), { canManageSource: true, disabled: false, sourceLoading: false })
const emit = defineEmits<{ 'manage-source': [id: number] }>()
const { t } = useI18n()
const source = computed(() => props.view.source)
const plan = computed(() => openAIPlanTypeLabel(source.value?.plan_type) || t('kilo.identity.planUnknown'))
const workspace = computed(() => {
  const id = source.value?.workspace_id || ''
  return id.length > 20 ? `${id.slice(0, 8)}…${id.slice(-4)}` : id
})
</script>

<template>
  <div class="min-w-0 space-y-2" data-testid="kilo-account-identity">
    <div class="min-w-0">
      <p class="break-words text-sm font-medium text-gray-900 dark:text-dark-100">{{ view.name || t('kilo.identity.key') }}</p>
      <p v-if="view.email" class="mt-0.5 break-all text-xs text-gray-500 dark:text-dark-400">{{ view.email }}</p>
      <div class="mt-1 flex flex-wrap items-center gap-1.5 text-[11px]">
        <span class="rounded bg-primary-50 px-1.5 py-0.5 font-medium text-primary-700 dark:bg-primary-900/30 dark:text-primary-300">{{ t('kilo.identity.key') }}</span>
        <span class="text-gray-500 dark:text-dark-400">{{ view.has_key ? t('kilo.identity.keyReady') : t('kilo.identity.keyMissing') }}</span>
      </div>
      <p v-if="view.organization_id" class="mt-1 break-all text-xs text-gray-500 dark:text-dark-400">{{ t('kilo.prepared.organization', { id: view.organization_id }) }}</p>
    </div>
    <div v-if="source" class="min-w-0 space-y-1 border-l-2 border-primary-200 pl-2.5 dark:border-primary-900" data-testid="kilo-gpt-identity">
      <div class="flex flex-wrap items-center gap-1.5">
        <span class="text-xs font-medium text-gray-700 dark:text-dark-200">GPT · {{ plan }}</span>
        <KiloStatusBadge :status="source.status || 'unknown'" />
      </div>
      <p v-if="source.name" class="break-words text-xs text-gray-700 dark:text-dark-200">{{ source.name }}</p>
      <p v-if="source.email" class="break-all text-xs text-gray-500 dark:text-dark-400">{{ source.email }}</p>
      <p v-if="source.workspace_id" class="max-w-[14rem] truncate text-xs text-gray-500 dark:text-dark-400" :title="t('kilo.identity.workspace', { id: source.workspace_id })" data-testid="kilo-workspace">{{ t('kilo.identity.workspace', { id: workspace }) }}</p>
      <p class="text-[11px] leading-5 text-gray-500 dark:text-dark-400">{{ source.quota_only ? t('kilo.identity.sourcePurpose') : t('kilo.identity.sourceUnconfirmed') }}</p>
      <button
        v-if="canManageSource"
        type="button"
        class="inline-flex min-h-8 items-center rounded-md px-1.5 text-xs font-medium text-primary-700 underline-offset-2 hover:bg-primary-50 hover:underline focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary-500 disabled:cursor-not-allowed disabled:opacity-50 dark:text-primary-300 dark:hover:bg-primary-900/20"
        :disabled="disabled || sourceLoading"
        :aria-label="t('kilo.identity.manageSourceHint')"
        data-testid="kilo-manage-source"
        @click="emit('manage-source', source.id)"
      >{{ sourceLoading ? t('common.loading') : t('kilo.identity.manageSource') }}</button>
    </div>
    <p v-else class="text-xs leading-5 text-gray-500 dark:text-dark-400" data-testid="kilo-unbound">{{ t('kilo.identity.unbound') }}</p>
  </div>
</template>
