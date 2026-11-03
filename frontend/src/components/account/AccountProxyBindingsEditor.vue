<template>
  <section class="space-y-3 rounded-xl border border-gray-200 bg-gray-50/60 p-4 dark:border-dark-600 dark:bg-dark-800/40">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <div><p class="text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.accounts.multiProxy.title') }}</p><p class="mt-1 text-xs text-gray-500">{{ t('admin.accounts.multiProxy.hint') }}</p></div>
      <button v-if="modelValue.length" type="button" class="btn btn-secondary btn-sm" :disabled="disabled" @click="emit('update:modelValue', [])">{{ t('admin.accounts.multiProxy.clear') }}</button>
    </div>
    <Select :model-value="null" :options="availableOptions" :placeholder="t('admin.accounts.multiProxy.add')" :disabled="disabled || modelValue.length >= 100" :searchable="true" :aria-label="t('admin.accounts.multiProxy.add')" @update:model-value="addProxy" />
    <p v-if="!modelValue.length" class="rounded-lg border border-dashed border-gray-300 p-4 text-center text-sm text-gray-500 dark:border-dark-600">{{ t('admin.accounts.multiProxy.direct') }}</p>
    <div v-for="(binding, index) in modelValue" :key="binding.proxy_id" class="grid gap-3 rounded-lg border border-gray-200 bg-white p-3 dark:border-dark-600 dark:bg-dark-800 sm:grid-cols-[minmax(0,1fr)_110px_110px_auto]" :data-testid="`proxy-binding-${binding.proxy_id}`">
      <div class="min-w-0 self-center"><p class="truncate text-sm font-medium text-gray-900 dark:text-white" :title="proxyName(binding.proxy_id)">{{ proxyName(binding.proxy_id) }}</p><p class="mt-1 text-xs text-gray-500">{{ t('admin.accounts.multiProxy.currentAccounts', { count: proxyByID(binding.proxy_id)?.account_count ?? '—' }) }}</p></div>
      <label class="text-xs text-gray-500">{{ t('admin.accounts.multiProxy.concurrency') }}<input class="input mt-1 w-full" type="number" min="0" max="10000" step="1" required :disabled="disabled" :value="binding.max_concurrency" :aria-label="`${proxyName(binding.proxy_id)} ${t('admin.accounts.multiProxy.concurrency')}`" @input="updateLimit(index, 'max_concurrency', $event)" /></label>
      <label class="text-xs text-gray-500">{{ t('admin.accounts.multiProxy.accountsLimit') }}<input class="input mt-1 w-full" type="number" min="0" max="10000" step="1" required :disabled="disabled" :value="binding.max_accounts" :aria-label="`${proxyName(binding.proxy_id)} ${t('admin.accounts.multiProxy.accountsLimit')}`" @input="updateLimit(index, 'max_accounts', $event)" /></label>
      <button class="self-end rounded-lg p-2 text-sm text-red-600 hover:bg-red-50 disabled:opacity-50 dark:hover:bg-red-900/20" type="button" :disabled="disabled" :aria-label="`${t('common.remove')} ${proxyName(binding.proxy_id)}`" @click="removeProxy(index)">{{ t('common.remove') }}</button>
    </div>
    <p class="text-xs leading-5 text-gray-500">{{ t('admin.accounts.multiProxy.sharedLimits') }}</p>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import type { AccountProxyBindingInput, Proxy } from '@/types'
const props = defineProps<{ modelValue: AccountProxyBindingInput[]; proxies: Proxy[]; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: AccountProxyBindingInput[]] }>()
const { t } = useI18n()
const proxyByID = (id: number) => props.proxies.find(proxy => proxy.id === id)
const proxyName = (id: number) => proxyByID(id)?.name || `#${id}`
const availableOptions = computed(() => props.proxies.filter(proxy => proxy.status === 'active' && !props.modelValue.some(row => row.proxy_id === proxy.id)).map(proxy => ({ value: proxy.id, label: `${proxy.name}${proxy.country_code ? ` · ${proxy.country_code}` : ''} · ${t('admin.accounts.multiProxy.currentAccounts', { count: proxy.account_count ?? '—' })}` })))
function addProxy(value: unknown) {
  if (props.disabled || props.modelValue.length >= 100) return
  const proxy = proxyByID(Number(value))
  if (!proxy || proxy.status !== 'active' || props.modelValue.some(row => row.proxy_id === proxy.id)) return
  emit('update:modelValue', [...props.modelValue, { proxy_id: proxy.id, max_concurrency: proxy.max_concurrency ?? 0, max_accounts: proxy.max_accounts ?? 0 }])
}
function updateLimit(index: number, key: 'max_concurrency' | 'max_accounts', event: Event) {
  if (props.disabled) return
  const value = (event.target as HTMLInputElement).value
  emit('update:modelValue', props.modelValue.map((row, rowIndex) => rowIndex === index ? { ...row, [key]: value === '' ? NaN : Number(value) } : { ...row }))
}
function removeProxy(index: number) { if (!props.disabled) emit('update:modelValue', props.modelValue.filter((_, rowIndex) => rowIndex !== index)) }
</script>
