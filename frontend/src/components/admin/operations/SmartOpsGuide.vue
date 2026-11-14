<template>
  <aside class="rounded-xl border border-primary-100 bg-primary-50/50 px-4 py-3 dark:border-primary-900/50 dark:bg-primary-950/20" :aria-label="t('smartOps.guide.title')" data-testid="smart-ops-guide">
    <p class="text-sm leading-6 text-gray-700 dark:text-dark-200"><span class="mr-2 font-semibold text-gray-900 dark:text-white">{{ t('smartOps.guide.purpose') }}</span>{{ t(`${prefix}.purpose`) }}</p>
    <details :key="toolID" class="group mt-2">
      <summary class="w-fit cursor-pointer rounded text-sm font-medium text-primary-700 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-primary-500 dark:text-primary-300">{{ t('smartOps.guide.expand') }}</summary>
      <div class="mt-4 space-y-4 text-sm leading-6 text-gray-600 dark:text-dark-300">
        <p><span class="mr-2 font-semibold text-gray-800 dark:text-dark-100">{{ t('smartOps.guide.when') }}</span>{{ t(`${prefix}.when`) }}</p>
        <div>
          <p class="mb-2 font-semibold text-gray-800 dark:text-dark-100">{{ t('smartOps.guide.steps') }}</p>
          <ol class="list-decimal space-y-1.5 pl-5">
            <li v-for="step in 4" :key="step">{{ t(`${prefix}.step${step}`) }}</li>
          </ol>
        </div>
        <p><span class="mr-2 font-semibold text-gray-800 dark:text-dark-100">{{ t('smartOps.guide.result') }}</span>{{ t(`${prefix}.result`) }}</p>
        <p><span class="mr-2 font-semibold text-gray-800 dark:text-dark-100">{{ t('smartOps.guide.effects') }}</span>{{ t(`${prefix}.effects`) }}</p>
      </div>
    </details>
  </aside>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

const props = defineProps<{ tool: string }>()
const { t } = useI18n()
const toolIDs = new Set(['radar', 'credentials', 'notifications', 'quality', 'auto-config', 'priority', 'channels', 'profit', 'experiments', 'external-guard', 'pelican', 'cache-tests', 'captures', 'ai', 'harvest', 'lottery'])
const toolID = computed(() => toolIDs.has(props.tool) ? props.tool : 'radar')
const prefix = computed(() => `smartOps.tools.${toolID.value}`)
</script>
