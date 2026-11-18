<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import KiloProxyPreflight from './KiloProxyPreflight.vue'
import { getProxies, isKiloRequestCanceled, type KiloProxy } from '@/api/admin/kilo'

const props = defineProps<{ id: string; disabled?: boolean; accountId?: number | null; proxies?: KiloProxy[]; excludedIds?: number[]; preflight?: boolean }>()
const model = defineModel<number | null>({ required: true })
const { t } = useI18n()
const proxies = shallowRef<KiloProxy[]>([])
const options = computed(() => (props.proxies ?? proxies.value).filter(item => item.status === 'active').map(item => {
  const own = item.owned && !item.owner_deleted && item.owner_account_id === props.accountId
  const owner = !item.owned ? t('kilo.proxyUnowned') : own ? t('kilo.proxyOwn')
    : item.owner_account_id === null ? t('kilo.proxyOwnerSealed')
    : item.owner_deleted ? t('kilo.proxyOwnerDeleted', { id: item.owner_account_id })
    : t('kilo.proxyOwned', { id: item.owner_account_id, name: item.owner_account_name })
  return { value: item.id, label: `${item.name} #${item.id} · ${owner}`, disabled: item.owned && !own || !!props.excludedIds?.includes(item.id) }
}))
const loading = shallowRef(false)
const error = shallowRef('')
let controller: AbortController | undefined
async function load() {
  if (loading.value) return
  controller = new AbortController()
  loading.value = true
  error.value = ''
  try {
    const data = await getProxies(controller.signal)
    if (controller.signal.aborted) return
    proxies.value = data
    if (options.value.some(item => item.value === model.value && item.disabled)) model.value = null
  } catch (cause) {
    if (!isKiloRequestCanceled(cause)) error.value = t('kilo.errors.proxies')
  } finally { loading.value = false }
}
onMounted(() => { if (props.proxies === undefined) void load() })
onBeforeUnmount(() => controller?.abort())
</script>

<template>
  <div>
    <Select :id="props.id" v-model="model" :options="options" :disabled="disabled || loading" :loading="loading" :aria-label="t('kilo.proxy')" :placeholder="t('kilo.proxyPlaceholder')" :empty-text="t('kilo.noProxies')" searchable />
    <KiloProxyPreflight v-if="preflight" :proxy-id="model" :disabled="disabled || loading" />
    <p v-if="error" class="mt-2 text-xs text-red-600 dark:text-red-400" role="alert">{{ error }} <button type="button" class="underline" :disabled="disabled || loading" @click="load">{{ t('common.refresh') }}</button></p>
    <p v-else-if="!loading && !options.length" class="mt-2 text-xs text-gray-500 dark:text-dark-400">{{ t('kilo.noProxies') }} <router-link to="/admin/proxies" class="text-primary-600 underline dark:text-primary-400">{{ t('kilo.manageProxies') }}</router-link></p>
  </div>
</template>
