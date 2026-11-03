<template>
  <section class="rounded-xl border border-gray-200 p-4 dark:border-dark-700" data-test="content-keyword-scores">
    <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.promptAudit.riskPolicy.keywordScores') }}</h3>
    <p class="mt-2 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.promptAudit.riskPolicy.keywordScoresHint') }}</p>
    <div class="mt-3 grid gap-3 sm:grid-cols-2">
      <label class="text-sm text-gray-700 dark:text-gray-200">{{ t('admin.promptAudit.riskPolicy.keywordCategory') }}
        <select v-model="category" class="input mt-1 w-full" data-test="keyword-score-category">
          <option value="all">{{ t('admin.promptAudit.riskPolicy.allKeywords') }}</option>
          <option v-for="group in CONTENT_AUDIT_KEYWORD_TEMPLATES" :key="group.id" :value="group.id">{{ t(`admin.riskControl.keywordTemplateCategories.${group.id}`) }}</option>
          <option value="custom">{{ t('admin.promptAudit.riskPolicy.customKeywords') }}</option>
        </select>
      </label>
      <label class="text-sm text-gray-700 dark:text-gray-200">{{ t('admin.promptAudit.riskPolicy.keywordSearch') }}
        <input v-model="search" type="search" class="input mt-1 w-full" data-test="keyword-score-search" />
      </label>
    </div>
    <div class="mt-3 flex flex-wrap items-end gap-2">
      <label class="text-sm text-gray-700 dark:text-gray-200">{{ t('admin.promptAudit.riskPolicy.score') }}
        <input v-model.number="bulkScore" type="number" min="0" max="100" step="1" class="input mt-1 w-28" data-test="keyword-score-bulk-value" />
      </label>
      <button type="button" class="btn btn-secondary" :disabled="disabled || filtered.length === 0 || !validAuditScore(bulkScore)" data-test="keyword-score-bulk-apply" @click="applyBulk">{{ t('admin.promptAudit.riskPolicy.applyFiltered', { count: filtered.length }) }}</button>
    </div>
    <div class="mt-3 divide-y divide-gray-100 dark:divide-dark-700">
      <label v-for="keyword in visible" :key="auditKeywordKey(keyword)" class="flex items-center justify-between gap-4 py-2 text-sm text-gray-700 dark:text-gray-200">
        <span class="min-w-0 break-words">{{ keyword }}</span>
        <input :value="auditKeywordScore(keyword, scores)" type="number" min="0" max="100" step="1" class="input w-24 shrink-0" :disabled="disabled" :aria-label="t('admin.promptAudit.riskPolicy.keywordScoreLabel', { keyword })" :data-keyword-score="keyword" @input="setScore(keyword, ($event.target as HTMLInputElement).value)" />
      </label>
    </div>
    <p v-if="!filtered.length" class="mt-3 text-sm text-gray-500">{{ t('admin.promptAudit.riskPolicy.noKeywords') }}</p>
    <div v-if="filtered.length > pageSize" class="mt-3 flex items-center justify-between gap-3 text-xs text-gray-500">
      <button type="button" class="btn btn-secondary btn-sm" :disabled="page <= 1" data-test="keyword-score-previous" @click="page--">{{ t('admin.promptAudit.riskPolicy.previousPage') }}</button>
      <span>{{ page }} / {{ pages }}</span>
      <button type="button" class="btn btn-secondary btn-sm" :disabled="page >= pages" data-test="keyword-score-next" @click="page++">{{ t('admin.promptAudit.riskPolicy.nextPage') }}</button>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { CONTENT_AUDIT_KEYWORD_TEMPLATES } from '@/utils/contentAuditKeywordTemplates'
import { auditKeywordKey, auditKeywordScore, validAuditScore } from '@/types/auditRiskPolicy'

const props = defineProps<{ keywords: string[]; scores: Record<string, number>; disabled?: boolean }>()
const emit = defineEmits<{ (event: 'update:scores', value: Record<string, number>): void }>()
const { t } = useI18n()
const category = ref('all')
const search = ref('')
const bulkScore = ref<number | string>(20)
const page = ref(1)
const pageSize = 20
const keywordCategories = new Map(CONTENT_AUDIT_KEYWORD_TEMPLATES.flatMap(group => group.keywords.map(keyword => [auditKeywordKey(keyword), group.id] as const)))
const filtered = computed(() => props.keywords.filter(keyword => {
  const key = auditKeywordKey(keyword)
  return (category.value === 'all' || (keywordCategories.get(key) ?? 'custom') === category.value) && key.includes(search.value.trim().toLowerCase())
}))
const pages = computed(() => Math.max(1, Math.ceil(filtered.value.length / pageSize)))
const visible = computed(() => filtered.value.slice((page.value - 1) * pageSize, page.value * pageSize))
watch([category, search], () => { page.value = 1 })
watch(pages, value => { page.value = Math.min(page.value, value) })
function setScore(keyword: string, value: string) {
  emit('update:scores', { ...props.scores, [auditKeywordKey(keyword)]: value.trim() === '' ? Number.NaN : Number(value) })
}
function applyBulk() {
  if (props.disabled || !validAuditScore(bulkScore.value)) return
  const score = bulkScore.value
  emit('update:scores', { ...props.scores, ...Object.fromEntries(filtered.value.map(keyword => [auditKeywordKey(keyword), score])) })
}
</script>
