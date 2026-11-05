<template>
  <div v-if="valid" class="space-y-1 rounded-lg bg-gray-50 p-3 text-xs leading-5 text-gray-600 dark:bg-dark-800 dark:text-dark-300" data-testid="proxy-pool-capacity">
    <p>{{ t('admin.proxyGroups.capacityPreview', { total: poolSize, ips: ipsPerAccount, accounts: accountSlots, last: lastAccountIPs }) }}</p>
    <p>{{ t('admin.proxyGroups.capacityHint') }}</p>
  </div>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

const props = defineProps<{ poolSize: number; accountConcurrency: number; proxyConcurrency: number }>()
const { t } = useI18n()
const valid = computed(() => [props.poolSize, props.accountConcurrency, props.proxyConcurrency].every(value => Number.isInteger(value) && value >= 1 && value <= 10000))
const ipsPerAccount = computed(() => Math.min(props.poolSize, Math.max(1, Math.floor(props.accountConcurrency / props.proxyConcurrency))))
const accountSlots = computed(() => Math.ceil(props.poolSize / ipsPerAccount.value))
const lastAccountIPs = computed(() => props.poolSize - (accountSlots.value - 1) * ipsPerAccount.value)
</script>
