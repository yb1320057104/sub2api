<template>
  <BaseDialog :show="show" :title="t('admin.proxyGroups.move')" width="normal" :close-on-escape="!busy" :close-on-click-outside="!busy" @close="close">
    <form id="proxy-group-members" class="space-y-4" @submit.prevent="submit">
      <p class="text-sm text-gray-500">{{ t('admin.proxyGroups.moveHint', { count: ids.length }) }}</p>
      <Select v-model="groupID" :options="options" :disabled="busy || loading" :aria-label="t('admin.proxyGroups.group')" />
      <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    </form>
    <template #footer><div class="flex justify-end gap-2"><button class="btn btn-secondary" :disabled="busy" @click="close">{{ t('common.cancel') }}</button><button type="submit" form="proxy-group-members" class="btn btn-primary" :disabled="busy || loading || !valid">{{ busy ? t('common.processing') : t('common.save') }}</button></div></template>
  </BaseDialog>
</template>
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import { assignProxyGroupMembers, listProxyGroups, proxyGroupErrorMessage, type ProxyGroup } from '@/api/admin/proxyGroups'
import { useAppStore } from '@/stores/app'

const props = defineProps<{ show: boolean; proxyIds: number[] }>()
const emit = defineEmits<{ close: []; assigned: [ids: number[]] }>()
const { t } = useI18n()
const app = useAppStore()
const ids = ref<number[]>([])
const groupID = ref<number>(0)
const groups = ref<ProxyGroup[]>([])
const busy = ref(false)
const loading = ref(false)
const error = ref('')
let loadVersion = 0
const options = computed(() => [{ value: 0, label: t('admin.proxyGroups.ungrouped') }, ...groups.value.map(group => ({ value: group.id, label: group.name }))])
const valid = computed(() => ids.value.length > 0 && ids.value.length <= 5000 && (groupID.value === 0 || groups.value.some(group => group.id === groupID.value)))
watch(() => props.show, async show => {
  const version = ++loadVersion
  if (!show) { loading.value = false; return }
  ids.value = [...new Set(props.proxyIds)]; groupID.value = 0; error.value = ''; groups.value = []; loading.value = true
  try { const result = await listProxyGroups(); if (version === loadVersion) groups.value = result }
  catch { if (version === loadVersion) error.value = t('admin.proxyGroups.loadFailed') }
  finally { if (version === loadVersion) loading.value = false }
}, { immediate: true })
function close() { if (!busy.value) emit('close') }
async function submit() {
  if (busy.value || loading.value || !valid.value) return
  const selectedIDs = [...ids.value]
  busy.value = true; error.value = ''
  try { const result = await assignProxyGroupMembers(selectedIDs, groupID.value || null); app.showSuccess(t('admin.proxyGroups.moved', { count: result.proxy_count })); emit('assigned', selectedIDs); emit('close') }
  catch (cause) { error.value = proxyGroupErrorMessage(cause, t('admin.proxyGroups.moveFailed')) }
  finally { busy.value = false }
}
</script>
