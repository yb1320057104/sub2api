<template>
  <component :is="embedded ? 'div' : AppLayout">
    <div class="space-y-5">
      <div v-if="!embedded">
        <h1 class="text-2xl font-semibold text-gray-900 dark:text-gray-100">{{ t('securityPolicyRanking.title') }}</h1>
        <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ t('securityPolicyRanking.description') }}</p>
        <p v-if="!isAdmin" class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('securityPolicyRanking.maskedIdentity') }}</p>
      </div>

      <section class="card space-y-3 p-4">
        <div class="flex flex-wrap items-center justify-between gap-4">
          <DateRangePicker :start-date="startDate" :end-date="endDate" @change="changeRange" />
          <div class="flex flex-wrap items-center gap-4">
            <label class="flex items-center gap-2 text-sm">
              <input v-model="autoRefresh" type="checkbox" class="h-4 w-4 rounded" data-testid="ranking-auto-refresh" />
              {{ t('securityPolicyRanking.autoRefresh') }}
            </label>
            <label class="flex items-center gap-2 text-sm">
              {{ t('securityPolicyRanking.refreshSeconds') }}
              <input v-model.number="refreshSeconds" type="number" min="1" max="3600" step="1" class="input w-24" data-testid="ranking-refresh-seconds" :aria-invalid="!validInterval" />
            </label>
            <button class="btn btn-secondary" :disabled="refreshing" data-testid="ranking-refresh" @click="refresh()">{{ t('common.refresh') }}</button>
          </div>
        </div>
        <div class="flex flex-wrap justify-between gap-2 text-xs text-gray-500 dark:text-gray-400">
          <span>{{ t('securityPolicyRanking.timezone', { timezone }) }}</span>
          <span v-if="ranking?.generated_at">{{ t('securityPolicyRanking.lastUpdated', { time: dateTime(ranking.generated_at) }) }}</span>
        </div>
        <p v-if="!validInterval" class="text-sm text-red-600 dark:text-red-400" role="alert">{{ t('securityPolicyRanking.invalidInterval') }}</p>
        <p v-if="rangeError" class="text-sm text-red-600 dark:text-red-400" role="alert">{{ rangeError }}</p>
      </section>

      <section v-if="isAdmin && !embedded" class="card space-y-4 p-4" data-testid="ranking-settings">
        <form class="space-y-4" @submit.prevent="saveSettings">
          <div class="flex flex-wrap items-end gap-3">
            <div class="min-w-48 flex-1">
              <label for="ranking-multiplier" class="input-label">{{ t('securityPolicyRanking.multiplier') }}</label>
              <input id="ranking-multiplier" v-model="settings.multiplier" type="text" inputmode="decimal" maxlength="11" class="input mt-1 w-full sm:max-w-60" :disabled="!settingsReady || savingSettings" aria-describedby="ranking-multiplier-hint" />
            </div>
            <button class="btn btn-primary" type="submit" :disabled="!settingsReady || savingSettings || deductionBusy" data-testid="ranking-save-settings">{{ savingSettings ? t('common.processing') : t('common.save') }}</button>
          </div>
          <p id="ranking-multiplier-hint" class="text-xs text-gray-500 dark:text-gray-400">{{ t('securityPolicyRanking.multiplierHint') }}</p>
          <div class="flex flex-wrap items-center gap-x-6 gap-y-3 border-t border-gray-200 pt-4 dark:border-dark-700">
            <label class="flex items-center gap-2 text-sm">
              <input v-model="settings.auto_disable_enabled" type="checkbox" class="h-4 w-4 rounded" :disabled="!settingsReady || savingSettings" data-testid="ranking-auto-disable" />
              {{ t('securityPolicyRanking.autoDisable') }}
            </label>
            <label class="flex items-center gap-2 text-sm">
              {{ t('securityPolicyRanking.threshold') }}
              <input v-model.number="settings.auto_disable_threshold" type="number" min="1" max="1000000000" step="1" class="input w-36" :disabled="!settingsReady || savingSettings" data-testid="ranking-threshold" />
            </label>
            <label class="flex items-center gap-2 text-sm">
              <input v-model="settings.reset_on_manual_enable" type="checkbox" class="h-4 w-4 rounded" :disabled="!settingsReady || savingSettings" data-testid="ranking-reset-on-enable" />
              {{ t('securityPolicyRanking.resetOnEnable') }}
            </label>
          </div>
          <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('securityPolicyRanking.autoDisableHint') }}</p>
          <fieldset class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-700" :disabled="!settingsReady || savingSettings || loadingGroups">
            <legend class="px-1 text-sm font-medium">{{ t('securityPolicyRanking.detectionGroups') }}</legend>
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('securityPolicyRanking.detectionGroupsHint') }}</p>
            <input v-model.trim="groupSearch" type="search" class="input w-full" :placeholder="t('securityPolicyRanking.searchGroups')" :aria-label="t('securityPolicyRanking.searchGroups')" data-testid="ranking-group-search" />
            <p v-if="loadingGroups" class="text-sm text-gray-500">{{ t('common.loading') }}</p>
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('securityPolicyRanking.groupRulesHint') }}</p>
            <div class="grid max-h-96 gap-3 overflow-y-auto sm:grid-cols-2 lg:grid-cols-3">
              <div v-for="group in filteredGroups" :key="group.id" class="min-w-0 space-y-3 rounded-md border border-gray-200 px-3 py-3 text-sm dark:border-dark-700">
                <label class="flex min-w-0 items-center gap-2">
                  <input v-model="settings.group_ids" type="checkbox" :value="group.id" :disabled="group.status !== 'active' && !settings.group_ids.includes(group.id)" class="h-4 w-4 shrink-0 rounded" :data-testid="`ranking-group-${group.id}`" />
                  <span class="min-w-0 truncate" :title="group.name">{{ group.name }}</span>
                  <span class="ml-auto shrink-0 text-xs text-gray-500">#{{ group.id }}<span v-if="group.status !== 'active'"> · {{ t('common.disabled') }}</span></span>
                </label>
                <label class="flex items-center justify-between gap-2 text-xs">
                  <span class="font-semibold text-red-700 dark:text-red-300">{{ t('securityPolicyRanking.groupMultiplier') }}</span>
                  <input :value="groupRule(group.id).multiplier" type="text" inputmode="decimal" maxlength="11" class="input w-28 font-semibold !text-red-700 dark:!text-red-300" :disabled="group.status !== 'active'" :data-testid="`ranking-group-multiplier-${group.id}`" @input="updateGroupRule(group.id, { multiplier: ($event.target as HTMLInputElement).value })" />
                </label>
                <label class="flex items-center gap-2 text-xs">
                  <input :checked="groupRule(group.id).auto_deduct_enabled" type="checkbox" class="h-4 w-4 rounded" :disabled="group.status !== 'active'" :data-testid="`ranking-group-auto-deduct-${group.id}`" @change="updateGroupRule(group.id, { auto_deduct_enabled: ($event.target as HTMLInputElement).checked })" />
                  {{ t('securityPolicyRanking.groupAutoDeduct') }}
                </label>
                <p v-if="!settings.group_ids.includes(group.id)" class="text-xs text-gray-500">{{ t('securityPolicyRanking.groupRuleInactive') }}</p>
                <button v-if="settings.group_rules.some(rule => rule.group_id === group.id)" type="button" class="text-xs text-primary-600 hover:underline dark:text-primary-400" :data-testid="`ranking-clear-rule-${group.id}`" @click="removeGroupRule(group.id)">{{ t('securityPolicyRanking.clearGroupRule') }}</button>
              </div>
            </div>
            <p v-if="!loadingGroups && !groupsError && !filteredGroups.length" class="text-sm text-gray-500">{{ t('common.noGroupsAvailable') }}</p>
            <div v-if="missingGroupIDs.length" class="space-y-2 text-sm text-amber-700 dark:text-amber-300">
              <p>{{ t('securityPolicyRanking.missingGroups') }}</p>
              <label v-for="id in missingGroupIDs" :key="id" class="mr-3 inline-flex items-center gap-2"><input v-model="settings.group_ids" type="checkbox" :value="id" :data-testid="`ranking-missing-group-${id}`" />#{{ id }}</label>
            </div>
            <div v-if="missingRuleIDs.length" class="space-y-2 text-sm text-amber-700 dark:text-amber-300">
              <p>{{ t('securityPolicyRanking.missingGroupRules') }}</p>
              <button v-for="id in missingRuleIDs" :key="id" type="button" class="btn btn-secondary btn-sm mr-2" :data-testid="`ranking-remove-missing-rule-${id}`" @click="removeGroupRule(id)">#{{ id }} · {{ t('common.delete') }}</button>
            </div>
            <p v-if="settings.group_ids.length === 0" class="text-sm text-amber-700 dark:text-amber-300" role="status" data-testid="ranking-empty-scope">{{ t('securityPolicyRanking.emptyDetectionGroups') }}</p>
          </fieldset>
          <div v-if="groupsError" class="space-y-2 text-sm text-red-600 dark:text-red-400" role="alert"><p>{{ groupsError }}</p><button type="button" class="btn btn-secondary btn-sm" :disabled="loadingGroups" @click="loadGroups">{{ t('common.refresh') }}</button></div>
          <UserAllowlistSelector v-model="settings.user_allowlist_ids" :title="t('securityPolicyRanking.userAllowlist')" :hint="t('securityPolicyRanking.userAllowlistHint')" :disabled="!settingsReady || savingSettings" test-id="ranking-user-allowlist" />
          <RouterLink to="/admin/users" class="inline-block text-sm text-primary-600 hover:underline dark:text-primary-400">{{ t('securityPolicyRanking.manageUsers') }}</RouterLink>
        </form>
        <p v-if="settingsError" class="text-sm text-red-600 dark:text-red-400" role="alert">{{ settingsError }}</p>
        <button v-if="!settingsReady && !loadingSettings" class="btn btn-secondary" @click="loadSettings">{{ t('common.refresh') }}</button>
        <p v-if="savedSettings" class="text-sm text-gray-600 dark:text-gray-300">{{ t('securityPolicyRanking.savedMultiplier', { multiplier: decimal(savedSettings.multiplier) }) }}</p>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('securityPolicyRanking.deductionHint') }}</p>
      </section>

      <div class="grid gap-3" :class="isAdmin ? 'sm:grid-cols-3' : 'sm:grid-cols-2'">
        <div class="card p-4"><p class="text-sm text-gray-500">{{ t('securityPolicyRanking.totalUsers') }}</p><p class="mt-2 text-2xl font-semibold tabular-nums">{{ ranking?.total ?? '—' }}</p></div>
        <div class="card p-4"><p class="text-sm text-gray-500">{{ t('securityPolicyRanking.totalTriggers') }}</p><p class="mt-2 text-2xl font-semibold tabular-nums">{{ ranking?.total_triggers ?? '—' }}</p><p v-if="ranking?.total_audit_blocks !== undefined" class="mt-2 text-xs text-gray-500 dark:text-gray-400" data-testid="ranking-audit-total">{{ t('securityPolicyRanking.auditBlockCount', { count: ranking.total_audit_blocks }) }}</p><p v-if="isAdmin && ranking" class="mt-2 text-xs text-gray-500 dark:text-gray-400" data-testid="ranking-count-summary">{{ countSummary(ranking.total_counted, ranking.total_skipped, ranking.total_unknown) }}</p></div>
        <div v-if="isAdmin" class="card p-4"><p class="text-sm text-gray-500">{{ t('securityPolicyRanking.totalPending') }}</p><p class="mt-2 text-2xl font-semibold tabular-nums">{{ ranking?.total_pending ?? '—' }}</p></div>
      </div>

      <div v-if="rankingError" class="rounded-lg bg-red-50 p-4 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300" role="alert">
        <p v-if="ranking">{{ t('securityPolicyRanking.refreshError') }}</p>
        <p>{{ rankingError }}</p>
      </div>
      <section class="card overflow-hidden" :aria-busy="refreshing">
        <div v-if="!ranking && refreshing" class="p-8 text-center text-gray-500">{{ t('common.loading') }}</div>
        <div v-else-if="ranking && !ranking.items.length" class="p-10 text-center text-gray-500">{{ t('securityPolicyRanking.empty') }}</div>
        <div v-else-if="ranking" class="overflow-x-auto">
          <table class="w-full text-left text-sm" data-testid="ranking-table">
            <thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-900/50 dark:text-gray-400">
              <tr>
                <th class="whitespace-nowrap px-4 py-3">{{ t('securityPolicyRanking.rank') }}</th>
                <th class="px-4 py-3">{{ t('securityPolicyRanking.user') }}</th>
                <th class="whitespace-nowrap px-4 py-3 text-right">{{ t('securityPolicyRanking.triggers') }}</th>
                <template v-if="isAdmin">
                  <th class="whitespace-nowrap px-4 py-3 text-right">{{ t('securityPolicyRanking.deducted') }}</th>
                  <th class="whitespace-nowrap px-4 py-3 text-right">{{ t('securityPolicyRanking.pending') }}</th>
                  <th class="whitespace-nowrap px-4 py-3 text-right">{{ t('securityPolicyRanking.amount') }}</th>
                  <th class="whitespace-nowrap px-4 py-3 text-right">{{ t('securityPolicyRanking.balance') }}</th>
                  <th class="whitespace-nowrap px-4 py-3">{{ t('securityPolicyRanking.status') }}</th>
                  <th class="whitespace-nowrap px-4 py-3 text-right">{{ t('common.actions') }}</th>
                </template>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
              <tr v-for="row in ranking.items" :key="isAdmin ? row.user_id ?? row.rank : row.rank" class="hover:bg-gray-50 dark:hover:bg-dark-700/40" :data-testid="`ranking-user-${isAdmin ? row.user_id ?? row.rank : row.rank}`">
                <td class="px-4 py-4 font-semibold tabular-nums" :class="row.rank <= 3 ? 'text-primary-600 dark:text-primary-400' : 'text-gray-500'">{{ row.rank }}</td>
                <td class="px-4 py-4"><div class="font-medium">{{ isAdmin ? row.username || `#${row.user_id}` : row.display_name }}</div><div v-if="isAdmin" class="mt-1 text-xs text-gray-500">{{ row.email }} · #{{ row.user_id }}</div></td>
                <td class="px-4 py-4 text-right tabular-nums"><div class="font-semibold">{{ row.trigger_count }}</div><div v-if="row.audit_block_count !== undefined" class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('securityPolicyRanking.auditBlockCount', { count: row.audit_block_count }) }}</div><div v-if="isAdmin" class="mt-1 min-w-44 text-xs text-gray-500 dark:text-gray-400" :data-testid="`ranking-counts-${row.user_id}`">{{ countSummary(row.counted_count, row.skipped_count, row.unknown_count) }}</div></td>
                <template v-if="isAdmin">
                  <td class="px-4 py-4 text-right tabular-nums">{{ row.deducted_count }}</td>
                  <td class="px-4 py-4 text-right tabular-nums">{{ row.pending_count }}</td>
                  <td class="whitespace-nowrap px-4 py-4 text-right tabular-nums">{{ decimal(row.pending_amount) }}</td>
                  <td class="whitespace-nowrap px-4 py-4 text-right tabular-nums">{{ decimal(row.balance) }}<div class="mt-1 text-xs text-green-700 dark:text-green-300" data-testid="ranking-auto-charge-summary">{{ t('securityPolicyRanking.autoChargeSummary', { count: row.auto_charged_count ?? 0, amount: decimal(row.auto_charged_amount) }) }}</div><div class="mt-1 max-w-72 whitespace-normal text-xs text-gray-500" data-testid="ranking-group-summary">{{ row.trigger_groups || '—' }}</div></td>
                  <td class="whitespace-nowrap px-4 py-4">
                    <span :class="row.status === 'active' ? 'text-green-600 dark:text-green-400' : 'text-gray-500'">{{ t(row.deleted ? 'securityPolicyRanking.deletedUser' : row.status === 'active' ? 'common.enabled' : 'common.disabled') }}</span>
                    <div v-if="row.auto_disable_count !== undefined" class="mt-1 text-xs text-gray-500">{{ t('securityPolicyRanking.disableCount', { count: row.auto_disable_count }) }}</div>
                  </td>
                  <td class="whitespace-nowrap px-4 py-4 text-right">
                    <button class="btn btn-secondary btn-sm mr-2" :disabled="!row.user_id || !!rangeError || !!rankingError || deductionBusy" :data-testid="`ranking-records-${row.user_id}`" @click="openRecords(row)">{{ t('securityPolicyRanking.records') }}</button>
                    <button class="btn btn-secondary btn-sm" :disabled="deductionBusy || savingSettings || !settingsReady || row.deleted || !row.pending_count || !!rankingError" :data-testid="`ranking-deduct-${row.user_id}`" @click="openPreview(row)">{{ previewingUser === row.user_id ? t('securityPolicyRanking.previewing') : t('securityPolicyRanking.deduct') }}</button>
                  </td>
                </template>
              </tr>
            </tbody>
          </table>
        </div>
        <div v-if="ranking && ranking.total > 0" class="flex flex-wrap items-center justify-between border-t border-gray-200 dark:border-dark-700">
          <label class="flex items-center gap-2 p-4 text-sm text-gray-600 dark:text-gray-300">{{ t('pagination.perPage') }}<select :value="pageSize" class="input w-24" data-testid="ranking-page-size" @change="changePageSize(Number(($event.target as HTMLSelectElement).value))"><option v-for="size in [10, 20, 50, 100]" :key="size" :value="size">{{ size }}</option></select></label>
          <Pagination class="min-w-0 flex-1 !border-t-0" :page="page" :page-size="pageSize" :total="ranking.total" :show-page-size-selector="false" @update:page="changePage" @update:page-size="changePageSize" />
        </div>
      </section>
    </div>

    <BaseDialog :show="isAdmin && !!recordsUser" :title="t('securityPolicyRanking.records')" width="extra-wide" :close-on-click-outside="true" @close="closeRecords">
      <div v-if="recordsUser" class="space-y-4" data-testid="ranking-records-dialog">
        <p class="break-all text-sm font-medium">{{ recordsUser.username || `#${recordsUser.user_id}` }} · {{ recordsUser.email }} · #{{ recordsUser.user_id }}</p>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ recordsRange?.start_date }} ~ {{ recordsRange?.end_date }} · {{ recordsRange?.timezone }}</p>
        <p class="rounded-lg bg-gray-50 p-3 text-sm text-gray-600 dark:bg-dark-800 dark:text-gray-300">{{ t('securityPolicyRanking.recordEvidenceHint') }}</p>
        <div v-if="recordsError" class="space-y-2 text-sm text-red-600 dark:text-red-400" role="alert"><p>{{ recordsError }}</p><button type="button" class="btn btn-secondary btn-sm" @click="loadRecords">{{ t('common.refresh') }}</button></div>
        <div v-if="loadingRecords" class="p-6 text-center text-sm text-gray-500" role="status">{{ t('common.loading') }}</div>
        <div v-else-if="records && !records.items.length" class="p-6 text-center text-sm text-gray-500">{{ t('securityPolicyRanking.empty') }}</div>
        <div v-else-if="records" class="max-h-[55vh] overflow-auto">
          <table class="w-full text-left text-sm" data-testid="ranking-records-table">
            <thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-900"><tr><th class="px-3 py-2">{{ t('securityPolicyRanking.recordTime') }}</th><th class="px-3 py-2">{{ t('securityPolicyRanking.recordSource') }}</th><th class="px-3 py-2">{{ t('securityPolicyRanking.recordGroup') }}</th><th class="px-3 py-2">{{ t('securityPolicyRanking.recordRequest') }}</th><th class="px-3 py-2">{{ t('securityPolicyRanking.recordCountStatus') }}</th><th class="px-3 py-2">{{ t('securityPolicyRanking.recordChargeStatus') }}</th></tr></thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
              <tr v-for="record in records.items" :key="record.record_key || `${record.source || 'upstream_policy'}-${record.audit_id ?? record.usage_id}-${record.occurred_at}-${record.request_id}`" :data-testid="`ranking-record-${record.audit_id ? `audit-${record.audit_id}` : record.usage_id}`">
                <td class="whitespace-nowrap px-3 py-3"><div>{{ dateTime(record.occurred_at) }}</div><div v-if="record.audit_id" class="mt-1 text-xs text-gray-500">{{ t('securityPolicyRanking.auditRecordId', { id: record.audit_id }) }}</div><div v-else-if="record.usage_id > 0" class="mt-1 text-xs text-gray-500">{{ t('securityPolicyRanking.usageRecordId', { id: record.usage_id }) }}</div><div v-if="record.linked_usage_id" class="mt-1 text-xs text-gray-500" data-testid="ranking-linked-usage">{{ t('securityPolicyRanking.linkedUsageRecordId', { id: record.linked_usage_id }) }}</div></td>
                <td class="min-w-40 px-3 py-3"><div>{{ t(`securityPolicyRanking.recordSources.${record.source || 'upstream_policy'}`) }}</div><div v-if="record.policy_action === 'block' || record.policy_action === 'charge_allow' || (record.policy_action === 'allow' && (record.risk_score ?? 0) > 0)" class="mt-1 text-xs font-medium" data-testid="ranking-policy-action">{{ t(`admin.promptAudit.riskPolicy.actions.${record.policy_action}`) }}<span v-if="typeof record.risk_score === 'number'"> · {{ record.risk_score }} / 100</span></div><div v-if="record.source?.endsWith('_observation')" class="mt-1 text-xs text-gray-500">{{ t('securityPolicyRanking.observationHint') }}</div><div v-else-if="record.source && record.source !== 'upstream_policy'" class="mt-1 text-xs text-gray-500">{{ t(record.policy_action === 'charge_allow' ? 'securityPolicyRanking.auditChargeNotManual' : 'securityPolicyRanking.auditNotDeductible') }}</div></td>
                <td class="px-3 py-3"><span v-if="record.group_id">{{ record.group_name || `#${record.group_id}` }}<span v-if="record.group_name" class="ml-1 text-xs text-gray-500">#{{ record.group_id }}</span></span><span v-else class="text-gray-500">{{ t('securityPolicyRanking.ungrouped') }}</span></td>
                <td class="max-w-64 break-all px-3 py-3"><div>{{ record.model || '—' }}</div><div class="mt-1 text-xs text-gray-500">{{ record.request_id || '—' }}</div></td>
                <td class="min-w-60 px-3 py-3"><div :class="record.count_status === 'counted' ? 'text-green-700 dark:text-green-300' : record.count_status === 'skipped' ? 'text-amber-700 dark:text-amber-300' : 'text-gray-500 dark:text-gray-400'">{{ recordCountLabel(record) }}</div><div v-if="record.settings_revision !== null && record.settings_revision !== undefined" class="mt-1 text-xs text-gray-500">{{ t('securityPolicyRanking.recordRevision', { revision: record.settings_revision }) }}</div></td>
                <td class="min-w-64 px-3 py-3" data-testid="ranking-record-charge">
                  <div :class="record.auto_charge_status === 'deducted' ? 'text-green-700 dark:text-green-300' : 'text-gray-600 dark:text-gray-400'">{{ t(`securityPolicyRanking.chargeStatuses.${record.auto_charge_status || 'historical_unknown'}`) }}</div>
                  <div v-if="record.auto_charge_reason" class="mt-1 text-xs text-gray-500">{{ recordChargeReason(record.auto_charge_reason) }}</div>
                  <div v-if="record.audit_multiplier != null && record.auto_charge_reason !== 'auto_deduct_off'" class="mt-1 text-xs tabular-nums text-gray-500" data-testid="ranking-record-charge-formula">{{ recordChargeFormula(record) }}</div>
                  <div class="mt-1 text-xs tabular-nums" data-testid="ranking-record-charge-deducted">{{ t('securityPolicyRanking.chargeDeducted', { amount: record.auto_charge_status === 'deducted' ? decimal(record.auto_charge_amount) : !record.auto_charge_status || record.auto_charge_status === 'historical_unknown' ? '—' : '0' }) }}</div>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <Pagination v-if="records && records.total > 0" :page="records.page" :page-size="records.page_size" :total="records.total" :show-page-size-selector="false" @update:page="changeRecordsPage" />
      </div>
      <template #footer><button type="button" class="btn btn-secondary" data-testid="ranking-records-close" @click="closeRecords">{{ t('common.close') }}</button></template>
    </BaseDialog>

    <BaseDialog :show="isAdmin && !!preview" :title="t('securityPolicyRanking.confirmTitle')" :close-on-escape="!deducting" :close-on-click-outside="!deducting" :show-close-button="!deducting" @close="closePreview">
      <div v-if="preview" class="space-y-4" data-testid="ranking-deduction-preview">
        <p class="text-sm text-gray-600 dark:text-gray-300">{{ t('securityPolicyRanking.confirmDescription') }}</p>
        <dl class="space-y-3 text-sm">
          <div><dt class="text-gray-500">{{ t('securityPolicyRanking.user') }}</dt><dd class="mt-1 break-all font-medium">{{ preview.username || `#${preview.user_id}` }} · {{ preview.email }} · #{{ preview.user_id }}</dd></div>
          <div><dt class="text-gray-500">{{ t('securityPolicyRanking.range') }}</dt><dd class="mt-1">{{ preview.start_date }} ~ {{ preview.end_date }} · {{ preview.timezone }}</dd></div>
          <div><dt class="text-gray-500">{{ t('securityPolicyRanking.balance') }}</dt><dd class="mt-1 tabular-nums">{{ decimal(preview.balance) }}</dd></div>
          <div class="rounded-lg bg-amber-50 p-4 dark:bg-amber-900/20"><dt class="text-amber-800 dark:text-amber-300">{{ t('securityPolicyRanking.previewAmount') }}</dt><dd class="mt-2 text-lg font-semibold tabular-nums text-amber-900 dark:text-amber-200" data-testid="ranking-preview-formula">{{ t('securityPolicyRanking.previewFormula', { count: preview.trigger_count, multiplier: decimal(preview.multiplier), amount: decimal(preview.amount) }) }}</dd></div>
        </dl>
        <p class="text-xs text-gray-500">{{ t('securityPolicyRanking.snapshotHint') }}</p>
        <p class="text-xs text-gray-500">{{ t('securityPolicyRanking.expires', { time: dateTime(preview.expires_at) }) }}</p>
        <div v-if="deductionError" class="space-y-2 text-sm text-red-600 dark:text-red-400" role="alert"><p>{{ deductionError }}</p><p>{{ t('securityPolicyRanking.retryHint') }}</p></div>
      </div>
      <template #footer>
        <div class="flex justify-end gap-2"><button class="btn btn-secondary" :disabled="deducting" @click="closePreview">{{ t('common.cancel') }}</button><button class="btn btn-primary" :disabled="deducting || !preview?.trigger_count" data-testid="ranking-confirm-deduct" @click="confirmDeduction">{{ deducting ? t('common.processing') : deductionError ? t('securityPolicyRanking.retryDeduct') : t('securityPolicyRanking.confirmDeduct') }}</button></div>
      </template>
    </BaseDialog>
  </component>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { securityPolicyRankingAPI } from '@/api/admin/securityPolicyRanking'
import groupsAPI from '@/api/admin/groups'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { SecurityPolicyDeductionPreview, SecurityPolicyGroupRule, SecurityPolicyRanking, SecurityPolicyRankingRange, SecurityPolicyRankingSettings, SecurityPolicyRankingUser, SecurityPolicyTriggerRecord, SecurityPolicyTriggerRecords } from '@/types/securityPolicyRanking'
import type { AdminGroup } from '@/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import DateRangePicker from '@/components/common/DateRangePicker.vue'
import Pagination from '@/components/common/Pagination.vue'
import UserAllowlistSelector from '@/components/admin/user/UserAllowlistSelector.vue'

defineProps<{ embedded?: boolean }>()
const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const isAdmin = computed(() => authStore.isAdmin)
const localDate = () => {
  const now = new Date()
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`
}
const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
const startDate = ref(localDate())
const endDate = ref(startDate.value)
const followToday = ref(true)
const page = ref(1)
const pageSize = ref(50)
const autoRefresh = ref(true)
const refreshSeconds = ref<number | string>(5)
const validInterval = computed(() => Number.isInteger(refreshSeconds.value) && Number(refreshSeconds.value) >= 1 && Number(refreshSeconds.value) <= 3600)
const ranking = ref<SecurityPolicyRanking | null>(null)
const refreshing = ref(false)
const rankingError = ref('')
const rangeError = ref('')
type RankingSettingsDraft = SecurityPolicyRankingSettings & { user_allowlist_ids: number[]; group_rules: SecurityPolicyGroupRule[] }
const settings = reactive<RankingSettingsDraft>({ multiplier: '6.7', auto_disable_enabled: false, auto_disable_threshold: 10, reset_on_manual_enable: true, group_ids: [], user_allowlist_ids: [], group_rules: [] })
const savedSettings = ref<SecurityPolicyRankingSettings | null>(null)
const settingsReady = ref(false)
const loadingSettings = ref(false)
const savingSettings = ref(false)
const settingsError = ref('')
const groups = ref<AdminGroup[]>([])
const groupSearch = ref('')
const groupsError = ref('')
const loadingGroups = ref(false)
const filteredGroups = computed(() => {
  const query = groupSearch.value.toLowerCase()
  return groups.value.filter(group => !query || `${group.id} ${group.name} ${group.platform}`.toLowerCase().includes(query))
})
const missingGroupIDs = computed(() => {
  if (loadingGroups.value || groupsError.value) return []
  const known = new Set(groups.value.map(group => group.id))
  return settings.group_ids.filter(id => !known.has(id))
})
const missingRuleIDs = computed(() => {
  if (loadingGroups.value || groupsError.value) return []
  const known = new Set(groups.value.map(group => group.id))
  return settings.group_rules.filter(rule => !known.has(rule.group_id)).map(rule => rule.group_id)
})
function groupRule(id: number): SecurityPolicyGroupRule {
  return settings.group_rules.find(rule => rule.group_id === id) ?? { group_id: id, multiplier: '1', auto_deduct_enabled: false }
}
function updateGroupRule(id: number, patch: Partial<Omit<SecurityPolicyGroupRule, 'group_id'>>) {
  const rule = { ...groupRule(id), ...patch }
  const index = settings.group_rules.findIndex(item => item.group_id === id)
  if (index < 0) settings.group_rules.push(rule)
  else settings.group_rules[index] = rule
}
function removeGroupRule(id: number) {
  settings.group_rules = settings.group_rules.filter(rule => rule.group_id !== id)
}
function normalizeSettings(value: SecurityPolicyRankingSettings): RankingSettingsDraft {
  return { ...value, group_ids: [...(value.group_ids ?? [])], user_allowlist_ids: [...(value.user_allowlist_ids ?? [])], group_rules: (value.group_rules ?? []).map(rule => ({ ...rule })) }
}
const recordsUser = ref<SecurityPolicyRankingUser | null>(null)
const recordsRange = ref<SecurityPolicyRankingRange | null>(null)
const records = ref<SecurityPolicyTriggerRecords | null>(null)
const recordsPage = ref(1)
const loadingRecords = ref(false)
const recordsError = ref('')
const preview = ref<SecurityPolicyDeductionPreview | null>(null)
const previewingUser = ref<number | null>(null)
const deducting = ref(false)
const deductionError = ref('')
const deductionBusy = computed(() => deducting.value || previewingUser.value !== null || !!preview.value)
let mounted = false
let timer: ReturnType<typeof setTimeout> | undefined
let requestController: AbortController | undefined
let previewController: AbortController | undefined
let recordsController: AbortController | undefined
let recordsRevision = 0
let adminRevision = 0
let revision = 0
let refreshQueued = false
const decimal = (value?: string | null) => value == null ? '—' : value.includes('.') ? value.replace(/0+$/, '').replace(/\.$/, '') : value
const dateTime = (value: string) => new Date(value).toLocaleString()
const range = (): SecurityPolicyRankingRange => ({ start_date: startDate.value, end_date: endDate.value, timezone })
const visible = () => document.visibilityState !== 'hidden'
const errorMessage = (error: unknown) => extractApiErrorMessage(error, t('common.error'))
const stopTimer = () => { clearTimeout(timer); timer = undefined }
const countSummary = (counted?: number, skipped?: number, unknown?: number) => t('securityPolicyRanking.countSummary', { counted: counted ?? '—', skipped: skipped ?? '—', unknown: unknown ?? '—' })

function recordCountLabel(record: SecurityPolicyTriggerRecord) {
  if (record.count_status === 'counted') return t('securityPolicyRanking.recordCounted')
  if (record.count_status !== 'skipped') return t('securityPolicyRanking.recordUnknown')
  const keys: Record<string, string> = {
    observation_only: 'observationHint', group_not_enabled: 'recordGroupSkipped', auto_disable_off: 'recordDisabledSkipped',
    admin_exempt: 'recordAdminSkipped', user_unavailable: 'recordUserSkipped', user_exempt: 'recordExemptSkipped',
  }
  return t(`securityPolicyRanking.${keys[record.count_reason] || 'recordSkipped'}`)
}

function recordChargeReason(reason: string) {
  const known = ['observation_only', 'group_not_enabled', 'user_exempt', 'user_unavailable', 'admin_exempt', 'auto_deduct_off', 'invalid_cost', 'charge_failed']
  return t(`securityPolicyRanking.chargeReasons.${known.includes(reason) ? reason : 'unknown'}`)
}

function recordChargeFormula(record: SecurityPolicyTriggerRecord) {
  const values = { cost: decimal(record.actual_cost), multiplier: decimal(record.audit_multiplier), amount: decimal(record.auto_charge_amount) }
  if (record.auto_charge_rule === 'max_cost_multiplier_minimum') {
    if (record.auto_charge_minimum == null) return t('securityPolicyRanking.chargeFormulaUnknown')
    return t('securityPolicyRanking.chargeMinimumFormula', { ...values, minimum: decimal(record.auto_charge_minimum) })
  }
  return t('securityPolicyRanking.chargeFormula', values)
}

function schedule() {
  stopTimer()
  if (!mounted || !visible() || !autoRefresh.value || !validInterval.value || refreshing.value) return
  timer = setTimeout(() => { void refresh() }, Number(refreshSeconds.value) * 1000)
}

// One list request at a time. Changed filters cancel the old request and queue
// only the latest range; late responses cannot repopulate the wrong date range.
async function refresh() {
  stopTimer()
  if (!mounted || !visible() || rangeError.value) return
  if (followToday.value && startDate.value !== localDate()) {
    startDate.value = localDate()
    endDate.value = startDate.value
    page.value = 1
    ranking.value = null
    closeRecords()
    revision++
    requestController?.abort()
  }
  if (refreshing.value) { refreshQueued = true; return }
  const currentRevision = revision
  const controller = new AbortController()
  requestController = controller
  refreshing.value = true
  try {
    const params = { ...range(), page: page.value, page_size: pageSize.value }
    const { data } = await (isAdmin.value ? securityPolicyRankingAPI.getRanking(params, controller.signal) : securityPolicyRankingAPI.getPublicRanking(params, controller.signal))
    if (!mounted || controller.signal.aborted || currentRevision !== revision) return
    ranking.value = data
    rankingError.value = ''
    const lastPage = Math.max(1, Math.ceil(data.total / pageSize.value))
    if (page.value > lastPage) { page.value = lastPage; refreshQueued = true }
  } catch (error) {
    if (mounted && !controller.signal.aborted && currentRevision === revision) rankingError.value = errorMessage(error)
  } finally {
    refreshing.value = false
    if (requestController === controller) requestController = undefined
    if (mounted && visible() && refreshQueued) { refreshQueued = false; void refresh() }
    else schedule()
  }
}

function resetList() {
  revision++
  requestController?.abort()
  previewController?.abort()
  closeRecords()
  ranking.value = null
  rankingError.value = ''
  if (!deducting.value) preview.value = null
  void refresh()
}
function validDate(value: string) {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return false
  const parsed = new Date(`${value}T12:00:00Z`)
  return !Number.isNaN(parsed.getTime()) && parsed.toISOString().slice(0, 10) === value
}
function changeRange(value: { startDate: string; endDate: string; preset: string | null }) {
  if (deducting.value) return
  if (!validDate(value.startDate) || !validDate(value.endDate) || value.startDate > value.endDate) {
    rangeError.value = t('securityPolicyRanking.invalidRange')
    stopTimer()
    return
  }
  rangeError.value = ''
  startDate.value = value.startDate
  endDate.value = value.endDate
  followToday.value = value.preset === 'today'
  page.value = 1
  resetList()
}
function changePage(value: number) { page.value = value; resetList() }
function changePageSize(value: number) { pageSize.value = Math.min(100, Math.max(10, Number.isFinite(value) ? Math.floor(value) : 50)); page.value = 1; resetList() }

async function loadSettings() {
  if (!isAdmin.value || loadingSettings.value) return
  loadingSettings.value = true
  settingsError.value = ''
  const currentAdminRevision = adminRevision
  try {
    const { data } = await securityPolicyRankingAPI.getSettings()
    if (!mounted || !isAdmin.value || currentAdminRevision !== adminRevision) return
    const normalized = normalizeSettings(data)
    savedSettings.value = normalized
    Object.assign(settings, normalizeSettings(normalized))
    settingsReady.value = true
  } catch (error) { if (mounted && isAdmin.value && currentAdminRevision === adminRevision) settingsError.value = errorMessage(error) }
  finally { if (currentAdminRevision === adminRevision) loadingSettings.value = false }
}

async function loadGroups() {
  if (!isAdmin.value || loadingGroups.value) return
  const currentAdminRevision = adminRevision
  loadingGroups.value = true
  groupsError.value = ''
  try {
    const data = await groupsAPI.getAllIncludingInactive()
    if (mounted && isAdmin.value && currentAdminRevision === adminRevision) groups.value = data
  } catch (error) {
    if (mounted && isAdmin.value && currentAdminRevision === adminRevision) groupsError.value = errorMessage(error)
  } finally {
    if (currentAdminRevision === adminRevision) loadingGroups.value = false
  }
}
async function saveSettings() {
  if (!isAdmin.value || !settingsReady.value || savingSettings.value || deductionBusy.value) return
  const multiplier = settings.multiplier.trim()
  if (!/^\d+(\.\d{1,6})?$/.test(multiplier) || Number(multiplier) < 0.000001 || Number(multiplier) > 1000) {
    settingsError.value = t('securityPolicyRanking.invalidMultiplier'); return
  }
  if (!Number.isInteger(settings.auto_disable_threshold) || settings.auto_disable_threshold < 1 || settings.auto_disable_threshold > 1000000000) {
    settingsError.value = t('securityPolicyRanking.invalidThreshold'); return
  }
  const groupRules = settings.group_rules.map(rule => ({ ...rule, multiplier: rule.multiplier.trim() }))
  const invalidRule = groupRules.find(rule => !/^\d+(\.\d{1,6})?$/.test(rule.multiplier) || Number(rule.multiplier) < 0.000001 || Number(rule.multiplier) > 1000)
  if (invalidRule) {
    settingsError.value = t('securityPolicyRanking.invalidGroupMultiplier', { id: invalidRule.group_id }); return
  }
  savingSettings.value = true
  settingsError.value = ''
  const currentAdminRevision = adminRevision
  try {
    const { data } = await securityPolicyRankingAPI.saveSettings({ ...settings, multiplier, group_ids: [...new Set(settings.group_ids)].sort((a, b) => a - b), user_allowlist_ids: [...new Set(settings.user_allowlist_ids)].sort((a, b) => a - b), group_rules: groupRules })
    if (!mounted || !isAdmin.value || currentAdminRevision !== adminRevision) return
    const normalized = normalizeSettings(data)
    savedSettings.value = normalized
    Object.assign(settings, normalizeSettings(normalized))
    appStore.showSuccess(t('securityPolicyRanking.settingsSaved'))
    resetList()
  } catch (error) { if (mounted && isAdmin.value && currentAdminRevision === adminRevision) settingsError.value = errorMessage(error) }
  finally { if (currentAdminRevision === adminRevision) savingSettings.value = false }
}

function closeRecords() {
  recordsRevision++
  recordsController?.abort()
  recordsController = undefined
  recordsUser.value = null
  recordsRange.value = null
  records.value = null
  recordsError.value = ''
  loadingRecords.value = false
}

function openRecords(row: SecurityPolicyRankingUser) {
  if (!isAdmin.value || !row.user_id || rangeError.value || rankingError.value || deductionBusy.value) return
  closeRecords()
  recordsUser.value = { ...row }
  recordsRange.value = range()
  recordsPage.value = 1
  void loadRecords()
}

async function loadRecords() {
  if (!mounted || !isAdmin.value || !recordsUser.value?.user_id || !recordsRange.value) return
  recordsController?.abort()
  const controller = new AbortController()
  recordsController = controller
  const currentRevision = ++recordsRevision
  const userID = recordsUser.value.user_id
  const params = { ...recordsRange.value, page: recordsPage.value, page_size: 20 }
  loadingRecords.value = true
  recordsError.value = ''
  records.value = null
  try {
    const { data } = await securityPolicyRankingAPI.getRecords(userID, params, controller.signal)
    if (!mounted || !isAdmin.value || controller.signal.aborted || currentRevision !== recordsRevision) return
    const lastPage = Math.max(1, Math.ceil(data.total / data.page_size))
    if (recordsPage.value > lastPage) {
      recordsPage.value = lastPage
      void loadRecords()
      return
    }
    records.value = data
  } catch (error) {
    if (mounted && isAdmin.value && !controller.signal.aborted && currentRevision === recordsRevision) recordsError.value = errorMessage(error)
  } finally {
    if (currentRevision === recordsRevision) {
      loadingRecords.value = false
      recordsController = undefined
    }
  }
}

function changeRecordsPage(value: number) {
  if (!Number.isInteger(value) || value < 1 || !records.value || value > Math.max(1, records.value.pages)) return
  recordsPage.value = value
  void loadRecords()
}

async function openPreview(row: SecurityPolicyRankingUser) {
  if (!isAdmin.value || !row.user_id || deductionBusy.value || savingSettings.value || !settingsReady.value || row.deleted || !row.pending_count) return
  closeRecords()
  const controller = new AbortController()
  const currentRevision = revision
  previewController = controller
  previewingUser.value = row.user_id
  deductionError.value = ''
  try {
    const { data } = await securityPolicyRankingAPI.preview(row.user_id, range(), controller.signal)
    if (!mounted || controller.signal.aborted || revision !== currentRevision) return
    if (data.trigger_count < 1) { appStore.showError(t('securityPolicyRanking.noPending')); void refresh(); return }
    preview.value = data
  } catch (error) { if (mounted && !controller.signal.aborted) appStore.showError(errorMessage(error)) }
  finally { previewingUser.value = null; if (previewController === controller) previewController = undefined }
}
function closePreview() {
  if (deducting.value) return
  preview.value = null
  deductionError.value = ''
}
async function confirmDeduction() {
  if (!isAdmin.value || !preview.value || deducting.value) return
  // Keep this ID on every failure, including an unknown network outcome.
  // The server returns the original result if this preview already completed.
  const currentPreview = preview.value
  deducting.value = true
  deductionError.value = ''
  try {
    const { data } = await securityPolicyRankingAPI.deduct(currentPreview.user_id, currentPreview.id)
    if (!mounted) return
    appStore.showSuccess(t('securityPolicyRanking.deductSuccess', { amount: decimal(data.amount), count: data.trigger_count }))
    preview.value = null
    resetList()
  } catch (error) { if (mounted) deductionError.value = errorMessage(error) }
  finally { deducting.value = false }
}
function onVisibilityChange() {
  stopTimer()
  if (!visible()) {
    revision++
    refreshQueued = false
    requestController?.abort()
    previewController?.abort()
    closeRecords()
  } else if (autoRefresh.value && validInterval.value) void refresh()
}
watch([autoRefresh, refreshSeconds], schedule)
watch(isAdmin, () => {
  adminRevision++
  loadingSettings.value = false
  savingSettings.value = false
  loadingGroups.value = false
  settingsReady.value = false
  savedSettings.value = null
  groups.value = []
  groupsError.value = ''
  settingsError.value = ''
  settings.group_ids = []
  settings.group_rules = []
  settings.user_allowlist_ids = []
  closePreview()
  resetList()
  if (isAdmin.value) { void loadSettings(); void loadGroups() }
})
onMounted(() => {
  mounted = true
  document.addEventListener('visibilitychange', onVisibilityChange)
  void refresh()
  if (isAdmin.value) { void loadSettings(); void loadGroups() }
})
onUnmounted(() => {
  mounted = false
  adminRevision++
  revision++
  refreshQueued = false
  stopTimer()
  requestController?.abort()
  previewController?.abort()
  closeRecords()
  document.removeEventListener('visibilitychange', onVisibilityChange)
})
</script>
