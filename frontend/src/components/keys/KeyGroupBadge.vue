<template>
  <span class="inline-flex min-w-0 flex-col items-start gap-1">
    <GroupBadge :name="group.name" :platform="group.platform" :subscription-type="group.subscription_type" :rate-multiplier="group.rate_multiplier" :user-rate-multiplier="userRate" :peak-rate-enabled="group.peak_rate_enabled" :peak-start="group.peak_start" :peak-end="group.peak_end" :peak-rate-multiplier="group.peak_rate_multiplier" :always-show-rate="true" />
    <span class="text-left text-xs text-gray-500 dark:text-gray-400" data-test="key-group-audit-rate">{{ t('keys.auditRate', { multiplier: group.security_audit_multiplier ?? '1' }) }} · {{ t(group.security_audit_auto_deduct_enabled ? 'keys.auditAutoDeductOn' : 'keys.auditAutoDeductOff') }}</span>
  </span>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import GroupBadge from '@/components/common/GroupBadge.vue'
import type { Group } from '@/types'

defineProps<{ group: Group; userRate?: number | null }>()
const { t } = useI18n()
</script>
