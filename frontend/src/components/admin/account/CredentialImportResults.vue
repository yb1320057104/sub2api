<template>
  <div class="space-y-3" data-testid="credential-import-results">
    <p role="status" class="text-sm text-gray-700 dark:text-dark-200">{{ t('tokenGuardV2.importSummary', { succeeded: result.succeeded, skipped: result.skipped, failed: result.failed }) }}</p>
    <p v-if="result.items.length < result.succeeded + result.skipped + result.failed" class="text-xs text-gray-500 dark:text-dark-400">{{ t('tokenGuardV2.importDetailsWindow', { count: result.items.length }) }}</p>
    <div v-if="result.items.length" class="max-h-72 overflow-auto rounded-xl border border-gray-200 dark:border-dark-700">
      <table class="w-full text-left text-xs">
        <thead class="sticky top-0 bg-gray-50 dark:bg-dark-900"><tr><th class="px-3 py-2">{{ t('authorization.batchLine') }}</th><th class="px-3 py-2">{{ t('authorization.email') }}</th><th class="px-3 py-2">{{ t('authorization.status') }}</th></tr></thead>
        <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
          <tr v-for="(item, index) in result.items" :key="index">
            <td class="px-3 py-2 tabular-nums">{{ item.line || '—' }}</td>
            <td class="break-all px-3 py-2">{{ item.email || '—' }}<span v-if="item.account_id" class="ml-1 text-gray-400">#{{ item.account_id }}</span></td>
            <td class="px-3 py-2">{{ statusLabel(item.status) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { TokenGuardV2ImportResult } from '@/api/admin/accountTokenGuardV2'
defineProps<{ result: TokenGuardV2ImportResult }>()
const { t } = useI18n()
const statuses = new Set(['added', 'saved', 'updated', 'enrolled', 'unchanged', 'busy', 'missing_credentials', 'invalid_format', 'invalid_email', 'invalid_password', 'invalid_twofa', 'duplicate_email', 'not_found', 'ambiguous_email', 'unsupported_account', 'account_changed', 'conflict', 'failed', 'cancelled', 'interrupted'])
const statusLabel = (value: string) => t('tokenGuardV2.importStatuses.' + (statuses.has(value) ? value : 'unknown'))
</script>
