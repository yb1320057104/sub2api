<script setup lang="ts">
import { computed, onBeforeUnmount, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import KiloStatusBadge from './KiloStatusBadge.vue'
import { parseKiloImport } from './import'
import type { KiloImportPreview } from './import'
import { importPrepared, isKiloRequestCanceled } from '@/api/admin/kilo'
import type { KiloImportResult } from '@/api/admin/kilo'
import { KiloRequestRecoveryError, reserveKiloRequest } from './requestIdentity'

const props = defineProps<{ show: boolean; encryptionReady: boolean }>()
const emit = defineEmits<{ close: []; imported: [] }>()
const { t } = useI18n()
const input = shallowRef('')
const preview = shallowRef<KiloImportPreview | null>(null)
const result = shallowRef<KiloImportResult | null>(null)
const busy = shallowRef(false)
const error = shallowRef('')
const requestId = shallowRef('')
let controller: AbortController | undefined
let fileReadVersion = 0
const validRows = computed(() => preview.value?.rows.filter(row => row.item && !row.issue) ?? [])
const invalidCount = computed(() => (preview.value?.rows.length ?? 0) - validRows.value.length)
const canImport = computed(() => props.encryptionReady && validRows.value.length > 0 && invalidCount.value === 0 && !preview.value?.issue && !busy.value)
const resultRows = computed(() => result.value?.items.map(item => ({ ...item, sourceLine: preview.value?.rows[item.line - 1]?.line ?? item.line })) ?? [])

function clear() {
  fileReadVersion++
  input.value = ''
  preview.value = null
  result.value = null
  error.value = ''
  requestId.value = ''
}
watch(() => props.show, show => { if (!show) { controller?.abort(); clear() } })
function close() { if (!busy.value) emit('close') }
function makePreview() {
  error.value = ''
  result.value = null
  preview.value = parseKiloImport(input.value)
  requestId.value = ''
}
function editInput() {
  preview.value = null
  result.value = null
  requestId.value = ''
  error.value = ''
}
async function readFile(event: Event) {
  const element = event.target as HTMLInputElement
  const file = element.files?.[0]
  element.value = ''
  if (!file) return
  if (file.size > 2 * 1024 * 1024) { error.value = t('kilo.import.fileTooLarge'); return }
  const version = ++fileReadVersion
  try {
    const content = await file.text()
    if (version !== fileReadVersion || !props.show) return
    input.value = content
    editInput()
  } catch {
    if (version === fileReadVersion) error.value = t('kilo.import.fileError')
  }
}
async function submit() {
  if (!canImport.value) return
  busy.value = true
  error.value = ''
  controller = new AbortController()
  try {
    const items = validRows.value.flatMap(row => row.item ? [row.item] : [])
    const identity = await reserveKiloRequest('import', items)
    requestId.value = identity.requestId
    if (controller.signal.aborted) return
    result.value = await importPrepared({ request_id: identity.requestId, items }, controller.signal)
    identity.complete()
    input.value = ''
    // Import credentials are needed only until the authority returns its per-row receipt.
    preview.value = preview.value ? { ...preview.value, rows: preview.value.rows.map(({ item: _item, ...row }) => row) } : null
    emit('imported')
  } catch (cause) {
    if (!isKiloRequestCanceled(cause)) error.value = cause instanceof KiloRequestRecoveryError ? t('kilo.errors.requestRecovery') : t('kilo.errors.import')
  } finally {
    busy.value = false
  }
}
onBeforeUnmount(() => { controller?.abort(); clear() })
</script>

<template>
  <BaseDialog :show="show" :title="t('kilo.import.title')" width="wide" :close-on-escape="!busy" :show-close-button="!busy" @close="close">
    <div class="space-y-4">
      <p class="text-sm leading-6 text-gray-600 dark:text-dark-300">{{ t('kilo.import.description') }}</p>
      <p v-if="requestId && !result" class="break-all text-xs leading-5 text-gray-500 dark:text-dark-400" role="status">{{ t('kilo.requestIdentity', { id: requestId }) }}</p>
      <p v-if="!encryptionReady" class="rounded-lg bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-900/20 dark:text-amber-300" role="alert">{{ t('kilo.encryptionRequired') }}</p>
      <template v-if="!preview">
        <div>
          <label for="kilo-import-input" class="mb-2 block text-sm font-medium text-gray-700 dark:text-dark-200">{{ t('kilo.import.input') }}</label>
          <textarea id="kilo-import-input" v-model="input" class="input min-h-48 w-full font-mono text-sm" :placeholder="t('kilo.import.placeholder')" :disabled="busy" spellcheck="false" autocomplete="off" autocapitalize="off" data-1p-ignore data-lpignore="true" aria-describedby="kilo-import-help" />
          <p id="kilo-import-help" class="mt-2 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('kilo.import.privacy') }}</p>
        </div>
        <div>
          <label for="kilo-import-file" class="mb-2 block text-sm font-medium text-gray-700 dark:text-dark-200">{{ t('kilo.import.file') }}</label>
          <input id="kilo-import-file" type="file" accept=".json,.txt,application/json,text/plain" class="block w-full text-sm text-gray-600 file:mr-3 file:rounded-lg file:border-0 file:bg-gray-100 file:px-3 file:py-2 file:text-gray-700 focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500 dark:text-dark-300 dark:file:bg-dark-700 dark:file:text-dark-200" :disabled="busy" @change="readFile" />
        </div>
      </template>
      <template v-else>
        <p v-if="preview.issue" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300" role="alert">{{ t(`kilo.import.issues.${preview.issue}`) }}</p>
        <p v-else-if="!result" class="text-sm font-medium text-gray-700 dark:text-dark-200">{{ t('kilo.import.previewCount', { valid: validRows.length, invalid: invalidCount }) }}</p>
        <p v-if="result" class="rounded-lg bg-primary-50 p-3 text-sm text-primary-800 dark:bg-primary-900/20 dark:text-primary-200" role="status">{{ t('kilo.import.result', { imported: result.imported, duplicates: result.duplicates, failed: result.failed }) }}</p>
        <div v-if="preview.rows.length" class="max-h-80 overflow-auto rounded-lg border border-gray-200 dark:border-dark-700">
          <table class="w-full text-left text-sm">
            <thead class="sticky top-0 bg-gray-50 text-xs text-gray-600 dark:bg-dark-800 dark:text-dark-300">
              <tr><th scope="col" class="px-3 py-2">{{ t('kilo.import.line') }}</th><th scope="col" class="px-3 py-2">{{ t('common.name') }}</th><th scope="col" class="px-3 py-2">{{ t('common.email') }}</th><th scope="col" class="px-3 py-2">{{ t('common.status') }}</th></tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
              <tr v-for="row in preview.rows" :key="row.line">
                <td class="px-3 py-2 tabular-nums">{{ row.line }}</td><td class="max-w-52 break-words px-3 py-2">{{ row.name || t('common.notAvailable') }}</td><td class="max-w-52 break-words px-3 py-2 text-gray-500 dark:text-dark-400">{{ row.email || t('common.notAvailable') }}</td>
                <td class="px-3 py-2"><span v-if="row.issue" class="text-red-600 dark:text-red-400">{{ t(`kilo.import.issues.${row.issue}`) }}</span><span v-else>{{ t('kilo.import.valid') }}</span></td>
              </tr>
            </tbody>
          </table>
        </div>
        <ul v-if="resultRows.length" class="max-h-40 space-y-2 overflow-y-auto" :aria-label="t('kilo.import.receipt')">
          <li v-for="item in resultRows" :key="item.line" class="flex flex-wrap items-center gap-2 text-sm"><span>{{ t('kilo.import.rowNumber', { line: item.sourceLine }) }}</span><KiloStatusBadge :status="item.status" /><span v-if="item.id" class="text-gray-500 dark:text-dark-400">#{{ item.id }}</span><span v-if="item.message" class="break-words text-xs text-gray-500 dark:text-dark-400">{{ item.message }}</span></li>
        </ul>
        <p v-if="invalidCount && !result" class="text-sm text-red-600 dark:text-red-400">{{ t('kilo.import.fixErrors') }}</p>
      </template>
      <p v-if="error" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300" role="alert">{{ error }}</p>
    </div>
    <template #footer>
      <button type="button" class="btn btn-secondary" :disabled="busy" @click="close">{{ result ? t('common.close') : t('common.cancel') }}</button>
      <button v-if="preview && !result" type="button" class="btn btn-secondary" :disabled="busy" @click="editInput">{{ t('kilo.import.edit') }}</button>
      <button v-if="!preview" type="button" class="btn btn-primary" :disabled="!input.trim() || busy || !encryptionReady" @click="makePreview">{{ t('kilo.import.preview') }}</button>
      <button v-else-if="!result" type="button" class="btn btn-primary" :disabled="!canImport" @click="submit">{{ busy ? t('common.submitting') : t('kilo.import.confirm', { count: validRows.length }) }}</button>
    </template>
  </BaseDialog>
</template>
