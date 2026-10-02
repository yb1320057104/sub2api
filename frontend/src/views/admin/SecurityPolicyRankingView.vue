<template>
  <AppLayout>
    <div class="space-y-5">
      <div>
        <h1 class="text-2xl font-semibold text-gray-900 dark:text-gray-100">{{ t('securityPolicyRanking.title') }}</h1>
        <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ t('securityPolicyRanking.description') }}</p>
        <p v-if="!isAdmin" class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('securityPolicyRanking.maskedIdentity') }}</p>
      </div>

      <section class="card space-y-3 p-4">
        <div class="flex flex-wrap items-center justify-between gap-4">
          <DateRangePicker :start-date="startDate" :end-date="endDate" @change="changeRange" />
          <div class="flex flex-wrap items-center gap-4">
            <label class="flex items-center gap-2 text-sm">
              <input v-model="autoRefresh" type="checkbox" class="h-4 w-4 rounded" data-testid="ranking-auto-refresh" />
              {{ t('securityPolicyRanking.autoRefresh') }}
            </label>
            <label class="flex items-center gap-2 text-sm">
              {{ t('securityPolicyRanking.refreshSeconds') }}
              <input v-model.number="refreshSeconds" type="number" min="1" max="3600" step="1" class="input w-24" data-testid="ranking-refresh-seconds" :aria-invalid="!validInterval" />
            </label>
            <button class="btn btn-secondary" :disabled="refreshing" data-testid="ranking-refresh" @click="refresh()">{{ t('common.refresh') }}</button>
          </div>
        </div>
        <div class="flex flex-wrap justify-between gap-2 text-xs text-gray-500 dark:text-gray-400">
          <span>{{ t('securityPolicyRanking.timezone', { timezone }) }}</span>
          <span v-if="ranking?.generated_at">{{ t('securityPolicyRanking.lastUpdated', { time: dateTime(ranking.generated_at) }) }}</span>
        </div>
        <p v-if="!validInterval" class="text-sm text-red-600 dark:text-red-400" role="alert">{{ t('securityPolicyRanking.invalidInterval') }}</p>
        <p v-if="rangeError" class="text-sm text-red-600 dark:text-red-400" role="alert">{{ rangeError }}</p>
      </section>

      <section v-if="isAdmin" class="card space-y-4 p-4" data-testid="ranking-settings">
        <form class="space-y-4" @submit.prevent="saveSettings">
          <div class="flex flex-wrap items-end gap-3">
            <div class="min-w-48 flex-1">
              <label for="ranking-multiplier" class="input-label">{{ t('securityPolicyRanking.multiplier') }}</label>
              <input id="ranking-multiplier" v-model="settings.multiplier" type="text" inputmode="decimal" maxlength="11" class="input mt-1 w-full sm:max-w-60" :disabled="!settingsReady || savingSettings" aria-describedby="ranking-multiplier-hint" />
            </div>
            <button class="btn btn-primary" type="submit" :disabled="!settingsReady || savingSettings || deductionBusy" data-testid="ranking-save-settings">{{ savingSettings ? t('common.processing') : t('common.save') }}</button>
          </div>
          <p id="ranking-multiplier-hint" class="text-xs text-gray-500 dark:text-gray-400">{{ t('securityPolicyRanking.multiplierHint') }}</p>
          <div class="flex flex-wrap items-center gap-x-6 gap-y-3 border-t border-gray-200 pt-4 dark:border-dark-700">
            <label class="flex items-center gap-2 text-sm">
              <input v-model="settings.auto_disable_enabled" type="checkbox" class="h-4 w-4 rounded" :disabled="!settingsReady || savingSettings" data-testid="ranking-auto-disable" />
              {{ t('securityPolicyRanking.autoDisable') }}
            </label>
            <label class="flex items-center gap-2 text-sm">
              {{ t('securityPolicyRanking.threshold') }}
              <input v-model.number="settings.auto_disable_threshold" type="number" min="1" max="1000000000" step="1" class="input w-36" :disabled="!settingsReady || savingSettings" data-testid="ranking-threshold" />
            </label>
            <label class="flex items-center gap-2 text-sm">
              <input v-model="settings.reset_on_manual_enable" type="checkbox" class="h-4 w-4 rounded" :disabled="!settingsReady || savingSettings" data-testid="ranking-reset-on-enable" />
              {{ t('securityPolicyRanking.resetOnEnable') }}
            </label>
          </div>
          <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('securityPolicyRanking.autoDisableHint') }}</p>
          <RouterLink to="/admin/users" class="inline-block text-sm text-primary-600 hover:underline dark:text-primary-400">{{ t('securityPolicyRanking.manageUsers') }}</RouterLink>
        </form>
        <p v-if="settingsError" class="text-sm text-red-600 dark:text-red-400" role="alert">{{ settingsError }}</p>
        <button v-if="!settingsReady && !loadingSettings" class="btn btn-secondary" @click="loadSettings">{{ t('common.refresh') }}</button>
        <p v-if="savedSettings" class="text-sm text-gray-600 dark:text-gray-300">{{ t('securityPolicyRanking.savedMultiplier', { multiplier: decimal(savedSettings.multiplier) }) }}</p>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('securityPolicyRanking.deductionHint') }}</p>
      </section>

      <div class="grid gap-3" :class="isAdmin ? 'sm:grid-cols-3' : 'sm:grid-cols-2'">
        <div class="card p-4"><p class="text-sm text-gray-500">{{ t('securityPolicyRanking.totalUsers') }}</p><p class="mt-2 text-2xl font-semibold tabular-nums">{{ ranking?.total ?? '—' }}</p></div>
        <div class="card p-4"><p class="text-sm text-gray-500">{{ t('securityPolicyRanking.totalTriggers') }}</p><p class="mt-2 text-2xl font-semibold tabular-nums">{{ ranking?.total_triggers ?? '—' }}</p></div>
        <div v-if="isAdmin" class="card p-4"><p class="text-sm text-gray-500">{{ t('securityPolicyRanking.totalPending') }}</p><p class="mt-2 text-2xl font-semibold tabular-nums">{{ ranking?.total_pending ?? '—' }}</p></div>
      </div>

      <div v-if="rankingError" class="rounded-lg bg-red-50 p-4 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300" role="alert">
        <p v-if="ranking">{{ t('securityPolicyRanking.refreshError') }}</p>
        <p>{{ rankingError }}</p>
      </div>
      <section class="card overflow-hidden" :aria-busy="refreshing">
        <div v-if="!ranking && refreshing" class="p-8 text-center text-gray-500">{{ t('common.loading') }}</div>
        <div v-else-if="ranking && !ranking.items.length" class="p-10 text-center text-gray-500">{{ t('securityPolicyRanking.empty') }}</div>
        <div v-else-if="ranking" class="overflow-x-auto">
          <table class="w-full text-left text-sm" data-testid="ranking-table">
            <thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-900/50 dark:text-gray-400">
              <tr>
                <th class="whitespace-nowrap px-4 py-3">{{ t('securityPolicyRanking.rank') }}</th>
                <th class="px-4 py-3">{{ t('securityPolicyRanking.user') }}</th>
                <th class="whitespace-nowrap px-4 py-3 text-right">{{ t('securityPolicyRanking.triggers') }}</th>
                <template v-if="isAdmin">
                  <th class="whitespace-nowrap px-4 py-3 text-right">{{ t('securityPolicyRanking.deducted') }}</th>
                  <th class="whitespace-nowrap px-4 py-3 text-right">{{ t('securityPolicyRanking.pending') }}</th>
                  <th class="whitespace-nowrap px-4 py-3 text-right">{{ t('securityPolicyRanking.amount') }}</th>
                  <th class="whitespace-nowrap px-4 py-3 text-right">{{ t('securityPolicyRanking.balance') }}</th>
                  <th class="whitespace-nowrap px-4 py-3">{{ t('securityPolicyRanking.status') }}</th>
                  <th class="whitespace-nowrap px-4 py-3 text-right">{{ t('common.actions') }}</th>
                </template>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
              <tr v-for="row in ranking.items" :key="isAdmin ? row.user_id ?? row.rank : row.rank" class="hover:bg-gray-50 dark:hover:bg-dark-700/40" :data-testid="`ranking-user-${isAdmin ? row.user_id ?? row.rank : row.rank}`">
                <td class="px-4 py-4 font-semibold tabular-nums" :class="row.rank <= 3 ? 'text-primary-600 dark:text-primary-400' : 'text-gray-500'">{{ row.rank }}</td>
                <td class="px-4 py-4"><div class="font-medium">{{ isAdmin ? row.username || `#${row.user_id}` : row.display_name }}</div><div v-if="isAdmin" class="mt-1 text-xs text-gray-500">{{ row.email }} · #{{ row.user_id }}</div></td>
                <td class="px-4 py-4 text-right font-semibold tabular-nums">{{ row.trigger_count }}</td>
                <template v-if="isAdmin">
                  <td class="px-4 py-4 text-right tabular-nums">{{ row.deducted_count }}</td>
                  <td class="px-4 py-4 text-right tabular-nums">{{ row.pending_count }}</td>
                  <td class="whitespace-nowrap px-4 py-4 text-right tabular-nums">{{ decimal(row.pending_amount) }}</td>
                  <td class="whitespace-nowrap px-4 py-4 text-right tabular-nums">{{ decimal(row.balance) }}</td>
                  <td class="whitespace-nowrap px-4 py-4">
                    <span :class="row.status === 'active' ? 'text-green-600 dark:text-green-400' : 'text-gray-500'">{{ t(row.deleted ? 'securityPolicyRanking.deletedUser' : row.status === 'active' ? 'common.enabled' : 'common.disabled') }}</span>
                    <div v-if="row.auto_disable_count !== undefined" class="mt-1 text-xs text-gray-500">{{ t('securityPolicyRanking.disableCount', { count: row.auto_disable_count }) }}</div>
                  </td>
                  <td class="whitespace-nowrap px-4 py-4 text-right">
                    <button class="btn btn-secondary btn-sm" :disabled="deductionBusy || savingSettings || !settingsReady || row.deleted || !row.pending_count || !!rankingError" :data-testid="`ranking-deduct-${row.user_id}`" @click="openPreview(row)">{{ previewingUser === row.user_id ? t('securityPolicyRanking.previewing') : t('securityPolicyRanking.deduct') }}</button>
                  </td>
                </template>
              </tr>
            </tbody>
          </table>
        </div>
        <div v-if="ranking && ranking.total > 0" class="flex flex-wrap items-center justify-between border-t border-gray-200 dark:border-dark-700">
          <label class="flex items-center gap-2 p-4 text-sm text-gray-600 dark:text-gray-300">{{ t('pagination.perPage') }}<select :value="pageSize" class="input w-24" data-testid="ranking-page-size" @change="changePageSize(Number(($event.target as HTMLSelectElement).value))"><option v-for="size in [10, 20, 50, 100]" :key="size" :value="size">{{ size }}</option></select></label>
          <Pagination class="min-w-0 flex-1 !border-t-0" :page="page" :page-size="pageSize" :total="ranking.total" :show-page-size-selector="false" @update:page="changePage" @update:page-size="changePageSize" />
        </div>
      </section>
    </div>

    <BaseDialog :show="isAdmin && !!preview" :title="t('securityPolicyRanking.confirmTitle')" :close-on-escape="!deducting" :close-on-click-outside="!deducting" :show-close-button="!deducting" @close="closePreview">
      <div v-if="preview" class="space-y-4" data-testid="ranking-deduction-preview">
        <p class="text-sm text-gray-600 dark:text-gray-300">{{ t('securityPolicyRanking.confirmDescription') }}</p>
        <dl class="space-y-3 text-sm">
          <div><dt class="text-gray-500">{{ t('securityPolicyRanking.user') }}</dt><dd class="mt-1 break-all font-medium">{{ preview.username || `#${preview.user_id}` }} · {{ preview.email }} · #{{ preview.user_id }}</dd></div>
          <div><dt class="text-gray-500">{{ t('securityPolicyRanking.range') }}</dt><dd class="mt-1">{{ preview.start_date }} ~ {{ preview.end_date }} · {{ preview.timezone }}</dd></div>
          <div><dt class="text-gray-500">{{ t('securityPolicyRanking.balance') }}</dt><dd class="mt-1 tabular-nums">{{ decimal(preview.balance) }}</dd></div>
          <div class="rounded-lg bg-amber-50 p-4 dark:bg-amber-900/20"><dt class="text-amber-800 dark:text-amber-300">{{ t('securityPolicyRanking.previewAmount') }}</dt><dd class="mt-2 text-lg font-semibold tabular-nums text-amber-900 dark:text-amber-200" data-testid="ranking-preview-formula">{{ t('securityPolicyRanking.previewFormula', { count: preview.trigger_count, multiplier: decimal(preview.multiplier), amount: decimal(preview.amount) }) }}</dd></div>
        </dl>
        <p class="text-xs text-gray-500">{{ t('securityPolicyRanking.snapshotHint') }}</p>
        <p class="text-xs text-gray-500">{{ t('securityPolicyRanking.expires', { time: dateTime(preview.expires_at) }) }}</p>
        <div v-if="deductionError" class="space-y-2 text-sm text-red-600 dark:text-red-400" role="alert"><p>{{ deductionError }}</p><p>{{ t('securityPolicyRanking.retryHint') }}</p></div>
      </div>
      <template #footer>
        <div class="flex justify-end gap-2"><button class="btn btn-secondary" :disabled="deducting" @click="closePreview">{{ t('common.cancel') }}</button><button class="btn btn-primary" :disabled="deducting || !preview?.trigger_count" data-testid="ranking-confirm-deduct" @click="confirmDeduction">{{ deducting ? t('common.processing') : deductionError ? t('securityPolicyRanking.retryDeduct') : t('securityPolicyRanking.confirmDeduct') }}</button></div>
      </template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { securityPolicyRankingAPI } from '@/api/admin/securityPolicyRanking'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { SecurityPolicyDeductionPreview, SecurityPolicyRanking, SecurityPolicyRankingRange, SecurityPolicyRankingSettings, SecurityPolicyRankingUser } from '@/types/securityPolicyRanking'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import DateRangePicker from '@/components/common/DateRangePicker.vue'
import Pagination from '@/components/common/Pagination.vue'

const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const isAdmin = computed(() => authStore.isAdmin)
const localDate = () => {
  const now = new Date()
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`
}
const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
const startDate = ref(localDate())
const endDate = ref(startDate.value)
const followToday = ref(true)
const page = ref(1)
const pageSize = ref(50)
const autoRefresh = ref(true)
const refreshSeconds = ref<number | string>(5)
const validInterval = computed(() => Number.isInteger(refreshSeconds.value) && Number(refreshSeconds.value) >= 1 && Number(refreshSeconds.value) <= 3600)
const ranking = ref<SecurityPolicyRanking | null>(null)
const refreshing = ref(false)
const rankingError = ref('')
const rangeError = ref('')
const settings = reactive<SecurityPolicyRankingSettings>({ multiplier: '6.7', auto_disable_enabled: false, auto_disable_threshold: 10, reset_on_manual_enable: true })
const savedSettings = ref<SecurityPolicyRankingSettings | null>(null)
const settingsReady = ref(false)
const loadingSettings = ref(false)
const savingSettings = ref(false)
const settingsError = ref('')
const preview = ref<SecurityPolicyDeductionPreview | null>(null)
const previewingUser = ref<number | null>(null)
const deducting = ref(false)
const deductionError = ref('')
const deductionBusy = computed(() => deducting.value || previewingUser.value !== null || !!preview.value)
let mounted = false
let timer: ReturnType<typeof setTimeout> | undefined
let requestController: AbortController | undefined
let previewController: AbortController | undefined
let revision = 0
let refreshQueued = false
const decimal = (value?: string) => value === undefined ? '—' : value.includes('.') ? value.replace(/0+$/, '').replace(/\.$/, '') : value
const dateTime = (value: string) => new Date(value).toLocaleString()
const range = (): SecurityPolicyRankingRange => ({ start_date: startDate.value, end_date: endDate.value, timezone })
const visible = () => document.visibilityState !== 'hidden'
const errorMessage = (error: unknown) => extractApiErrorMessage(error, t('common.error'))
const stopTimer = () => { clearTimeout(timer); timer = undefined }

function schedule() {
  stopTimer()
  if (!mounted || !visible() || !autoRefresh.value || !validInterval.value || refreshing.value) return
  timer = setTimeout(() => { void refresh() }, Number(refreshSeconds.value) * 1000)
}

// One list request at a time. Changed filters cancel the old request and queue
// only the latest range; late responses cannot repopulate the wrong date range.
async function refresh() {
  stopTimer()
  if (!mounted || !visible() || rangeError.value) return
  if (followToday.value && startDate.value !== localDate()) {
    startDate.value = localDate()
    endDate.value = startDate.value
    page.value = 1
    ranking.value = null
    revision++
    requestController?.abort()
  }
  if (refreshing.value) { refreshQueued = true; return }
  const currentRevision = revision
  const controller = new AbortController()
  requestController = controller
  refreshing.value = true
  try {
    const params = { ...range(), page: page.value, page_size: pageSize.value }
    const { data } = await (isAdmin.value ? securityPolicyRankingAPI.getRanking(params, controller.signal) : securityPolicyRankingAPI.getPublicRanking(params, controller.signal))
    if (!mounted || controller.signal.aborted || currentRevision !== revision) return
    ranking.value = data
    rankingError.value = ''
    const lastPage = Math.max(1, Math.ceil(data.total / pageSize.value))
    if (page.value > lastPage) { page.value = lastPage; refreshQueued = true }
  } catch (error) {
    if (mounted && !controller.signal.aborted && currentRevision === revision) rankingError.value = errorMessage(error)
  } finally {
    refreshing.value = false
    if (requestController === controller) requestController = undefined
    if (mounted && visible() && refreshQueued) { refreshQueued = false; void refresh() }
    else schedule()
  }
}

function resetList() {
  revision++
  requestController?.abort()
  previewController?.abort()
  ranking.value = null
  rankingError.value = ''
  if (!deducting.value) preview.value = null
  void refresh()
}
function validDate(value: string) {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return false
  const parsed = new Date(`${value}T12:00:00Z`)
  return !Number.isNaN(parsed.getTime()) && parsed.toISOString().slice(0, 10) === value
}
function changeRange(value: { startDate: string; endDate: string; preset: string | null }) {
  if (deducting.value) return
  if (!validDate(value.startDate) || !validDate(value.endDate) || value.startDate > value.endDate) {
    rangeError.value = t('securityPolicyRanking.invalidRange')
    stopTimer()
    return
  }
  rangeError.value = ''
  startDate.value = value.startDate
  endDate.value = value.endDate
  followToday.value = value.preset === 'today'
  page.value = 1
  resetList()
}
function changePage(value: number) { page.value = value; resetList() }
function changePageSize(value: number) { pageSize.value = Math.min(100, Math.max(10, Number.isFinite(value) ? Math.floor(value) : 50)); page.value = 1; resetList() }

async function loadSettings() {
  if (!isAdmin.value || loadingSettings.value) return
  loadingSettings.value = true
  settingsError.value = ''
  try {
    const { data } = await securityPolicyRankingAPI.getSettings()
    if (!mounted || !isAdmin.value) return
    savedSettings.value = { ...data }
    Object.assign(settings, data)
    settingsReady.value = true
  } catch (error) { if (mounted) settingsError.value = errorMessage(error) }
  finally { loadingSettings.value = false }
}
async function saveSettings() {
  if (!isAdmin.value || !settingsReady.value || savingSettings.value || deductionBusy.value) return
  const multiplier = settings.multiplier.trim()
  if (!/^\d+(\.\d{1,6})?$/.test(multiplier) || Number(multiplier) < 0.000001 || Number(multiplier) > 1000) {
    settingsError.value = t('securityPolicyRanking.invalidMultiplier'); return
  }
  if (!Number.isInteger(settings.auto_disable_threshold) || settings.auto_disable_threshold < 1 || settings.auto_disable_threshold > 1000000000) {
    settingsError.value = t('securityPolicyRanking.invalidThreshold'); return
  }
  savingSettings.value = true
  settingsError.value = ''
  try {
    const { data } = await securityPolicyRankingAPI.saveSettings({ ...settings, multiplier })
    if (!mounted) return
    savedSettings.value = { ...data }
    Object.assign(settings, data)
    appStore.showSuccess(t('securityPolicyRanking.settingsSaved'))
    resetList()
  } catch (error) { if (mounted) settingsError.value = errorMessage(error) }
  finally { savingSettings.value = false }
}

async function openPreview(row: SecurityPolicyRankingUser) {
  if (!isAdmin.value || !row.user_id || deductionBusy.value || savingSettings.value || !settingsReady.value || row.deleted || !row.pending_count) return
  const controller = new AbortController()
  const currentRevision = revision
  previewController = controller
  previewingUser.value = row.user_id
  deductionError.value = ''
  try {
    const { data } = await securityPolicyRankingAPI.preview(row.user_id, range(), controller.signal)
    if (!mounted || controller.signal.aborted || revision !== currentRevision) return
    if (data.trigger_count < 1) { appStore.showError(t('securityPolicyRanking.noPending')); void refresh(); return }
    preview.value = data
  } catch (error) { if (mounted && !controller.signal.aborted) appStore.showError(errorMessage(error)) }
  finally { previewingUser.value = null; if (previewController === controller) previewController = undefined }
}
function closePreview() {
  if (deducting.value) return
  preview.value = null
  deductionError.value = ''
}
async function confirmDeduction() {
  if (!isAdmin.value || !preview.value || deducting.value) return
  // Keep this ID on every failure, including an unknown network outcome.
  // The server returns the original result if this preview already completed.
  const currentPreview = preview.value
  deducting.value = true
  deductionError.value = ''
  try {
    const { data } = await securityPolicyRankingAPI.deduct(currentPreview.user_id, currentPreview.id)
    if (!mounted) return
    appStore.showSuccess(t('securityPolicyRanking.deductSuccess', { amount: decimal(data.amount), count: data.trigger_count }))
    preview.value = null
    resetList()
  } catch (error) { if (mounted) deductionError.value = errorMessage(error) }
  finally { deducting.value = false }
}
function onVisibilityChange() {
  stopTimer()
  if (!visible()) {
    revision++
    refreshQueued = false
    requestController?.abort()
    previewController?.abort()
  } else if (autoRefresh.value && validInterval.value) void refresh()
}
watch([autoRefresh, refreshSeconds], schedule)
watch(isAdmin, () => {
  settingsReady.value = false
  savedSettings.value = null
  closePreview()
  resetList()
  if (isAdmin.value) void loadSettings()
})
onMounted(() => {
  mounted = true
  document.addEventListener('visibilitychange', onVisibilityChange)
  void refresh()
  if (isAdmin.value) void loadSettings()
})
onUnmounted(() => {
  mounted = false
  revision++
  refreshQueued = false
  stopTimer()
  requestController?.abort()
  previewController?.abort()
  document.removeEventListener('visibilitychange', onVisibilityChange)
})
</script>
