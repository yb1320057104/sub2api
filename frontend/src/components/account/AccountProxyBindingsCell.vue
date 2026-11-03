<template>
  <div v-if="bindings.length" class="flex min-w-0 flex-col gap-1.5" :class="compact ? 'mt-1 max-w-44' : 'min-w-40'">
    <span v-if="account.parent_account_id" class="text-[11px] text-gray-500">{{ t('admin.accounts.multiProxy.inherited') }}</span>
    <div v-for="binding in bindings" :key="binding.proxy_id" class="min-w-0" :data-testid="`proxy-concurrency-${binding.proxy_id}`">
      <div class="flex items-center gap-2" :title="t('admin.accounts.multiProxy.sharedRuntime')">
        <span class="min-w-0 flex-1 truncate text-xs text-gray-600 dark:text-gray-300" :title="binding.proxy?.name || `#${binding.proxy_id}`">{{ binding.proxy?.name || `#${binding.proxy_id}` }}</span>
        <span class="shrink-0 rounded px-1.5 py-0.5 font-mono text-[11px] tabular-nums" :class="binding.max_concurrency > 0 && binding.current_concurrency != null && binding.current_concurrency >= binding.max_concurrency ? 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300' : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'">{{ binding.current_concurrency ?? '—' }} / {{ binding.max_concurrency === 0 ? '∞' : binding.max_concurrency }}</span>
      </div>
      <div v-if="!compact && binding.proxy?.expires_at" class="mt-1 flex items-center gap-1 text-[11px] text-gray-500"><span>{{ new Date(binding.proxy.expires_at).toLocaleString() }}</span><span :class="proxyExpiryBadgeClass(binding.proxy.expires_at, binding.proxy.status)">{{ expiryText(binding.proxy) }}</span></div>
    </div>
  </div>
  <span v-else-if="!compact" class="text-xs text-gray-400">{{ t('admin.accounts.multiProxy.directShort') }}</span>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Account, Proxy } from '@/types'
import { accountProxyBindings } from './proxyBindings'
import { proxyExpiryBadgeClass, proxyExpiryLabelKey } from '@/utils/proxyExpiry'
const props = defineProps<{ account: Account; compact?: boolean }>()
const { t } = useI18n()
const bindings = computed(() => accountProxyBindings(props.account))
function expiryText(proxy: Proxy) { const { key, params } = proxyExpiryLabelKey(proxy.expires_at, proxy.status); return params ? t(key, params) : t(key) }
</script>
