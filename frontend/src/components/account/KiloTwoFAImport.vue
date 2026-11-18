<script setup lang="ts">
import { watch } from 'vue'
import { useI18n } from 'vue-i18n'
import CredentialEncryptionSetup from './CredentialEncryptionSetup.vue'
import KiloTwoFARow from './KiloTwoFARow.vue'
import KiloRoutingFields from '@/components/admin/kilo/KiloRoutingFields.vue'
import Toggle from '@/components/common/Toggle.vue'
import { useKiloTwoFAImport, type KiloTwoFAImportProps } from '@/composables/useKiloTwoFAImport'
import type { KiloOnboardingBatchResult } from '@/api/admin/kilo'

const props = defineProps<KiloTwoFAImportProps>()
const emit = defineEmits<{ busy: [value: boolean]; created: [result: KiloOnboardingBatchResult] }>()
const { t } = useI18n()
const {
  raw, rows, strategy, groupIds, routing, activateWhenReady, encryptionReady, prepared, reusable,
  groups, proxyGroups, proxies, choicesLoading, choicesError, busy, error, requestId, result,
  validation, capacityWarning, loadChoices, preview, updateRow, reset, submit
} = useKiloTwoFAImport(props, receipt => emit('created', receipt))
watch(busy, value => emit('busy', value), { flush: 'sync' })
</script>

<template>
  <div class="space-y-5" data-testid="kilo-two-fa-import">
    <CredentialEncryptionSetup @ready="encryptionReady = $event" />
    <div class="space-y-2"><p class="text-sm leading-6 text-gray-600 dark:text-dark-300">{{ t('kilo.twoFA.description') }}</p><p class="rounded-lg bg-primary-50 p-3 text-sm leading-6 text-primary-800 dark:bg-primary-900/20 dark:text-primary-200">{{ t('kilo.inferenceBoundary') }}</p><p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('kilo.twoFA.privacy') }}</p></div>
    <template v-if="!result">
      <section v-if="!rows.length" class="space-y-3">
        <label for="kilo-two-fa-credentials" class="input-label">{{ t('kilo.twoFA.credentials') }}</label>
        <textarea id="kilo-two-fa-credentials" v-model="raw" class="input w-full font-mono" rows="5" autocomplete="off" autocapitalize="off" :spellcheck="false" :disabled="busy" :placeholder="t('kilo.twoFA.placeholder')" />
        <button type="button" class="btn btn-secondary" :disabled="busy || !raw.trim()" data-testid="kilo-two-fa-preview" @click="preview">{{ t('kilo.twoFA.preview') }}</button>
      </section>
      <section class="space-y-3" :aria-label="t('kilo.pool.strategy')">
        <div class="flex flex-wrap items-center justify-between gap-2"><h3 class="text-sm font-semibold">{{ t('kilo.pool.strategy') }}</h3><span class="text-xs text-gray-500 dark:text-dark-400" data-testid="kilo-idle-count">{{ t('kilo.pool.idleCount', { count: reusable.length }) }}</span></div>
        <div class="grid gap-3 sm:grid-cols-2">
          <label class="flex cursor-pointer items-start gap-3 rounded-lg border p-3" :class="strategy === 'prefer_idle' ? 'border-primary-500 bg-primary-50 dark:bg-primary-900/20' : 'border-gray-200 dark:border-dark-700'"><input v-model="strategy" type="radio" name="kilo-two-fa-strategy" value="prefer_idle" class="mt-1 text-primary-600 focus:ring-primary-500" :disabled="busy" /><span><span class="block text-sm font-medium">{{ t('kilo.pool.preferIdle') }}</span><span class="mt-1 block text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('kilo.pool.preferIdleHint') }}</span></span></label>
          <label class="flex cursor-pointer items-start gap-3 rounded-lg border p-3" :class="strategy === 'new_identity' ? 'border-primary-500 bg-primary-50 dark:bg-primary-900/20' : 'border-gray-200 dark:border-dark-700'"><input v-model="strategy" type="radio" name="kilo-two-fa-strategy" value="new_identity" class="mt-1 text-primary-600 focus:ring-primary-500" :disabled="busy || !!initialPrepared" /><span><span class="block text-sm font-medium">{{ t('kilo.pool.newIdentity') }}</span><span class="mt-1 block text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('kilo.pool.newIdentityHint') }}</span></span></label>
        </div>
      </section>
      <section v-if="rows.length" class="space-y-3">
        <div class="flex flex-wrap items-center justify-between gap-3"><h3 class="text-sm font-semibold">{{ t('kilo.twoFA.review', { count: rows.length }) }}</h3><button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="reset">{{ t('kilo.twoFA.editCredentials') }}</button></div>
        <KiloTwoFARow v-for="(row, index) in rows" :key="row.login.email" :model-value="row" :index="index" :strategy="strategy" :prepared="prepared" :proxies="proxies" :excluded-prepared-ids="rows.filter(item => item !== row).flatMap(item => item.prepared_id ? [item.prepared_id] : [])" :excluded-proxy-ids="rows.filter(item => item !== row && !item.prepared_id).flatMap(item => item.login_proxy_id ? [item.login_proxy_id] : [])" :disabled="busy || choicesLoading" :fixed-prepared="!!initialPrepared" @update:model-value="updateRow(row.login.email, $event)" />
      </section>
      <KiloRoutingFields v-model="routing" v-model:group-ids="groupIds" :groups="groups" :proxy-groups="proxyGroups" :disabled="busy || choicesLoading" />
      <div class="flex items-start gap-3"><Toggle id="kilo-two-fa-activate" v-model="activateWhenReady" :disabled="busy" :aria-label="t('kilo.onboarding.activate')" /><div><label for="kilo-two-fa-activate" class="text-sm font-medium">{{ t('kilo.onboarding.activate') }}</label><p class="mt-1 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ activateWhenReady ? t('kilo.onboarding.activateHint') : t('kilo.onboarding.importOnlyHint') }}</p></div></div>
      <p class="rounded-lg bg-gray-50 p-3 text-xs leading-5 text-gray-600 dark:bg-dark-900/50 dark:text-dark-300">{{ t('kilo.pool.lifecycle') }}</p>
      <p v-if="capacityWarning" class="text-sm text-amber-700 dark:text-amber-300">{{ capacityWarning }}</p>
      <p v-if="requestId" class="break-all text-xs leading-5 text-gray-500 dark:text-dark-400" role="status">{{ t('kilo.requestIdentity', { id: requestId }) }}</p>
      <p v-if="choicesLoading" class="text-sm text-gray-500 dark:text-dark-400" role="status">{{ t('common.loading') }}</p>
      <p v-else-if="validation" class="text-sm text-amber-700 dark:text-amber-300" role="status">{{ validation }}</p>
      <button v-if="choicesError" type="button" class="btn btn-secondary btn-sm" :disabled="busy || choicesLoading" @click="loadChoices">{{ t('common.refresh') }}</button>
      <div class="flex flex-wrap items-center gap-3"><button type="button" class="btn btn-primary" :disabled="busy || choicesLoading || !!validation" data-testid="kilo-two-fa-submit" @click="submit">{{ busy ? t('common.submitting') : t('kilo.twoFA.submit') }}</button><p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('kilo.twoFA.submitHint') }}</p></div>
    </template>
    <p v-else class="rounded-lg bg-emerald-50 p-3 text-sm text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-300" role="status">{{ t('kilo.batch.created', { id: result.batch_id }) }}</p>
    <p v-if="error" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300" role="alert">{{ error }}</p>
  </div>
</template>
