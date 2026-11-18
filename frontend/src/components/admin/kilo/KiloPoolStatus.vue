<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { KiloPrepared } from '@/api/admin/kilo'
import { isReusableKiloPrepared } from './pool'

const props = defineProps<{ item: KiloPrepared }>()
const { t, locale } = useI18n()
const mode = computed(() => props.item.identity_mode === 'gpt_coupled' || props.item.identity_mode === 'independent' ? props.item.identity_mode : 'unknown')
const checkedAt = computed(() => props.item.identity_checked_at ? new Date(props.item.identity_checked_at).toLocaleString(locale.value) : '')
const modeTone = computed(() => mode.value === 'gpt_coupled' ? 'bg-primary-50 text-primary-800 dark:bg-primary-900/20 dark:text-primary-200' : mode.value === 'independent' ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-300' : 'bg-amber-50 text-amber-800 dark:bg-amber-900/20 dark:text-amber-300')
const identity = computed(() => props.item.identity_status ?? 'unknown')
const binding = computed(() => props.item.binding_state ?? 'unknown')
const reusable = computed(() => isReusableKiloPrepared(props.item))
const identityTone = computed(() => identity.value === 'banned' ? 'bg-red-50 text-red-700 dark:bg-red-900/20 dark:text-red-300' : identity.value === 'retired' ? 'bg-amber-50 text-amber-800 dark:bg-amber-900/20 dark:text-amber-300' : identity.value === 'active' ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-300' : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-dark-300')
const bindingTone = computed(() => binding.value === 'blocked' ? 'bg-red-50 text-red-700 dark:bg-red-900/20 dark:text-red-300' : ['recovering', 'unbinding', 'binding'].includes(binding.value) ? 'bg-amber-50 text-amber-800 dark:bg-amber-900/20 dark:text-amber-300' : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-dark-300')
</script>

<template>
  <div class="space-y-2" data-testid="kilo-pool-status" aria-live="polite">
    <span class="inline-flex rounded-md px-2 py-1 text-xs font-medium" :class="modeTone" :aria-label="`${t('kilo.pool.modeLabel')}: ${t(`kilo.pool.mode.${mode}`)}`" data-testid="kilo-identity-mode">{{ t(`kilo.pool.mode.${mode}`) }}</span>
    <p class="text-xs leading-5 text-gray-500 dark:text-dark-400" data-testid="kilo-identity-policy">{{ t(`kilo.pool.modeHint.${mode}`) }}</p>
    <p v-if="item.identity_source_account_id" class="text-xs text-gray-500 dark:text-dark-400" data-testid="kilo-identity-origin">{{ t('kilo.pool.identityOrigin', { id: item.identity_source_account_id }) }}</p>
    <p v-if="checkedAt" class="text-xs text-gray-500 dark:text-dark-400">{{ t('kilo.pool.identityChecked', { time: checkedAt }) }}</p>
    <div class="flex flex-wrap items-center gap-2"><span class="inline-flex rounded-md px-2 py-1 text-xs font-medium" :class="identityTone" :aria-label="`${t('kilo.pool.identityLabel')}: ${t(`kilo.pool.identity.${identity}`)}`">{{ t(`kilo.pool.identity.${identity}`) }}</span><span class="inline-flex rounded-md px-2 py-1 text-xs font-medium" :class="bindingTone" :aria-label="`${t('kilo.pool.bindingLabel')}: ${t(`kilo.pool.binding.${binding}`)}`">{{ t(`kilo.pool.binding.${binding}`) }}</span></div>
    <p v-if="identity === 'retired'" class="text-xs leading-5 text-amber-700 dark:text-amber-300" data-testid="kilo-identity-retired">{{ t('kilo.pool.retiredHint') }}</p>
    <p v-if="reusable" class="text-xs font-medium text-emerald-700 dark:text-emerald-300" data-testid="kilo-reusable-slot">{{ t('kilo.pool.reusable') }}</p>
    <p v-if="item.binding_revision !== undefined" class="text-xs text-gray-500 dark:text-dark-400">{{ t('kilo.pool.revision', { revision: item.binding_revision }) }}</p>
    <p v-if="item.recovery_task_id" class="text-xs text-gray-500 dark:text-dark-400">{{ t('kilo.pool.recoveryTask', { id: item.recovery_task_id }) }}</p>
    <p v-if="item.binding_error" class="break-words text-xs leading-5 text-red-600 dark:text-red-400" role="status">{{ item.binding_error }}</p>
  </div>
</template>
