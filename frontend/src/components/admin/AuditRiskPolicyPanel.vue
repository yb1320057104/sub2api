<template>
  <section class="rounded-xl border border-gray-200 p-4 dark:border-dark-700 sm:p-5" :data-test="`${audit}-risk-policy`">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h3 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.promptAudit.riskPolicy.title') }}</h3>
      <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-200">
        <input type="checkbox" :checked="modelValue != null" :data-test="`${audit}-risk-policy-enabled`" @change="setEnabled(($event.target as HTMLInputElement).checked)" />
        {{ t('admin.promptAudit.riskPolicy.enabled') }}
      </label>
    </div>
    <p class="mt-2 text-sm leading-6 text-gray-500 dark:text-gray-400">{{ t('admin.promptAudit.riskPolicy.hint') }}</p>
    <p v-if="modelValue == null" class="mt-3 text-sm text-amber-700 dark:text-amber-300" :data-test="`${audit}-risk-policy-legacy`">{{ t('admin.promptAudit.riskPolicy.legacy') }}</p>
    <template v-else>
      <div class="mt-4 grid gap-3 md:grid-cols-3">
        <div v-for="level in levels" :key="level" class="rounded-lg bg-gray-50 p-3 dark:bg-dark-900/40">
          <p class="font-medium text-gray-900 dark:text-white">{{ t(`admin.promptAudit.riskLevels.${level}`) }}</p>
          <label class="mt-3 block text-sm text-gray-700 dark:text-gray-200">
            {{ t('admin.promptAudit.riskPolicy.threshold') }}
            <input :value="modelValue[`${level}_threshold`]" type="number" min="0" max="100" step="1" class="input mt-1 w-full" :data-test="`${audit}-risk-${level}-threshold`" :aria-label="t('admin.promptAudit.riskPolicy.thresholdLabel', { level: t(`admin.promptAudit.riskLevels.${level}`) })" @input="setThreshold(level, ($event.target as HTMLInputElement).value)" />
          </label>
          <label class="mt-3 block text-sm text-gray-700 dark:text-gray-200">
            {{ t('admin.promptAudit.riskPolicy.action') }}
            <select :value="modelValue[`${level}_action`]" class="input mt-1 w-full" :data-test="`${audit}-risk-${level}-action`" :aria-label="t('admin.promptAudit.riskPolicy.actionLabel', { level: t(`admin.promptAudit.riskLevels.${level}`) })" @change="setAction(level, ($event.target as HTMLSelectElement).value as AuditPolicyAction)">
              <option v-for="action in actions" :key="action" :value="action">{{ t(`admin.promptAudit.riskPolicy.actions.${action}`) }}</option>
            </select>
          </label>
        </div>
      </div>
      <p v-if="!validAuditRiskPolicy(modelValue)" role="alert" class="mt-3 text-sm text-red-600 dark:text-red-300">{{ t('admin.promptAudit.riskPolicy.invalid') }}</p>
      <p v-else class="mt-3 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.promptAudit.riskPolicy.ranges', { low: modelValue.low_threshold, medium: modelValue.medium_threshold, high: modelValue.high_threshold }) }}</p>
      <p class="mt-3 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t(`admin.promptAudit.riskPolicy.${audit}Scores`) }}</p>
      <p class="mt-2 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.promptAudit.riskPolicy.chargeHint') }}</p>
      <p v-if="!synchronous" class="mt-2 text-xs text-amber-700 dark:text-amber-300">{{ t(`admin.promptAudit.riskPolicy.${audit}ModeHint`) }}</p>
    </template>
  </section>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { defaultAuditRiskPolicy, validAuditRiskPolicy } from '@/types/auditRiskPolicy'
import type { AuditPolicyAction, AuditRiskLevel, AuditRiskPolicy } from '@/types/auditRiskPolicy'

const props = defineProps<{ modelValue: AuditRiskPolicy | null | undefined; audit: 'content' | 'prompt'; synchronous: boolean }>()
const emit = defineEmits<{ (event: 'update:modelValue', value: AuditRiskPolicy | null): void }>()
const { t } = useI18n()
const levels: AuditRiskLevel[] = ['low', 'medium', 'high']
const actions: AuditPolicyAction[] = ['allow', 'block', 'charge_allow']
const previous = ref<AuditRiskPolicy>(defaultAuditRiskPolicy())
watch(() => props.modelValue, value => { if (value != null) previous.value = { ...value } }, { immediate: true, deep: true })
function setEnabled(enabled: boolean) { emit('update:modelValue', enabled ? { ...previous.value } : null) }
function setThreshold(level: AuditRiskLevel, value: string) {
  if (props.modelValue) emit('update:modelValue', { ...props.modelValue, [`${level}_threshold`]: value.trim() === '' ? Number.NaN : Number(value) })
}
function setAction(level: AuditRiskLevel, value: AuditPolicyAction) {
  if (props.modelValue) emit('update:modelValue', { ...props.modelValue, [`${level}_action`]: value })
}
</script>
