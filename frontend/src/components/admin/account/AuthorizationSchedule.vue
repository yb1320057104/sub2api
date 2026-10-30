<template>
  <section class="rounded-2xl border border-gray-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-800">
    <div class="mb-5 flex flex-wrap items-start justify-between gap-3">
      <div><h2 class="font-semibold text-gray-900 dark:text-white">{{ t('authorization.schedule') }}</h2><p class="mt-1 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('authorization.scheduleHint') }}</p></div>
      <RouterLink to="/admin/account-authorization" class="text-sm font-medium text-primary-600 dark:text-primary-300">{{ t('authorization.openWorkbench') }} →</RouterLink>
    </div>
    <form v-if="config" class="space-y-4" @submit.prevent="save">
      <fieldset :disabled="saving" class="grid gap-5 md:grid-cols-2">
        <div class="space-y-3 rounded-xl bg-gray-50 p-4 dark:bg-dark-900/50">
          <label class="flex items-center justify-between gap-3 font-medium"><span>{{ t('authorization.inspectionEnabled') }}</span><Toggle v-model="config.inspection_enabled" :aria-label="t('authorization.inspectionEnabled')" /></label>
          <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('authorization.inspectionCron') }} <code class="ml-2">{{ config.inspection_cron }}</code></p>
          <label class="block"><span class="input-label">{{ t('authorization.inspectionConcurrency') }}</span><input v-model.number="config.inspection_concurrency" type="number" min="1" max="50" step="1" required class="input" /></label>
          <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('authorization.next') }}：{{ date(config.next_inspection_at) }}</p>
        </div>
        <div class="space-y-3 rounded-xl bg-gray-50 p-4 dark:bg-dark-900/50">
          <label class="flex items-center justify-between gap-3 font-medium"><span>{{ t('authorization.reauthEnabled') }}</span><Toggle v-model="config.reauth_enabled" :aria-label="t('authorization.reauthEnabled')" /></label>
          <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('authorization.reauthCron') }} <code class="ml-2">{{ config.reauth_cron }}</code></p>
          <label class="block"><span class="input-label">{{ t('authorization.reauthConcurrency') }}</span><input v-model.number="config.authorization_concurrency" type="number" min="1" max="50" step="1" required class="input" /></label>
          <label class="block"><span class="input-label">{{ t('authorization.statusCodes') }}</span><input v-model="codes" class="input font-mono" placeholder="401, 403" required /><span class="mt-1 block text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('authorization.statusCodesHint') }}</span></label>
          <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('authorization.next') }}：{{ date(config.next_reauth_at) }}</p>
        </div>
      </fieldset>
      <p v-if="!config.browser_available" class="rounded-xl bg-amber-50 p-3 text-xs leading-5 text-amber-800 dark:bg-amber-950/30 dark:text-amber-200">{{ t('authorization.browserMissing') }}</p>
      <p v-if="!config.encryption_key_configured" class="rounded-xl bg-amber-50 p-3 text-xs leading-5 text-amber-800 dark:bg-amber-950/30 dark:text-amber-200">{{ t('authorization.encryptionMissing') }}</p>
      <div class="flex flex-wrap items-center justify-between gap-3"><p class="max-w-3xl text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('authorization.latestAccountsHint') }}</p><button class="btn btn-primary" :disabled="saving">{{ t(saving ? 'common.saving' : 'common.save') }}</button></div>
    </form>
    <p v-else-if="!error" class="text-sm text-gray-500">{{ t('common.loading') }}</p>
    <p v-if="error" class="mt-3 text-sm text-red-600 dark:text-red-400" role="alert">{{ error }} <button type="button" class="underline" @click="load">{{ t('authorization.refresh') }}</button></p>
  </section>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Toggle from '@/components/common/Toggle.vue'
import { accountAuthorizationAPI, type AuthorizationConfig } from '@/api/admin/accountAuthorization'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatDateTime } from '@/utils/format'
const { t } = useI18n()
const app = useAppStore()
const config = ref<AuthorizationConfig | null>(null)
const codes = ref('401')
const error = ref('')
const saving = ref(false)
const controller = new AbortController()
const date = (value: string | null) => value ? formatDateTime(value) : t('authorization.off')
async function load() {
  try { const value = await accountAuthorizationAPI.config(controller.signal); if (!controller.signal.aborted) { config.value = value; codes.value = value.reauth_status_codes.join(', '); error.value = '' } }
  catch (e) { if (!controller.signal.aborted) error.value = extractApiErrorMessage(e, t('authorization.loadFailed')) }
}
async function save() {
  if (!config.value || saving.value) return
  const values = codes.value.trim().split(/[\s,，;；]+/).map(Number)
  if (!values.length || values.some(value => !Number.isInteger(value) || value < 400 || value > 599) || new Set(values).size !== values.length) { error.value = t('authorization.invalidCodes'); return }
  saving.value = true; error.value = ''
  try { const value = await accountAuthorizationAPI.saveConfig({ ...config.value, reauth_status_codes: values }); if (!controller.signal.aborted) { config.value = value; app.showSuccess(t('authorization.saved')) } }
  catch (e) { if (!controller.signal.aborted) error.value = extractApiErrorMessage(e, t('authorization.saveFailed')) }
  finally { saving.value = false }
}
onMounted(load)
onUnmounted(() => controller.abort())
</script>
