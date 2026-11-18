<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import KiloProxySelect from './KiloProxySelect.vue'
import KiloProxyPreflight from './KiloProxyPreflight.vue'
import Select from '@/components/common/Select.vue'
import type { KiloCandidate, KiloProxy, KiloWorkspaceMode } from '@/api/admin/kilo'
import type { KiloOnboardingDraft } from './useKiloOnboardingForm'

const props = defineProps<{
  modelValue: KiloOnboardingDraft
  source?: KiloCandidate
  proxies: KiloProxy[]
  excludedProxyIds: number[]
  disabled: boolean
}>()
const emit = defineEmits<{ 'update:modelValue': [value: KiloOnboardingDraft]; remove: [] }>()
const { t } = useI18n()
const prefix = computed(() => `kilo-onboarding-${props.modelValue.source_account_id}`)
const sourceName = computed(() => props.source?.name || `#${props.modelValue.source_account_id}`)
const workspaceOptions = computed(() => (['auto', 'personal', 'business'] as const).map(value => ({ value, label: t(`kilo.workspaceMode.${value}`) })))
function update(patch: Partial<KiloOnboardingDraft>) { emit('update:modelValue', { ...props.modelValue, ...patch }) }
function text(event: Event) { return (event.target as HTMLInputElement | HTMLTextAreaElement).value }
function materialLabel(value?: boolean) { return t(value === undefined ? 'kilo.onboarding.materialUnknown' : value ? 'kilo.onboarding.materialSaved' : 'kilo.onboarding.materialAbsent') }
</script>

<template>
  <article class="space-y-4 rounded-xl border border-gray-200 p-4 dark:border-dark-700">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div class="min-w-0"><h4 class="break-words text-sm font-semibold">{{ sourceName }} <span class="font-normal text-gray-400">#{{ modelValue.source_account_id }}</span></h4><p class="mt-1 break-all text-xs text-gray-500 dark:text-dark-400">{{ source?.email || t('common.notAvailable') }}</p><p class="mt-1 text-xs text-gray-500 dark:text-dark-400">{{ t('kilo.onboarding.materialSummary', { password: materialLabel(source?.has_password), totp: materialLabel(source?.has_totp) }) }}</p><p v-if="source?.onboarding_reason" class="mt-2 break-words text-xs text-amber-700 dark:text-amber-300">{{ source.onboarding_reason }}</p></div>
      <button type="button" class="btn btn-secondary btn-sm" :disabled="disabled" :aria-label="t('kilo.onboarding.removeSource', { name: sourceName })" @click="emit('remove')">{{ t('common.remove') }}</button>
    </div>
    <div class="grid gap-4 sm:grid-cols-2">
      <div><label :for="`${prefix}-name`" class="mb-2 block text-sm font-medium">{{ t('kilo.onboarding.name') }}</label><input :id="`${prefix}-name`" :value="modelValue.name" type="text" class="input w-full" maxlength="200" required :disabled="disabled" @input="update({ name: text($event) })" /></div>
      <div><label :for="`${prefix}-workspace`" class="mb-2 block text-sm font-medium">{{ t('kilo.workspaceMode.label') }}</label><Select :id="`${prefix}-workspace`" :model-value="modelValue.workspace_mode" :options="workspaceOptions" :disabled="disabled" :aria-label="t('kilo.workspaceMode.label')" @update:model-value="update({ workspace_mode: $event as KiloWorkspaceMode })" /><p class="mt-2 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('kilo.workspaceMode.hint') }}</p></div>
      <details class="sm:col-span-2"><summary class="cursor-pointer text-sm text-gray-600 dark:text-dark-300">{{ t('kilo.workspaceMode.advanced') }}</summary><div class="mt-3"><label :for="`${prefix}-organization`" class="mb-2 block text-sm font-medium">{{ t('kilo.onboarding.organization') }}</label><input :id="`${prefix}-organization`" :value="modelValue.organization_id" type="text" class="input w-full" maxlength="100" :placeholder="t('kilo.onboarding.personalPlaceholder')" :disabled="disabled" @input="update({ organization_id: text($event) })" /><p class="mt-2 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('kilo.workspaceMode.organizationHint') }}</p></div></details>
      <div class="sm:col-span-2"><label :for="`${prefix}-login`" class="mb-2 block text-sm font-medium">{{ t('kilo.routing.loginProxy') }}</label><p v-if="source?.login_proxy_id" class="text-sm text-gray-600 dark:text-dark-300">{{ t('kilo.onboarding.sourceFixedProxy', { id: source.login_proxy_id }) }}</p><KiloProxySelect v-else :id="`${prefix}-login`" :model-value="modelValue.login_proxy_id" :proxies="proxies" :excluded-ids="excludedProxyIds" :disabled="disabled" preflight @update:model-value="update({ login_proxy_id: $event })" /><KiloProxyPreflight v-if="source?.login_proxy_id" :proxy-id="source.login_proxy_id" :disabled="disabled" /></div>
      <div class="sm:col-span-2"><label :for="`${prefix}-models`" class="mb-2 block text-sm font-medium">{{ t('kilo.onboarding.models') }}</label><textarea :id="`${prefix}-models`" :value="modelValue.model_ids_input" class="input min-h-20 w-full font-mono text-xs" :placeholder="t('kilo.onboarding.modelsPlaceholder')" :disabled="disabled" spellcheck="false" :aria-describedby="`${prefix}-models-help`" @input="update({ model_ids_input: text($event) })" /><p :id="`${prefix}-models-help`" class="mt-2 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('kilo.onboarding.modelsHint') }}</p></div>
    </div>
  </article>
</template>
