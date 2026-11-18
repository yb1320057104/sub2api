<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import Toggle from '@/components/common/Toggle.vue'
import KiloOnboardingSourceRow from './KiloOnboardingSourceRow.vue'
import { useKiloOnboardingForm, type KiloOnboardingFormProps } from './useKiloOnboardingForm'
import type { KiloOnboardingBatchResult } from '@/api/admin/kilo'

const props = defineProps<KiloOnboardingFormProps>()
const emit = defineEmits<{ close: []; created: [result: KiloOnboardingBatchResult]; reloadChoices: [] }>()
const { t } = useI18n()
const {
  rows, search, groupIds, routing, activateWhenReady, confirmed, sharedModels,
  proxies, proxiesLoading, proxiesError, busy, error, requestId, sourceById,
  filteredSources, selectedIds, groupOptions, proxyGroupOptions, capacityWarning,
  validation, sourceReason, toggleSource, selectVisible, updateRow, removeRow,
  applyModels, loadProxies, submit
} = useKiloOnboardingForm(props, result => emit('created', result))
</script>

<template>
  <BaseDialog :show="show" :title="t('kilo.onboarding.title')" width="wide" :show-close-button="!busy" :close-on-escape="!busy" @close="!busy && emit('close')">
    <form id="kilo-onboarding-form" class="space-y-5" @submit.prevent="submit">
      <div class="space-y-2"><p class="text-sm leading-6 text-gray-600 dark:text-dark-300">{{ t('kilo.onboarding.description') }}</p><p class="rounded-lg bg-primary-50 p-3 text-sm leading-6 text-primary-800 dark:bg-primary-900/20 dark:text-primary-200">{{ t('kilo.inferenceBoundary') }}</p><p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('kilo.onboarding.privacy') }}</p></div>
      <p v-if="requestId" class="break-all text-xs leading-5 text-gray-500 dark:text-dark-400" role="status">{{ t('kilo.requestIdentity', { id: requestId }) }}</p>
      <section class="space-y-3" :aria-label="t('kilo.onboarding.sources')">
        <label for="kilo-onboarding-search" class="block text-sm font-medium">{{ t('kilo.onboarding.sources') }}</label>
        <input id="kilo-onboarding-search" v-model="search" type="search" class="input w-full" :placeholder="t('kilo.batch.sourceSearch')" :disabled="busy" />
        <div class="flex flex-wrap items-center justify-between gap-2 text-xs"><span class="text-gray-500 dark:text-dark-400">{{ t('kilo.onboarding.selected', { count: rows.length }) }}</span><div class="flex gap-2"><button type="button" class="btn btn-secondary btn-sm" :disabled="busy || choicesLoading || rows.length >= 100 || !filteredSources.length" @click="selectVisible">{{ t('kilo.selectVisible') }}</button><button type="button" class="btn btn-secondary btn-sm" :disabled="busy || !rows.length" @click="rows = []">{{ t('common.clear') }}</button></div></div>
        <div class="max-h-48 overflow-auto rounded-lg border border-gray-200 dark:border-dark-700">
          <p v-if="!filteredSources.length" class="p-4 text-sm text-gray-500 dark:text-dark-400">{{ choicesLoading ? t('common.loading') : t('kilo.onboarding.noSources') }}</p>
          <label v-for="source in filteredSources" :key="source.id" class="flex min-h-11 items-start gap-3 border-b border-gray-100 px-3 py-3 last:border-0 dark:border-dark-700"><input type="checkbox" class="mt-0.5 h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500" :checked="selectedIds.has(source.id)" :disabled="busy || !selectedIds.has(source.id) && (!!sourceReason(source) || rows.length >= 100)" @change="toggleSource(source)" /><span class="min-w-0 text-sm"><span class="break-words font-medium">{{ source.name }} <span class="text-gray-400">#{{ source.id }}</span></span><span class="block break-all text-xs text-gray-500 dark:text-dark-400">{{ source.email || t('kilo.batch.sourceMissingEmail') }}</span><span v-if="sourceReason(source)" class="mt-1 block break-words text-xs text-amber-700 dark:text-amber-300">{{ sourceReason(source) }}</span></span></label>
        </div>
      </section>
      <section v-if="rows.length" class="space-y-3" :aria-label="t('kilo.onboarding.mapping')">
        <h3 class="text-sm font-semibold">{{ t('kilo.onboarding.mapping') }}</h3>
        <p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('kilo.onboarding.loginHint') }}</p>
        <div class="space-y-2 rounded-lg bg-gray-50 p-3 dark:bg-dark-900/50"><label for="kilo-onboarding-shared-models" class="block text-sm font-medium">{{ t('kilo.onboarding.sharedModels') }}</label><textarea id="kilo-onboarding-shared-models" v-model="sharedModels" class="input min-h-16 w-full font-mono text-xs" :disabled="busy" :placeholder="t('kilo.onboarding.modelsPlaceholder')" spellcheck="false" /><button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="applyModels">{{ t('kilo.onboarding.applyModels') }}</button></div>
        <p v-if="proxiesLoading" class="text-xs text-gray-500 dark:text-dark-400" role="status">{{ t('kilo.onboarding.loadingProxies') }}</p>
        <p v-if="proxiesError" class="text-xs text-red-600 dark:text-red-400" role="alert">{{ proxiesError }} <button type="button" class="underline" :disabled="busy || proxiesLoading" @click="loadProxies">{{ t('common.refresh') }}</button></p>
        <KiloOnboardingSourceRow v-for="row in rows" :key="row.source_account_id" :model-value="row" :source="sourceById.get(row.source_account_id)" :proxies="proxies" :excluded-proxy-ids="rows.filter(item => item.source_account_id !== row.source_account_id).flatMap(item => item.login_proxy_id ? [item.login_proxy_id] : [])" :disabled="busy || proxiesLoading" @update:model-value="updateRow(row.source_account_id, $event)" @remove="removeRow(row.source_account_id)" />
      </section>
      <section class="space-y-4 rounded-xl border border-gray-200 p-4 dark:border-dark-700">
        <div class="flex items-start gap-3"><Toggle id="kilo-onboarding-activate" v-model="activateWhenReady" :disabled="busy" :aria-label="t('kilo.onboarding.activate')" /><div><label for="kilo-onboarding-activate" class="text-sm font-medium">{{ t('kilo.onboarding.activate') }}</label><p class="mt-1 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ activateWhenReady ? t('kilo.onboarding.activateHint') : t('kilo.onboarding.importOnlyHint') }}</p></div></div>
        <div class="grid gap-4 sm:grid-cols-2">
          <div><label for="kilo-onboarding-groups" class="mb-2 block text-sm font-medium">{{ t('kilo.batch.groups') }}</label><Select id="kilo-onboarding-groups" v-model="groupIds" :options="groupOptions" multiple :disabled="busy || choicesLoading" :aria-label="t('kilo.batch.groups')" :placeholder="t('kilo.batch.groupPlaceholder')" /></div>
          <div><label for="kilo-onboarding-proxy-group" class="mb-2 block text-sm font-medium">{{ t('kilo.batch.proxyGroup') }}</label><Select id="kilo-onboarding-proxy-group" v-model="routing.proxy_group_id" :options="proxyGroupOptions" :disabled="busy || choicesLoading" :aria-label="t('kilo.batch.proxyGroup')" :placeholder="t('kilo.batch.proxyPlaceholder')" /></div>
          <div><label for="kilo-onboarding-proxy-count" class="mb-2 block text-sm font-medium">{{ t('kilo.batch.proxiesPerAccount') }}</label><input id="kilo-onboarding-proxy-count" v-model.number="routing.proxies_per_account" type="number" class="input w-full" min="1" max="100" required :disabled="busy" /></div>
          <div><label for="kilo-onboarding-proxy-concurrency" class="mb-2 block text-sm font-medium">{{ t('kilo.batch.proxyConcurrency') }}</label><input id="kilo-onboarding-proxy-concurrency" v-model.number="routing.proxy_concurrency" type="number" class="input w-full" min="1" max="10000" required :disabled="busy" /></div>
          <div><label for="kilo-onboarding-account-concurrency" class="mb-2 block text-sm font-medium">{{ t('kilo.batch.accountConcurrency') }}</label><input id="kilo-onboarding-account-concurrency" v-model.number="routing.account_concurrency" type="number" class="input w-full" min="1" max="10000" required :disabled="busy" /></div>
          <div><label for="kilo-onboarding-priority" class="mb-2 block text-sm font-medium">{{ t('kilo.batch.priority') }}</label><input id="kilo-onboarding-priority" v-model.number="routing.priority" type="number" class="input w-full" min="0" max="10000" required :disabled="busy" /></div>
          <div class="sm:col-span-2"><label for="kilo-onboarding-statuses" class="mb-2 block text-sm font-medium">{{ t('kilo.routing.autoReleaseStatuses') }}</label><input id="kilo-onboarding-statuses" v-model="routing.statuses" type="text" class="input w-full" :disabled="busy" placeholder="401, 402, 403" /><p class="mt-2 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('kilo.routing.autoReleaseHint') }}</p></div>
        </div>
        <p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('kilo.routing.requestHint') }}</p>
        <p v-if="capacityWarning && activateWhenReady" class="text-sm text-amber-700 dark:text-amber-300">{{ capacityWarning }}</p>
      </section>
      <p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('kilo.onboarding.closeHint') }}</p>
      <label class="flex items-start gap-3 text-sm leading-6"><input id="kilo-onboarding-confirm" v-model="confirmed" type="checkbox" class="mt-1 h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500" :disabled="busy" /><span>{{ t('kilo.onboarding.confirmation') }}</span></label>
      <p v-if="validation" class="text-sm text-amber-700 dark:text-amber-300" role="status">{{ validation }} <button v-if="choicesError" type="button" class="underline" :disabled="busy || choicesLoading" @click="emit('reloadChoices')">{{ t('common.refresh') }}</button></p>
      <p v-if="error" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300" role="alert">{{ error }}</p>
    </form>
    <template #footer><button type="button" class="btn btn-secondary" :disabled="busy" @click="emit('close')">{{ t('common.cancel') }}</button><button type="submit" form="kilo-onboarding-form" class="btn btn-primary" :disabled="busy || choicesLoading || proxiesLoading || !!validation || !confirmed">{{ busy ? t('common.submitting') : t('kilo.onboarding.submit') }}</button></template>
  </BaseDialog>
</template>
