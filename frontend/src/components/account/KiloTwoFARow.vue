<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import KiloProxySelect from '@/components/admin/kilo/KiloProxySelect.vue'
import KiloProxyPreflight from '@/components/admin/kilo/KiloProxyPreflight.vue'
import { isReusableKiloPrepared } from '@/components/admin/kilo/pool'
import type { KiloBindingStrategy, KiloPrepared, KiloProxy, KiloWorkspaceMode } from '@/api/admin/kilo'
import type { KiloTwoFADraft } from '@/composables/useKiloTwoFAImport'

const props = defineProps<{ modelValue: KiloTwoFADraft; index: number; strategy: KiloBindingStrategy; prepared: KiloPrepared[]; proxies: KiloProxy[]; excludedPreparedIds: number[]; excludedProxyIds: number[]; disabled: boolean; fixedPrepared?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: KiloTwoFADraft] }>()
const { t } = useI18n()
const prefix = computed(() => `kilo-two-fa-${props.index}`)
const reusable = computed(() => props.prepared.filter(isReusableKiloPrepared))
const workspaceOptions = computed(() => (['auto', 'personal', 'business'] as const).map(value => ({ value, label: t(`kilo.workspaceMode.${value}`) })))
const slot = computed(() => reusable.value.find(item => item.id === props.modelValue.prepared_id))
const slotOptions = computed(() => [
  { value: 0, label: t('kilo.twoFA.automaticSlot') },
  ...reusable.value.map(item => ({ value: item.id, label: `${item.name} #${item.id}`, disabled: props.excludedPreparedIds.includes(item.id) }))
])
function update(patch: Partial<KiloTwoFADraft>) { emit('update:modelValue', { ...props.modelValue, ...patch }) }
function text(event: Event) { return (event.target as HTMLInputElement | HTMLTextAreaElement).value }
function chooseSlot(value: unknown) {
  const prepared = reusable.value.find(item => item.id === Number(value))
  update({ prepared_id: prepared?.id ?? null, login_proxy_id: prepared?.login_proxy_id ?? null, organization_id: prepared?.organization_id ?? props.modelValue.organization_id })
}
</script>

<template>
  <article class="space-y-4 rounded-xl border border-gray-200 p-4 dark:border-dark-700">
    <h4 class="break-all text-sm font-semibold">{{ index + 1 }}. {{ modelValue.login.email }}</h4>
    <div class="grid gap-4 sm:grid-cols-2">
      <div><label :for="`${prefix}-name`" class="mb-2 block text-sm font-medium">{{ t('kilo.onboarding.name') }}</label><input :id="`${prefix}-name`" :value="modelValue.name" type="text" class="input w-full" maxlength="200" :disabled="disabled" @input="update({ name: text($event) })" /></div>
      <div><label :for="`${prefix}-workspace`" class="mb-2 block text-sm font-medium">{{ t('kilo.workspaceMode.label') }}</label><Select :id="`${prefix}-workspace`" :model-value="modelValue.workspace_mode" :options="workspaceOptions" :disabled="disabled" :aria-label="t('kilo.workspaceMode.label')" @update:model-value="update({ workspace_mode: $event as KiloWorkspaceMode })" /><p class="mt-2 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('kilo.workspaceMode.hint') }}</p></div>
      <div v-if="strategy === 'prefer_idle'" class="sm:col-span-2"><label :for="`${prefix}-slot`" class="mb-2 block text-sm font-medium">{{ t('kilo.twoFA.slot') }}</label><Select :id="`${prefix}-slot`" :model-value="modelValue.prepared_id || 0" :options="slotOptions" :disabled="disabled || fixedPrepared" :aria-label="t('kilo.twoFA.slot')" @update:model-value="chooseSlot" /><p class="mt-2 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('kilo.twoFA.slotHint') }}</p></div>
      <div class="sm:col-span-2"><label :for="`${prefix}-login`" class="mb-2 block text-sm font-medium">{{ t(slot ? 'kilo.routing.loginProxy' : 'kilo.twoFA.fallbackProxy') }}</label><p v-if="slot" class="text-sm text-gray-600 dark:text-dark-300">{{ t('kilo.routing.loginIdentity', { name: slot.login_proxy_name || '', id: slot.login_proxy_id }) }}</p><KiloProxySelect v-else :id="`${prefix}-login`" :model-value="modelValue.login_proxy_id" :proxies="proxies" :excluded-ids="excludedProxyIds" :disabled="disabled" preflight @update:model-value="update({ login_proxy_id: $event })" /><KiloProxyPreflight v-if="slot" :proxy-id="slot.login_proxy_id" :disabled="disabled" /><p class="mt-2 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('kilo.twoFA.fallbackHint') }}</p></div>
      <div class="sm:col-span-2"><label :for="`${prefix}-models`" class="mb-2 block text-sm font-medium">{{ t('kilo.onboarding.models') }}</label><textarea :id="`${prefix}-models`" :value="modelValue.models_input" class="input min-h-20 w-full font-mono text-xs" :placeholder="t('kilo.onboarding.modelsPlaceholder')" spellcheck="false" :disabled="disabled" @input="update({ models_input: text($event) })" /><p class="mt-2 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('kilo.onboarding.modelsHint') }}</p></div>
    </div>
  </article>
</template>
