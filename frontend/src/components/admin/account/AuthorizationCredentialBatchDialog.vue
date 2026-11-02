<template>
  <BaseDialog :show="show" :title="t('authorization.batchSupplement')" width="wide" @close="close">
    <form id="authorization-batch-supplement" class="space-y-4" autocomplete="off" @submit.prevent="submit">
      <p class="text-sm leading-6 text-gray-600 dark:text-dark-300">{{ t('authorization.batchSupplementHint') }}</p>
      <label class="block">
        <span class="input-label">{{ t('authorization.accountInput') }}</span>
        <textarea v-model="raw" class="input min-h-48 font-mono text-sm" rows="8" :disabled="saving" :placeholder="t('authorization.batchSupplementPlaceholder')" :maxlength="2 * 1024 * 1024" spellcheck="false" autocomplete="off" autocapitalize="off" />
      </label>
      <p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('authorization.batchSupplementPrivacy') }}</p>
      <p v-if="saving" role="status" class="text-sm text-primary-600 dark:text-primary-300">{{ t('authorization.batchSupplementSaving') }}</p>
      <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
      <p v-if="notice" role="status" class="text-sm text-gray-600 dark:text-dark-300">{{ notice }}</p>
      <div v-if="results.length" class="max-h-72 overflow-auto rounded-xl border border-gray-200 dark:border-dark-700">
        <table class="w-full text-left text-xs">
          <thead class="sticky top-0 bg-gray-50 dark:bg-dark-900"><tr><th class="px-3 py-2">{{ t('authorization.batchLine') }}</th><th class="px-3 py-2">{{ t('authorization.email') }}</th><th class="px-3 py-2">{{ t('authorization.status') }}</th></tr></thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
            <tr v-for="result in results" :key="result.line">
              <td class="px-3 py-2 tabular-nums">{{ result.line }}</td>
              <td class="max-w-64 break-all px-3 py-2">{{ result.email || '—' }}<span v-if="result.account_id" class="ml-1 text-gray-400">#{{ result.account_id }}</span></td>
              <td class="px-3 py-2" :class="result.status === 'saved' ? 'text-emerald-700 dark:text-emerald-300' : 'text-amber-700 dark:text-amber-300'">{{ t('authorization.batchResults.' + result.status) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </form>
    <template #footer>
      <button v-if="saving" type="button" class="btn btn-secondary" @click="cancel">{{ t('authorization.batchStop') }}</button>
      <button v-else type="button" class="btn btn-secondary" @click="close">{{ t('common.close') }}</button>
      <button type="submit" form="authorization-batch-supplement" class="btn btn-primary" :disabled="saving || !raw.trim()">{{ t('authorization.saveCredentials') }}</button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { accountAuthorizationAPI, type AuthorizationSupplementResult } from '@/api/admin/accountAuthorization'
import { extractApiErrorMessage } from '@/utils/apiError'

const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{ close: []; saved: [] }>()
const { t } = useI18n()
const raw = ref('')
const saving = ref(false)
const error = ref('')
const notice = ref('')
const results = ref<AuthorizationSupplementResult[]>([])
let request: AbortController | null = null
let version = 0

function abort() {
  version++
  request?.abort()
  request = null
  raw.value = ''
  saving.value = false
}
function cancel() {
  abort()
  error.value = ''
  notice.value = t('authorization.batchSupplementCancelled')
  emit('saved')
}
function close() {
  const wasSaving = saving.value
  abort()
  if (wasSaving) emit('saved')
  emit('close')
}
async function submit() {
  if (saving.value || !raw.value.trim()) return
  error.value = ''; notice.value = ''; results.value = []
  const lines = raw.value.split(/\r?\n/).filter(line => line.trim()).length
  if (lines > 200 || new TextEncoder().encode(raw.value).length > 2 * 1024 * 1024) {
    error.value = t('authorization.batchSupplementLimit')
    return
  }
  const current = new AbortController()
  const generation = ++version
  request = current
  saving.value = true
  let value = raw.value
  raw.value = ''
  try {
    const result = await accountAuthorizationAPI.supplementBatch(value, current.signal)
    if (generation !== version || current.signal.aborted || !props.show) return
    results.value = result.items
    notice.value = t('authorization.batchSupplementComplete', { saved: result.saved, total: result.items.length })
    if (result.cancelled) notice.value += ' ' + t('authorization.batchSupplementCancelled')
    if (result.saved) emit('saved')
  } catch (e) {
    if (generation !== version || current.signal.aborted || !props.show) return
    error.value = extractApiErrorMessage(e, t('authorization.saveFailed'))
    notice.value = t('authorization.batchSupplementUnconfirmed')
    emit('saved')
  } finally {
    value = ''
    if (generation === version) { saving.value = false; request = null }
  }
}
function visibility() { if (document.hidden && props.show) close() }
watch(() => props.show, () => { abort(); error.value = ''; notice.value = ''; results.value = [] })
onMounted(() => document.addEventListener('visibilitychange', visibility))
onUnmounted(() => { abort(); document.removeEventListener('visibilitychange', visibility) })
</script>
