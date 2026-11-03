<template>
  <section class="border-b border-gray-100 py-6 dark:border-dark-800" aria-labelledby="prompt-engine-title">
    <h2 id="prompt-engine-title" class="text-base font-semibold text-gray-950 dark:text-white">{{ t('admin.promptAudit.local.title') }}</h2>
    <div class="mt-3 flex flex-wrap gap-5 text-sm text-gray-700 dark:text-dark-200">
      <label v-for="engine in ['local_rules', 'remote'] as const" :key="engine" class="flex items-center gap-2">
        <input type="radio" name="prompt-audit-engine" :value="engine" :checked="draft.engine === engine" @change="patch({ engine })" />
        {{ t(`admin.promptAudit.local.engines.${engine}`) }}
      </label>
    </div>
    <p class="mt-3 text-sm text-gray-500 dark:text-dark-300">{{ t(draft.engine === 'local_rules' ? 'admin.promptAudit.local.localHint' : 'admin.promptAudit.local.remoteHint') }}</p>

    <div v-if="draft.engine === 'remote'" class="mt-4 rounded-xl bg-primary-50 p-4 dark:bg-primary-950/30">
      <label class="flex items-center gap-2 text-sm font-medium"><input type="checkbox" :checked="draft.fast_mode" data-test="prompt-fast-mode" @change="patch({ fast_mode: ($event.target as HTMLInputElement).checked })" />{{ t('admin.promptAudit.fastMode.title') }}</label>
      <p class="mt-2 text-sm text-gray-600 dark:text-gray-300">{{ t('admin.promptAudit.fastMode.hint') }}</p>
    </div>
    <div v-if="draft.engine === 'local_rules' || draft.fast_mode" class="mt-5">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <h3 class="text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.promptAudit.local.rules') }}</h3>
        <div class="flex gap-2">
          <button type="button" class="btn btn-secondary btn-sm" data-test="local-rule-templates" :disabled="!draft.default_local_rules?.length" @click="requestTemplates">{{ t('admin.promptAudit.local.templates') }}</button>
          <button type="button" class="btn btn-secondary btn-sm" data-test="local-rule-add" :disabled="draft.local_rules.length >= 64" @click="addRule">{{ t('admin.promptAudit.local.add') }}</button>
        </div>
      </div>
      <p class="mt-2 text-xs text-gray-500 dark:text-dark-400">{{ t(draft.risk_policy ? 'admin.promptAudit.riskPolicy.localScoreHint' : 'admin.promptAudit.local.actionHint') }}</p>
      <p class="mt-2 text-xs text-gray-500 dark:text-dark-400">{{ t('admin.promptAudit.local.templateUpdateHint') }}</p>
      <p v-if="draft.local_rules.length === 0" class="mt-4 rounded-lg bg-gray-50 p-4 text-sm text-gray-500 dark:bg-dark-900/40">{{ t('admin.promptAudit.local.empty') }}</p>
      <div class="mt-4 space-y-3">
        <article v-for="(rule, index) in draft.local_rules" :key="rule.id" class="rounded-xl border border-gray-200 p-4 dark:border-dark-700" :data-test="`local-rule-${index}`">
          <div class="flex flex-wrap items-center gap-3">
            <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-dark-200"><input type="checkbox" :checked="rule.enabled" @change="updateRule(index, { enabled: ($event.target as HTMLInputElement).checked })" />{{ t('admin.promptAudit.local.enabled') }}</label>
            <input :value="rule.name" class="input min-w-0 flex-1" maxlength="100" :aria-label="t('admin.promptAudit.local.name')" @input="updateRule(index, { name: ($event.target as HTMLInputElement).value })" />
            <button type="button" class="btn btn-secondary btn-sm" :aria-label="t('admin.promptAudit.local.remove', { name: rule.name })" @click="removeRule(index)">{{ t('common.delete') }}</button>
          </div>
          <div class="mt-3 grid gap-3 sm:grid-cols-3">
            <label class="text-sm text-gray-700 dark:text-dark-200">{{ t('admin.promptAudit.local.kind') }}
              <select :value="rule.kind" class="input mt-1 w-full" @change="updateRule(index, { kind: ($event.target as HTMLSelectElement).value as PromptLocalRule['kind'] })"><option value="keyword">{{ t('admin.promptAudit.local.keyword') }}</option><option value="regex">{{ t('admin.promptAudit.local.regex') }}</option></select>
            </label>
            <label class="text-sm text-gray-700 dark:text-dark-200">{{ t('admin.promptAudit.local.category') }}
              <select :value="rule.category" class="input mt-1 w-full" @change="updateRule(index, { category: ($event.target as HTMLSelectElement).value })"><option v-for="scanner in SCANNER_CATALOG" :key="scanner.id" :value="scanner.id">{{ t(`admin.promptAudit.scanners.${scanner.id}`) }}</option></select>
            </label>
            <label v-if="!draft.risk_policy" class="text-sm text-gray-700 dark:text-dark-200">{{ t('admin.promptAudit.local.action') }}
              <select :value="rule.action" class="input mt-1 w-full" @change="updateRule(index, { action: ($event.target as HTMLSelectElement).value as PromptLocalRule['action'] })"><option value="warn">{{ t('admin.promptAudit.local.warn') }}</option><option value="block">{{ t('admin.promptAudit.local.block') }}</option></select>
            </label>
            <label v-else class="text-sm text-gray-700 dark:text-dark-200">{{ t('admin.promptAudit.riskPolicy.score') }}
              <input :value="rule.score === undefined ? (rule.action === 'block' ? 100 : 50) : rule.score" type="number" min="0" max="100" step="1" class="input mt-1 w-full" :data-test="`local-rule-score-${index}`" :aria-label="t('admin.promptAudit.riskPolicy.ruleScoreLabel', { name: rule.name })" @input="updateRule(index, { score: scoreInput(($event.target as HTMLInputElement).value) })" />
            </label>
          </div>
          <label class="mt-3 block text-sm text-gray-700 dark:text-dark-200">{{ t('admin.promptAudit.local.pattern') }}
            <textarea :value="rule.pattern" rows="2" maxlength="2000" class="input mt-1 w-full font-mono text-xs" :aria-label="t('admin.promptAudit.local.pattern')" @input="updateRule(index, { pattern: ($event.target as HTMLTextAreaElement).value })" />
          </label>
          <label class="mt-2 flex items-center gap-2 text-xs text-gray-600 dark:text-dark-300"><input type="checkbox" :checked="rule.case_sensitive" @change="updateRule(index, { case_sensitive: ($event.target as HTMLInputElement).checked })" />{{ t('admin.promptAudit.local.caseSensitive') }}</label>
        </article>
      </div>
    </div>
    <details class="mt-5 rounded-xl border border-gray-200 p-4 dark:border-dark-700">
      <summary class="cursor-pointer text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.promptAudit.local.instructions') }}</summary>
      <p class="mt-2 text-sm text-gray-500 dark:text-dark-300">{{ t('admin.promptAudit.local.instructionsHint') }}</p>
      <button type="button" class="btn btn-secondary btn-sm mt-3" data-test="local-instruction-template" :disabled="!draft.default_audit_instructions?.trim()" @click="requestInstructions">{{ t('admin.promptAudit.local.loadInstructions') }}</button>
      <textarea :value="draft.audit_instructions" rows="10" maxlength="16000" class="input mt-3 w-full text-sm" :aria-label="t('admin.promptAudit.local.instructions')" @input="patch({ audit_instructions: ($event.target as HTMLTextAreaElement).value })" />
    </details>
    <ConfirmDialog :show="showTemplates" :title="t('admin.promptAudit.local.replaceTitle')" :message="t('admin.promptAudit.local.replaceMessage')" @confirm="applyTemplates" @cancel="showTemplates = false" />
    <ConfirmDialog :show="showInstructions" :title="t('admin.promptAudit.local.replaceInstructionsTitle')" :message="t('admin.promptAudit.local.replaceInstructionsMessage')" @confirm="applyInstructions" @cancel="showInstructions = false" />
  </section>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import type { PromptAuditDraft, PromptLocalRule } from '../types'
import { cloneData, SCANNER_CATALOG } from '../viewModel'

const props = defineProps<{ draft: PromptAuditDraft }>()
const emit = defineEmits<{ (event: 'update:draft', value: PromptAuditDraft): void }>()
const { t } = useI18n()
const showTemplates = ref(false)
const showInstructions = ref(false)
function patch(value: Partial<PromptAuditDraft>) { emit('update:draft', { ...cloneData(props.draft), ...value }) }
function updateRule(index: number, value: Partial<PromptLocalRule>) {
  const rules = cloneData(props.draft.local_rules)
  rules[index] = { ...rules[index], ...value }
  patch({ local_rules: rules })
}
function removeRule(index: number) { patch({ local_rules: props.draft.local_rules.filter((_, i) => i !== index) }) }
function addRule() {
  patch({ local_rules: [...cloneData(props.draft.local_rules), { id: `rule-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`, name: t('admin.promptAudit.local.newRule'), kind: 'keyword', pattern: '', category: 'jailbreak', action: 'warn', ...(props.draft.risk_policy ? { score: 20 } : {}), enabled: true, case_sensitive: false }] })
}
function scoreInput(value: string): number { return value.trim() === '' ? Number.NaN : Number(value) }
function requestTemplates() {
  if (props.draft.local_rules.length) showTemplates.value = true
  else applyTemplates()
}
function applyTemplates() {
  showTemplates.value = false
  patch({ local_rules: cloneData(props.draft.default_local_rules ?? []) })
}
function requestInstructions() {
  if (props.draft.audit_instructions.trim()) showInstructions.value = true
  else applyInstructions()
}
function applyInstructions() {
  showInstructions.value = false
  patch({ audit_instructions: props.draft.default_audit_instructions ?? '' })
}
</script>
