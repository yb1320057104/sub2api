<template>
  <BaseDialog :show="show" :title="t('admin.proxyGroups.manage')" width="wide" :close-on-escape="!busy" :close-on-click-outside="!busy" @close="close">
    <div class="space-y-4">
      <p class="text-sm text-gray-500">{{ t('admin.proxyGroups.manageHint') }}</p>
      <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">{{ error }}</p>
      <p v-if="loading" class="text-sm text-gray-500">{{ t('common.loading') }}</p>
      <div v-else class="max-h-72 space-y-2 overflow-y-auto">
        <div v-for="group in groups" :key="group.id" class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
          <div class="flex items-center justify-between gap-3">
            <div class="min-w-0"><p class="break-words font-medium">{{ group.name }}</p><p class="mt-1 text-xs text-gray-500">{{ t('admin.proxyGroups.counts', { total: group.proxy_count, active: group.active_proxy_count }) }}</p><p v-if="group.description" class="mt-1 break-words text-xs text-gray-500">{{ group.description }}</p></div>
            <div class="flex shrink-0 gap-2"><button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="edit(group)">{{ t('common.edit') }}</button><button type="button" class="btn btn-danger btn-sm" :disabled="busy" @click="pendingDelete = group">{{ t('common.delete') }}</button></div>
          </div>
          <div v-if="pendingDelete?.id === group.id" class="mt-3 space-y-2 rounded-lg bg-amber-50 p-3 dark:bg-amber-900/20">
            <p class="text-sm">{{ t('admin.proxyGroups.deleteConfirm', { name: group.name }) }}</p>
            <div class="flex gap-2"><button type="button" class="btn btn-danger btn-sm" :disabled="busy" @click="remove(group.id)">{{ t('common.confirm') }}</button><button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="pendingDelete = null">{{ t('common.cancel') }}</button></div>
          </div>
        </div>
        <p v-if="groups.length === 0" class="py-4 text-sm text-gray-500">{{ t('admin.proxyGroups.empty') }}</p>
      </div>
      <form class="space-y-3 border-t border-gray-200 pt-4 dark:border-dark-600" @submit.prevent="save">
        <h3 class="font-medium">{{ editingID ? t('admin.proxyGroups.edit') : t('admin.proxyGroups.create') }}</h3>
        <label class="input-label">{{ t('admin.proxyGroups.name') }}<input v-model="form.name" data-testid="proxy-group-name" class="input mt-1" maxlength="100" required :disabled="busy" /></label>
        <label class="input-label">{{ t('admin.proxyGroups.description') }}<input v-model="form.description" data-testid="proxy-group-description" class="input mt-1" maxlength="1000" :disabled="busy" /></label>
        <div class="flex gap-2"><button type="submit" class="btn btn-primary" :disabled="busy || loading || !form.name.trim()">{{ busy ? t('common.processing') : t('common.save') }}</button><button v-if="editingID" type="button" class="btn btn-secondary" :disabled="busy" @click="resetForm">{{ t('common.cancel') }}</button></div>
      </form>
    </div>
    <template #footer><button class="btn btn-secondary" :disabled="busy" @click="close">{{ t('common.close') }}</button></template>
  </BaseDialog>
</template>
<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { createProxyGroup, deleteProxyGroup, listProxyGroups, proxyGroupErrorMessage, updateProxyGroup, type ProxyGroup } from '@/api/admin/proxyGroups'

const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{ close: []; changed: [] }>()
const { t } = useI18n()
const groups = ref<ProxyGroup[]>([])
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const editingID = ref<number | null>(null)
const pendingDelete = ref<ProxyGroup | null>(null)
const form = reactive({ name: '', description: '' })
let loadVersion = 0

function resetForm() { editingID.value = null; form.name = ''; form.description = '' }
function close() { if (!busy.value) emit('close') }
function edit(group: ProxyGroup) { if (busy.value) return; editingID.value = group.id; form.name = group.name; form.description = group.description; pendingDelete.value = null }
async function load() {
  const version = ++loadVersion
  loading.value = true
  try { const result = await listProxyGroups(); if (version === loadVersion) groups.value = result }
  catch { if (version === loadVersion) error.value = t('admin.proxyGroups.loadFailed') }
  finally { if (version === loadVersion) loading.value = false }
}
watch(() => props.show, show => {
  if (show) { error.value = ''; pendingDelete.value = null; resetForm(); void load() }
  else { loadVersion++; loading.value = false }
}, { immediate: true })
async function save() {
  if (busy.value || loading.value || !form.name.trim()) return
  busy.value = true; error.value = ''
  try {
    const payload = { name: form.name.trim(), description: form.description.trim() }
    if (editingID.value) await updateProxyGroup(editingID.value, payload)
    else await createProxyGroup(payload)
    resetForm(); emit('changed'); await load()
  } catch (cause) { error.value = proxyGroupErrorMessage(cause, t('admin.proxyGroups.saveFailed')) }
  finally { busy.value = false }
}
async function remove(id: number) {
  if (busy.value) return
  busy.value = true; error.value = ''
  try { await deleteProxyGroup(id); if (editingID.value === id) resetForm(); pendingDelete.value = null; emit('changed'); await load() }
  catch (cause) { error.value = proxyGroupErrorMessage(cause, t('admin.proxyGroups.deleteFailed')) }
  finally { busy.value = false }
}
</script>
