<template>
  <div class="space-y-5">
    <form class="card space-y-5 p-5" novalidate @submit.prevent="save">
      <div>
        <h2 class="font-semibold text-gray-900 dark:text-white">{{ t('radarEmail.title') }}</h2>
        <p class="mt-1 text-sm leading-6 text-gray-500 dark:text-dark-400">{{ t('radarEmail.description') }}</p>
      </div>
      <div class="flex flex-wrap items-start justify-between gap-3 rounded-xl bg-gray-50 p-4 dark:bg-dark-800/70">
        <div>
          <p class="text-sm font-medium" :class="data?.smtp_configured ? 'text-emerald-700 dark:text-emerald-300' : 'text-amber-700 dark:text-amber-300'">{{ t(!initialized ? 'radarEmail.smtpLoading' : data?.smtp_configured ? 'radarEmail.smtpConfigured' : 'radarEmail.smtpMissing') }}</p>
          <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('radarEmail.smtpHint') }}</p>
        </div>
        <RouterLink to="/admin/settings" class="shrink-0 text-sm font-medium text-primary-600 hover:underline dark:text-primary-300">{{ t('radarEmail.openSettings') }} →</RouterLink>
      </div>
      <fieldset :disabled="!initialized || saving" class="space-y-5">
        <label class="inline-flex items-center gap-2 text-sm font-medium"><input v-model="form.enabled" type="checkbox" class="rounded border-gray-300 text-primary-600" @change="dirty = true" />{{ t('radarEmail.enabled') }}</label>
        <div class="grid gap-5 sm:grid-cols-2">
          <label class="block"><span class="input-label">{{ t('radarEmail.interval') }}</span><input v-model.number="form.interval_minutes" type="number" min="1" max="10080" step="1" required class="input" @input="dirty = true" /><span class="mt-1 block text-xs text-gray-500 dark:text-dark-400">{{ t('radarEmail.intervalHint') }}</span></label>
          <label class="block"><span class="input-label">{{ t('radarEmail.minimumCount') }}</span><input v-model.number="form.minimum_count" type="number" min="1" max="1000000" step="1" required class="input" @input="dirty = true" /><span class="mt-1 block text-xs text-gray-500 dark:text-dark-400">{{ t('radarEmail.minimumHint') }}</span></label>
        </div>
        <fieldset class="space-y-3 rounded-xl border border-gray-200 p-4 dark:border-dark-700">
          <legend class="px-1 text-sm font-medium">{{ t('radarEmail.conditions') }}</legend>
          <p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('radarEmail.conditionsHint') }}</p>
          <label v-for="condition in conditionKeys" :key="condition" class="flex items-start gap-3 text-sm">
            <input v-model="form.conditions[condition]" type="checkbox" class="mt-1 rounded border-gray-300 text-primary-600" @change="dirty = true" />
            <span><span class="block font-medium">{{ t('radarEmail.condition.' + condition) }}</span><span class="mt-0.5 block text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('radarEmail.conditionHint.' + condition) }}</span></span>
          </label>
        </fieldset>
        <label class="block"><span class="input-label">{{ t('radarEmail.recipients') }}</span><textarea v-model="recipientsText" rows="4" maxlength="6000" autocomplete="off" spellcheck="false" class="input resize-y font-mono text-sm" :placeholder="t('radarEmail.recipientsPlaceholder')" :required="form.enabled" @input="dirty = true" /><span class="mt-1 block text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('radarEmail.recipientsHint') }}</span></label>
        <div class="flex flex-wrap items-center justify-between gap-3">
          <p class="max-w-3xl text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('radarEmail.scheduleHint') }}</p>
          <button type="submit" class="btn btn-primary" :disabled="saving || !dirty || configConflict">{{ t(saving ? 'radarEmail.saving' : 'radarEmail.save') }}</button>
        </div>
      </fieldset>
      <p v-if="dirty" class="text-xs text-amber-700 dark:text-amber-300">{{ t('radarEmail.unsaved') }}</p>
      <div v-if="configConflict" class="rounded-xl border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800 dark:border-amber-900 dark:bg-amber-950/30 dark:text-amber-200" role="alert">
        <p>{{ t('radarEmail.conflict') }}</p>
        <button type="button" class="mt-2 font-medium underline disabled:opacity-50" :disabled="loading || saving" @click="load(true)">{{ t('radarEmail.reloadConfig') }}</button>
      </div>
      <p v-if="saveError" class="text-sm text-red-600 dark:text-red-400" role="alert">{{ saveError }}</p>
    </form>
    <section class="card space-y-4 p-5" :aria-busy="loading">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <h2 class="font-semibold text-gray-900 dark:text-white">{{ t('radarEmail.progress') }}</h2>
        <div class="flex items-center gap-3"><span class="rounded-full px-3 py-1 text-xs font-medium" :class="statusClass" role="status">{{ statusLabel }}</span><button type="button" class="btn btn-secondary px-3 py-1.5 text-xs" :disabled="loading || saving" @click="load()">{{ t('radarEmail.refresh') }}</button></div>
      </div>
      <p v-if="loadError" class="text-sm text-red-600 dark:text-red-400" role="alert">{{ loadError }}</p>
      <dl class="grid gap-5 text-sm sm:grid-cols-2 xl:grid-cols-4">
        <div><dt class="text-xs text-gray-500 dark:text-dark-400">{{ t('radarEmail.lastChecked') }}</dt><dd class="mt-1 tabular-nums">{{ date(data?.status.last_checked_at) }}</dd></div>
        <div><dt class="text-xs text-gray-500 dark:text-dark-400">{{ t('radarEmail.matchingCount') }}</dt><dd class="mt-1 font-semibold tabular-nums">{{ data?.status.last_count ?? '—' }} / {{ data?.status.total_accounts ?? '—' }}</dd></div>
        <div><dt class="text-xs text-gray-500 dark:text-dark-400">{{ t('radarEmail.unknownQuota') }}</dt><dd class="mt-1 tabular-nums">{{ !data ? '—' : data.config.conditions.quota_below_100 ? data.status.unknown_quota_count ?? '—' : t('radarEmail.quotaDisabled') }}</dd></div>
        <div><dt class="text-xs text-gray-500 dark:text-dark-400">{{ t('radarEmail.nextCheck') }}</dt><dd class="mt-1 tabular-nums">{{ !data ? '—' : !data.config.enabled ? t('radarEmail.disabled') : data.config.next_check_at ? date(data.config.next_check_at) : t('radarEmail.awaitingSchedule') }}</dd></div>
        <div><dt class="text-xs text-gray-500 dark:text-dark-400">{{ t('radarEmail.lastSent') }}</dt><dd class="mt-1 tabular-nums">{{ date(data?.status.last_sent_at) }}</dd></div>
        <div><dt class="text-xs text-gray-500 dark:text-dark-400">{{ t('radarEmail.deliveryCount') }}</dt><dd class="mt-1 tabular-nums">{{ data?.status.sent_count ?? '—' }} / {{ data?.status.failed_count ?? '—' }}</dd></div>
      </dl>
      <p v-if="data?.status.last_error" class="break-words rounded-lg bg-amber-50 p-3 text-sm leading-6 text-amber-800 dark:bg-amber-950/30 dark:text-amber-200">{{ data.status.last_error }}</p>
      <p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('radarEmail.refreshHint') }}</p>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useIntervalFn } from '@vueuse/core'
import { radarEmailAlertAPI, type RadarEmailAlertConfig, type RadarEmailAlertView } from '@/api/admin/radarEmailAlert'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatDateTime } from '@/utils/format'

const { t } = useI18n()
const app = useAppStore()
const conditionKeys = ['quota_below_100', 'qualified', 'not_401'] as const
const knownStatuses = ['idle', 'checking', 'healthy', 'sent', 'partial', 'send_failed', 'check_failed', 'superseded', 'interrupted']
const data = ref<RadarEmailAlertView | null>(null)
const form = ref<RadarEmailAlertConfig>({
  enabled: false, interval_minutes: 30, minimum_count: 1,
  conditions: { quota_below_100: true, qualified: true, not_401: true },
  recipients: [], revision: 0, next_check_at: null
})
const recipientsText = ref('')
const initialized = ref(false), dirty = ref(false), saving = ref(false), loading = ref(false)
const configConflict = ref(false), loadError = ref(''), saveError = ref('')
let disposed = false, sequence = 0
let controller: AbortController | undefined

const date = (value?: string | null) => value ? formatDateTime(value) : '—'
const statusLabel = computed(() => {
  if (!data.value) return t('radarEmail.loading')
  const status = data.value.status.last_status
  return knownStatuses.includes(status) ? t('radarEmail.status.' + status) : t('radarEmail.unknownStatus', { status: status || '—' })
})
const statusClass = computed(() => {
  const status = data.value?.status.last_status
  if (status === 'send_failed' || status === 'check_failed') return 'bg-red-50 text-red-700 dark:bg-red-950/30 dark:text-red-300'
  if (status === 'sent' || status === 'partial') return 'bg-amber-50 text-amber-700 dark:bg-amber-950/30 dark:text-amber-300'
  if (status === 'healthy') return 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/30 dark:text-emerald-300'
  return 'bg-primary-50 text-primary-700 dark:bg-primary-950/30 dark:text-primary-300'
})

function adoptConfig(config: RadarEmailAlertConfig) {
  form.value = { ...config, conditions: { ...config.conditions }, recipients: [...config.recipients] }
  recipientsText.value = config.recipients.join('\n')
  dirty.value = false
  configConflict.value = false
  saveError.value = ''
}

async function load(replaceDraft = false) {
  if (disposed || saving.value) return
  const id = ++sequence
  controller?.abort()
  controller = new AbortController()
  loading.value = true
  try {
    const value = await radarEmailAlertAPI.get(controller.signal)
    if (disposed || id !== sequence) return
    data.value = value
    loadError.value = ''
    // Status refreshes never replace unsaved form values or their CAS revision.
    if (!initialized.value || !dirty.value || replaceDraft) adoptConfig(value.config)
    initialized.value = true
  } catch (error) {
    if (!disposed && id === sequence) loadError.value = extractApiErrorMessage(error, t('radarEmail.loadFailed'))
  } finally {
    if (id === sequence) loading.value = false
  }
}

function validate(recipients: string[]) {
  if (!Number.isInteger(form.value.interval_minutes) || form.value.interval_minutes < 1 || form.value.interval_minutes > 10080) return t('radarEmail.invalidInterval')
  if (!Number.isInteger(form.value.minimum_count) || form.value.minimum_count < 1 || form.value.minimum_count > 1000000) return t('radarEmail.invalidMinimum')
  if (!conditionKeys.some(key => form.value.conditions[key])) return t('radarEmail.conditionsRequired')
  if (form.value.enabled && recipients.length === 0) return t('radarEmail.recipientsRequired')
  if (recipients.length > 20) return t('radarEmail.tooManyRecipients')
  if (recipients.some(email => email.length > 254 || !/^[^\s@<>]+@[^\s@<>]+$/.test(email))) return t('radarEmail.invalidRecipients')
  return ''
}

async function save() {
  if (!initialized.value || saving.value || configConflict.value) return
  const seenRecipients = new Set<string>()
  const recipients = recipientsText.value.split(/\r?\n/).map(value => value.trim()).filter(value => {
    const key = value.toLowerCase()
    if (!value || seenRecipients.has(key)) return false
    seenRecipients.add(key)
    return true
  })
  saveError.value = validate(recipients)
  if (saveError.value) return
  const config: RadarEmailAlertConfig = { ...form.value, conditions: { ...form.value.conditions }, recipients }
  saving.value = true
  sequence++
  controller?.abort()
  loading.value = false
  try {
    const value = await radarEmailAlertAPI.save(config)
    if (disposed) return
    data.value = value
    adoptConfig(value.config)
    loadError.value = ''
    app.showSuccess(t('radarEmail.saved'))
  } catch (error) {
    if (disposed) return
    const failure = error as { status?: number; response?: { status?: number } } | null
    if (failure?.status === 409 || failure?.response?.status === 409) configConflict.value = true
    else saveError.value = extractApiErrorMessage(error, t('radarEmail.saveFailed'))
  } finally {
    saving.value = false
  }
}

useIntervalFn(() => { if (!document.hidden && !loading.value && !saving.value) void load() }, 10000)
onMounted(() => { void load() })
onUnmounted(() => { disposed = true; sequence++; controller?.abort() })
</script>
