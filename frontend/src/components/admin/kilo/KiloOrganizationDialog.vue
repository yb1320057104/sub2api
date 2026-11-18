<script setup lang="ts">
import { onBeforeUnmount, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import * as api from '@/api/admin/kilo'
import type { KiloOrganization, KiloPrepared } from '@/api/admin/kilo'

const props = defineProps<{ prepared: KiloPrepared | null }>()
const emit = defineEmits<{ close: []; saved: [] }>()
const { t } = useI18n()
const organizations = shallowRef<KiloOrganization[]>([])
const selectedId = shallowRef('')
const loading = shallowRef(false)
const saving = shallowRef(false)
const error = shallowRef('')
let controller: AbortController | undefined
async function load() {
  controller?.abort()
  if (!props.prepared) return
  controller = new AbortController()
  const signal = controller.signal
  loading.value = true
  error.value = ''
  try {
    const items = await api.getOrganizations(props.prepared.id, signal)
    if (!signal.aborted) organizations.value = items
  } catch (cause) {
    if (!signal.aborted && !api.isKiloRequestCanceled(cause)) error.value = api.kiloErrorMessage(cause, t('kilo.errors.organizations'))
  } finally { if (!signal.aborted) loading.value = false }
}
watch(() => props.prepared, item => {
  controller?.abort(); organizations.value = []; selectedId.value = item?.organization_id ?? ''; error.value = ''
  if (item) void load()
}, { immediate: true })
async function save() {
  if (!props.prepared || saving.value || loading.value || error.value || !organizations.value.some(item => item.id === selectedId.value)) return
  saving.value = true
  controller = new AbortController()
  try {
    await api.setOrganization(props.prepared.id, selectedId.value, controller.signal)
    emit('saved')
  } catch (cause) {
    if (!api.isKiloRequestCanceled(cause)) error.value = api.kiloErrorMessage(cause, t('kilo.errors.organizationSave'))
  } finally { saving.value = false }
}
onBeforeUnmount(() => controller?.abort())
</script>

<template>
  <BaseDialog :show="!!prepared" :title="t('kilo.organization.title')" :show-close-button="!saving" :close-on-escape="!saving" @close="!saving && emit('close')">
    <div class="space-y-4"><p class="text-sm leading-6 text-gray-600 dark:text-dark-300">{{ t('kilo.organization.description', { name: prepared?.name ?? '' }) }}</p><div><label for="kilo-organization" class="mb-2 block text-sm font-medium">{{ t('kilo.organization.label') }}</label><Select id="kilo-organization" v-model="selectedId" :options="organizations.map(item => ({ value: item.id, label: item.id === '' ? t('kilo.prepared.personal') : item.name }))" :disabled="loading || saving" :loading="loading" :aria-label="t('kilo.organization.label')" :placeholder="t('kilo.organization.placeholder')" :empty-text="t('kilo.organization.empty')" /></div><p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('kilo.organization.hint') }}</p><p v-if="error" class="text-sm text-red-600 dark:text-red-400" role="alert">{{ error }} <button type="button" class="underline" :disabled="loading || saving" @click="load">{{ t('common.refresh') }}</button></p></div>
    <template #footer><button type="button" class="btn btn-secondary" :disabled="saving" @click="emit('close')">{{ t('common.cancel') }}</button><button type="button" class="btn btn-primary" :disabled="saving || loading || !!error || !organizations.some(item => item.id === selectedId)" @click="save">{{ saving ? t('common.saving') : t('common.save') }}</button></template>
  </BaseDialog>
</template>
