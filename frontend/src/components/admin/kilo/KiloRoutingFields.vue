<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import type { KiloGroup, KiloProxyGroup, KiloRoutingOptions } from '@/api/admin/kilo'
import { kiloRequestProxyOptions } from './routing'

const props = withDefaults(defineProps<{ groups: KiloGroup[]; proxyGroups: KiloProxyGroup[]; disabled?: boolean; prefix?: string }>(), { prefix: 'kilo-import-routing' })
const routing = defineModel<KiloRoutingOptions>({ required: true })
const groupIds = defineModel<number[]>('groupIds', { required: true })
const { t } = useI18n()
const groupOptions = computed(() => props.groups.map(group => ({ value: group.id, label: group.name })))
const proxyOptions = computed(() => kiloRequestProxyOptions(props.proxyGroups, t))
const numericFields = [
  { key: 'proxies_per_account', label: 'kilo.batch.proxiesPerAccount', min: 1, max: 100 },
  { key: 'proxy_concurrency', label: 'kilo.batch.proxyConcurrency', min: 1, max: 10000 },
  { key: 'account_concurrency', label: 'kilo.batch.accountConcurrency', min: 1, max: 10000 },
  { key: 'priority', label: 'kilo.batch.priority', min: 0, max: 10000 }
] as const
function updateNumber(key: typeof numericFields[number]['key'], event: Event) {
  routing.value = { ...routing.value, [key]: (event.target as HTMLInputElement).valueAsNumber }
}
</script>

<template>
  <section class="space-y-4 rounded-xl border border-gray-200 p-4 dark:border-dark-700">
    <h3 class="text-sm font-semibold">{{ t('kilo.identity.requestSettings') }}</h3>
    <div class="grid gap-4 sm:grid-cols-2">
      <div><label :for="`${prefix}-groups`" class="mb-2 block text-sm font-medium">{{ t('kilo.batch.groups') }}</label><Select :id="`${prefix}-groups`" v-model="groupIds" :options="groupOptions" multiple :disabled="disabled" :aria-label="t('kilo.batch.groups')" :placeholder="t('kilo.batch.groupPlaceholder')" /></div>
      <div><label :for="`${prefix}-pool`" class="mb-2 block text-sm font-medium">{{ t('kilo.batch.proxyGroup') }}</label><Select :id="`${prefix}-pool`" :model-value="routing.proxy_group_id" :options="proxyOptions" :disabled="disabled" :aria-label="t('kilo.batch.proxyGroup')" :placeholder="t('kilo.batch.proxyPlaceholder')" @update:model-value="routing = { ...routing, proxy_group_id: Number($event) }" /></div>
      <div v-for="field in numericFields" :key="field.key"><label :for="`${prefix}-${field.key}`" class="mb-2 block text-sm font-medium">{{ t(field.label) }}</label><input :id="`${prefix}-${field.key}`" :value="routing[field.key]" type="number" class="input w-full" :min="field.min" :max="field.max" required :disabled="disabled" @input="updateNumber(field.key, $event)" /></div>
    </div>
    <p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('kilo.routing.requestHint') }}</p>
  </section>
</template>
