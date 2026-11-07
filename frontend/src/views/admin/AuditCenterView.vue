<template>
  <AppLayout>
    <div class="mx-auto max-w-[1600px] space-y-6 pb-28">
      <p class="max-w-4xl text-sm leading-6 text-gray-500 dark:text-gray-400">{{ t('admin.auditCenter.intro') }}</p>
      <div v-if="loadError" class="rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-700 dark:border-red-900 dark:bg-red-950/20 dark:text-red-300" role="alert">
        {{ loadError }}
        <button class="btn btn-secondary btn-sm ml-3" :disabled="loading" @click="load">{{ t('common.refresh') }}</button>
      </div>
      <p v-if="loading && !snapshot" role="status">{{ t('common.loading') }}</p>
      <template v-if="snapshot && prompt && ranking">
        <div class="card flex flex-wrap items-start justify-between gap-4 p-4 sm:p-6"><div class="min-w-0 flex-1"><h2 class="font-semibold">{{ t('admin.auditCenter.globalSwitch') }}</h2><p class="mt-2 text-sm text-gray-500">{{ t('admin.auditCenter.globalSwitchHint') }}</p><p v-if="!riskControlEnabled" class="mt-2 text-sm text-amber-700 dark:text-amber-300">{{ t('admin.auditCenter.globalDisabled') }}</p></div><label class="flex items-center gap-2 text-sm"><input v-model="riskControlEnabled" type="checkbox" :disabled="saving" data-test="audit-global-enabled" />{{ t('common.enabled') }}</label></div>
        <nav class="flex flex-wrap gap-2" :aria-label="t('admin.auditCenter.workflow')">
          <a v-for="(step, index) in steps" :key="step" :href="`#audit-${step}`" class="rounded-full border border-gray-200 px-4 py-2 text-sm hover:border-primary-400 dark:border-dark-700">{{ index + 1 }} · {{ t(`admin.auditCenter.steps.${step}`) }}</a>
        </nav>

        <fieldset :disabled="saving" class="min-w-0 space-y-6">
        <section id="audit-scope" class="card scroll-mt-20 space-y-5 p-4 sm:p-6" data-test="audit-shared-scope">
          <div><h2 class="text-lg font-semibold">1 · {{ t('admin.auditCenter.steps.scope') }}</h2><p class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.auditCenter.scopeHint') }}</p></div>
          <div v-if="hasDifferences" class="space-y-3 rounded-xl bg-amber-50 p-4 dark:bg-amber-950/20" data-test="audit-differences">
            <p class="text-sm font-medium text-amber-900 dark:text-amber-200">{{ t('admin.auditCenter.differences') }}</p>
            <div class="grid gap-3 md:grid-cols-3">
              <div v-for="source in sources" :key="source" class="min-w-0 rounded-lg bg-white/70 p-3 text-sm dark:bg-dark-800/70">
                <p class="font-medium">{{ sourceLabel(source) }}</p>
                <p class="mt-2 break-words text-xs">{{ t('admin.auditCenter.savedGroups') }}: {{ idsLabel(sourceGroups(source)) }}</p>
                <p class="mt-1 break-words text-xs">{{ t('admin.auditCenter.savedExemptions') }}: {{ idsLabel(sourceAllowlist(source)) }}</p>
              </div>
            </div>
          </div>
          <div class="space-y-3">
            <div class="flex flex-wrap items-center justify-between gap-3"><h3 class="font-medium">{{ t('admin.auditCenter.detectionGroups') }}</h3><span class="text-xs text-gray-500">{{ t('admin.auditCenter.selectedCount', { count: sharedGroups.length }) }}</span></div>
            <div v-if="!groupsResolved" class="flex flex-wrap items-center gap-2" data-test="audit-adopt-groups">
              <span class="text-sm text-amber-700 dark:text-amber-300">{{ t('admin.auditCenter.chooseGroupSource') }}</span>
              <button v-for="source in sources" :key="source" type="button" class="btn btn-secondary btn-sm" @click="adoptGroups(source)">{{ t('admin.auditCenter.useSource', { source: sourceLabel(source) }) }}</button>
            </div>
            <template v-else>
              <input v-model="groupSearch" type="search" class="input w-full" :placeholder="t('admin.auditCenter.searchGroups')" :aria-label="t('admin.auditCenter.searchGroups')" />
              <div class="grid max-h-64 gap-2 overflow-y-auto sm:grid-cols-2 lg:grid-cols-3">
                <label v-for="group in visibleGroups" :key="group.id" class="flex min-w-0 items-center gap-3 rounded-lg border border-gray-200 p-3 text-sm dark:border-dark-700">
                  <input type="checkbox" :checked="sharedGroups.includes(group.id)" :disabled="group.status !== 'active' && !sharedGroups.includes(group.id)" :data-test="`audit-shared-group-${group.id}`" @change="toggleGroup(group.id)" />
                  <span class="min-w-0 flex-1 break-words">{{ group.name }}</span><span class="shrink-0 text-xs text-gray-500">#{{ group.id }}<template v-if="group.status !== 'active'"> · {{ t('common.disabled') }}</template></span>
                </label>
              </div>
              <div v-if="missingGroups.length" class="text-sm text-amber-700 dark:text-amber-300"><p>{{ t('admin.auditCenter.missingGroups') }}</p><label v-for="id in missingGroups" :key="id" class="mr-4 inline-flex items-center gap-2"><input type="checkbox" checked @change="toggleGroup(id)" />#{{ id }}</label></div>
              <p v-if="!sharedGroups.length" class="text-sm text-amber-700 dark:text-amber-300">{{ t('admin.auditCenter.emptyScope') }}</p>
            </template>
          </div>
          <div v-if="!allowlistResolved" class="space-y-2" data-test="audit-adopt-allowlist">
            <p class="text-sm text-amber-700 dark:text-amber-300">{{ t('admin.auditCenter.chooseExemptionSource') }}</p>
            <div class="flex flex-wrap gap-2"><button v-for="source in sources" :key="source" type="button" class="btn btn-secondary btn-sm" @click="adoptAllowlist(source)">{{ t('admin.auditCenter.useSource', { source: sourceLabel(source) }) }}</button></div>
          </div>
          <UserAllowlistSelector v-else :model-value="sharedAllowlist" :title="t('admin.auditCenter.exemptions')" :hint="t('admin.auditCenter.exemptionsHint')" :disabled="saving" test-id="audit-shared-allowlist" @update:model-value="setAllowlist" />
          <p v-if="snapshot.content.user_allowlist_inherited && !allowlistTouched" class="text-xs text-amber-700 dark:text-amber-300">{{ t('admin.auditCenter.inheritedAllowlist') }}</p>
        </section>

        <section id="audit-engines" class="scroll-mt-20 space-y-4">
          <div><h2 class="text-lg font-semibold">2 · {{ t('admin.auditCenter.steps.engines') }}</h2><p class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.auditCenter.enginesHint') }}</p></div>
          <div class="card space-y-4 p-4 sm:p-6">
            <div class="flex flex-wrap items-center justify-between gap-3"><h3 class="font-semibold">{{ t('admin.auditCenter.contentEngine') }}</h3><label class="flex items-center gap-2 text-sm"><input v-model="contentEnabled" type="checkbox" data-test="audit-content-enabled" />{{ t('common.enabled') }}</label></div>
            <div class="grid gap-4 sm:grid-cols-2">
              <label class="text-sm">{{ t('admin.auditCenter.execution') }}<select v-model="contentMode" class="input mt-2 w-full" data-test="audit-content-mode"><option value="pre_block">{{ t('admin.auditCenter.synchronous') }}</option><option value="observe">{{ t('admin.auditCenter.observation') }}</option><option value="off">{{ t('common.disabled') }}</option></select></label>
              <div class="rounded-lg bg-gray-50 p-3 text-sm dark:bg-dark-900/40"><p>{{ t('admin.auditCenter.contentSummary', { count: (snapshot.content.blocked_keywords ?? []).length, engine: snapshot.content.engine || 'openai' }) }}</p><p class="mt-2 text-xs text-gray-500">{{ t('admin.auditCenter.keywordFirst') }}</p></div>
            </div>
            <p v-if="contentEnabled && contentMode !== 'pre_block'" class="text-sm text-amber-700 dark:text-amber-300">{{ t('admin.auditCenter.observationNoCharge') }}</p>
            <details class="rounded-xl border border-gray-200 p-4 dark:border-dark-700"><summary class="cursor-pointer font-medium">{{ t('admin.auditCenter.contentAdvanced') }}</summary><p class="my-3 text-sm text-gray-500">{{ t('admin.auditCenter.advancedDraftHint') }}</p>
              <RiskControlView :key="generation" ref="contentEditor" embedded editor-only :initial-config="snapshot.content" :shared-risk-policy="policyTouched ? sharedPolicy : undefined" @changed="contentAdvancedDirty = $event" />
            </details>
          </div>
          <div class="card space-y-4 p-4 sm:p-6">
            <div class="flex flex-wrap items-center justify-between gap-3"><h3 class="font-semibold">{{ t('admin.auditCenter.promptEngine') }}</h3><label class="flex items-center gap-2 text-sm"><input v-model="prompt.enabled" type="checkbox" data-test="audit-prompt-enabled" />{{ t('common.enabled') }}</label></div>
            <div class="flex flex-wrap gap-5 text-sm"><label class="flex items-center gap-2"><input v-model="prompt.blocking_enabled" type="checkbox" :disabled="!prompt.enabled" />{{ t('admin.auditCenter.synchronous') }}</label><label class="flex items-center gap-2"><input v-model="prompt.blocking_latest_turn_only" type="checkbox" :disabled="!prompt.enabled || !prompt.blocking_enabled" />{{ t('admin.promptAudit.saveBar.blockingLatestTurnOnly') }}</label><label class="flex items-center gap-2"><input v-model="prompt.store_pass_events" type="checkbox" />{{ t('admin.promptAudit.saveBar.storePass') }}</label></div>
            <p v-if="prompt.enabled && !prompt.blocking_enabled" class="text-sm text-amber-700 dark:text-amber-300">{{ t('admin.auditCenter.observationNoCharge') }}</p>
            <details class="rounded-xl border border-gray-200 p-4 dark:border-dark-700"><summary class="cursor-pointer font-medium">{{ t('admin.auditCenter.promptRules', { count: prompt.local_rules.length }) }}</summary><LocalRulePanel :draft="effectivePrompt" @update:draft="replacePrompt" /></details>
            <EndpointPool v-if="prompt.engine === 'remote'" :endpoints="prompt.endpoints" :probe-results="probeResults" :probing-ids="probingIds" @update:endpoints="prompt.endpoints = $event" @probe="probeEndpoint" />
            <details class="rounded-xl border border-gray-200 p-4 dark:border-dark-700"><summary class="cursor-pointer font-medium">{{ t('admin.auditCenter.promptAdvanced') }}</summary><PolicyPanel :draft="effectivePrompt" :groups="groups" hide-shared @update:draft="replacePrompt" /></details>
          </div>
        </section>

        <section id="audit-actions" class="card scroll-mt-20 space-y-5 p-4 sm:p-6" data-test="audit-shared-actions">
          <div><h2 class="text-lg font-semibold">3 · {{ t('admin.auditCenter.steps.actions') }}</h2><p class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.auditCenter.actionsHint') }}</p></div>
          <div v-if="!policyResolved" class="space-y-3 rounded-xl bg-amber-50 p-4 dark:bg-amber-950/20" data-test="audit-adopt-policy"><p class="text-sm text-amber-800 dark:text-amber-200">{{ t('admin.auditCenter.choosePolicySource') }}</p><div class="grid gap-3 sm:grid-cols-2"><div v-for="source in ['content', 'prompt'] as const" :key="source" class="space-y-2 text-sm"><p class="font-medium">{{ sourceLabel(source) }} · {{ policySummary(snapshot[source].risk_policy) }}</p><button type="button" class="btn btn-secondary btn-sm" @click="adoptPolicy(source)">{{ t('admin.auditCenter.useSource', { source: sourceLabel(source) }) }}</button></div></div></div>
          <AuditRiskPolicyPanel v-else shared :model-value="sharedPolicy" audit="content" :synchronous="contentEnabled && contentMode === 'pre_block' && prompt.enabled && prompt.blocking_enabled" @update:model-value="setPolicy" />
          <p v-if="policyResolved && !sharedPolicy" class="text-sm text-amber-700 dark:text-amber-300">{{ t('admin.auditCenter.legacyPenalty') }}</p>
          <div class="space-y-3 rounded-xl border border-amber-200 p-4 dark:border-amber-900" data-test="audit-legacy-penalty"><label class="flex items-center gap-2 text-sm font-medium"><input v-model="legacyBanEnabled" type="checkbox" data-test="audit-legacy-ban" />{{ t('admin.auditCenter.legacyBanTitle') }}</label><p class="text-xs leading-5 text-gray-500">{{ t('admin.auditCenter.legacyBanHint') }}</p><details><summary class="cursor-pointer text-sm">{{ t('admin.auditCenter.legacyBanValues', { threshold: legacyBanThreshold, hours: legacyBanWindow }) }}</summary><div class="mt-3 grid gap-3 sm:grid-cols-2"><label class="text-sm">{{ t('admin.riskControl.banThreshold') }}<input v-model.number="legacyBanThreshold" type="number" min="1" max="1000" class="input mt-2 w-full" /></label><label class="text-sm">{{ t('admin.riskControl.violationWindowHours') }}<input v-model.number="legacyBanWindow" type="number" min="1" max="8760" class="input mt-2 w-full" /></label></div></details></div>
          <div class="grid gap-4 rounded-xl border border-gray-200 p-4 dark:border-dark-700 sm:grid-cols-3"><label class="flex items-center gap-2 text-sm"><input v-model="ranking.auto_disable_enabled" type="checkbox" />{{ t('securityPolicyRanking.autoDisable') }}</label><label class="text-sm">{{ t('securityPolicyRanking.threshold') }}<input v-model.number="ranking.auto_disable_threshold" type="number" min="1" max="1000000000" step="1" class="input mt-2 w-full" /></label><label class="flex items-center gap-2 text-sm"><input v-model="ranking.reset_on_manual_enable" type="checkbox" />{{ t('securityPolicyRanking.resetOnEnable') }}</label></div>
          <div><h3 class="font-medium">{{ t('admin.auditCenter.groupPenalties') }}</h3><p class="mt-2 text-sm text-gray-500">{{ t('admin.auditCenter.groupPenaltiesHint') }}</p></div>
          <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3"><div v-for="id in penaltyGroupIDs" :key="id" class="space-y-3 rounded-xl border border-gray-200 p-4 dark:border-dark-700"><p class="font-medium">{{ groupName(id) }} <span class="text-xs text-gray-500">#{{ id }}</span></p><label class="flex items-center gap-2 text-sm"><input type="checkbox" :checked="ruleFor(id).auto_deduct_enabled" :data-test="`audit-charge-group-${id}`" @change="updateRule(id, { auto_deduct_enabled: ($event.target as HTMLInputElement).checked })" />{{ t('securityPolicyRanking.groupAutoDeduct') }}</label><label v-if="ruleFor(id).auto_deduct_enabled" class="block text-sm font-semibold text-red-700 dark:text-red-300">{{ t('securityPolicyRanking.groupMultiplier') }}<input :value="ruleFor(id).multiplier" type="text" inputmode="decimal" maxlength="11" class="input mt-2 w-full !text-red-700 dark:!text-red-300" @input="updateRule(id, { multiplier: ($event.target as HTMLInputElement).value })" /></label><p v-else class="text-xs text-gray-500">{{ t('admin.auditCenter.chargeOff') }}</p><p v-if="!effectiveRankingGroups.includes(id)" class="text-xs text-amber-700 dark:text-amber-300">{{ t('securityPolicyRanking.groupRuleInactive') }}</p><button v-if="ranking.group_rules?.some(rule => rule.group_id === id)" type="button" class="text-xs text-primary-600 hover:underline dark:text-primary-400" @click="removeRule(id)">{{ t('securityPolicyRanking.clearGroupRule') }}</button></div></div>
          <div class="space-y-3 rounded-xl border border-gray-200 p-4 dark:border-dark-700" data-test="audit-upstream-minimum"><label class="block text-sm font-medium">{{ t('securityPolicyRanking.multiplier') }}<input v-model="ranking.multiplier" type="text" inputmode="decimal" maxlength="11" class="input mt-2 w-full sm:max-w-xs" data-test="audit-upstream-minimum-input" /></label><p class="text-sm leading-6 text-gray-500">{{ t('admin.auditCenter.upstreamMinimumHint') }}</p><p class="text-xs leading-5 text-gray-500">{{ t('securityPolicyRanking.multiplierHint') }}</p></div>
        </section>

        </fieldset>
        <section id="audit-evidence" class="scroll-mt-20 space-y-4">
          <div><h2 class="text-lg font-semibold">4 · {{ t('admin.auditCenter.steps.evidence') }}</h2><p class="mt-2 text-sm text-gray-500">{{ t('admin.auditCenter.evidenceHint') }}</p></div>
          <div class="card space-y-4 p-4 sm:p-6"><div class="flex flex-wrap items-center justify-between gap-3"><h3 class="font-medium">{{ t('admin.auditCenter.chargeDiagnostics') }}</h3><span class="text-xs text-gray-500">{{ t('admin.auditCenter.savedSnapshot') }}</span></div><p class="text-sm text-gray-500">{{ t('admin.auditCenter.readyHint') }}</p><div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3"><div v-for="row in snapshot.groups" :key="row.group_id" class="space-y-2 rounded-xl border border-gray-200 p-3 dark:border-dark-700"><p class="font-medium">{{ groupName(row.group_id) }}</p><p class="text-sm" :class="row.charge_state === 'ready' ? 'text-emerald-700 dark:text-emerald-300' : 'text-amber-700 dark:text-amber-300'">{{ stateLabel(row.charge_state) }}</p><ul v-if="row.reasons.length" class="list-inside list-disc text-xs leading-6 text-gray-600 dark:text-gray-300"><li v-for="reason in row.reasons" :key="reason">{{ reasonLabel(reason) }}</li></ul></div></div><p v-if="!snapshot.groups.length" class="text-sm text-gray-500">{{ t('admin.auditCenter.emptyScope') }}</p><p v-if="snapshot.charge_summary_sampled" class="text-xs text-gray-500" data-test="audit-outcome-sampling">{{ t('admin.auditCenter.summarySample', { limit: snapshot.charge_summary_sample_limit }) }}<span v-if="snapshot.charge_summary_from && snapshot.charge_summary_to"> {{ new Date(snapshot.charge_summary_from).toLocaleString() }} – {{ new Date(snapshot.charge_summary_to).toLocaleString() }}</span></p><AuditOutcomeTable :title="t('admin.auditCenter.chargeResults')" :rows="snapshot.charge_summary" :status-label="chargeStatus" :reason-label="reasonLabel" /><AuditOutcomeTable :title="t('admin.auditCenter.importResults')" :rows="snapshot.import_summary ?? []" :status-label="chargeStatus" :reason-label="reasonLabel" /><div class="space-y-2" data-test="audit-observation-summary"><p class="text-xs text-gray-500">{{ t('admin.auditCenter.observationHint') }}</p><AuditOutcomeTable :title="t('admin.auditCenter.observationResults')" :rows="snapshot.observation_summary ?? []" :status-label="observationStatus" :reason-label="reasonLabel" :source-label="observationSourceLabel" :empty-message="t('admin.auditCenter.noObservationEvidence')" /></div></div>
          <div class="flex flex-wrap gap-2" role="group" :aria-label="t('admin.auditCenter.eventSources')"><button v-for="source in eventSources" :key="source" type="button" :aria-pressed="eventSource === source" class="btn" :class="eventSource === source ? 'btn-primary' : 'btn-secondary'" @click="eventSource = source">{{ sourceLabel(source) }}</button></div>
          <SecurityPolicyRankingView v-if="eventSource === 'ranking'" :key="`ranking-${generation}`" embedded />
          <RiskControlView v-else-if="eventSource === 'content'" :key="`events-${generation}`" embedded />
          <PromptAuditView v-else embedded />
        </section>
      </template>
    </div>
    <div v-if="snapshot" class="fixed inset-x-0 bottom-0 z-30 border-t border-gray-200 bg-white/95 px-4 py-3 shadow-lg backdrop-blur dark:border-dark-700 dark:bg-dark-900/95 lg:left-[var(--app-sidebar-width)]"><div class="mx-auto flex max-w-[1600px] flex-wrap items-center justify-between gap-3"><p class="text-sm" :class="dirty ? 'text-amber-700 dark:text-amber-300' : 'text-gray-500'">{{ t(dirty ? 'admin.auditCenter.unsaved' : 'admin.auditCenter.synced') }}</p><div class="flex gap-2"><button type="button" class="btn btn-secondary" :disabled="!dirty || saving" @click="reset">{{ t('common.reset') }}</button><button type="button" class="btn btn-primary" :disabled="!dirty || saving || loading" data-test="audit-save-all" @click="save">{{ t(saving ? 'common.saving' : 'admin.auditCenter.saveAll') }}</button></div></div></div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import UserAllowlistSelector from '@/components/admin/user/UserAllowlistSelector.vue'
import AuditRiskPolicyPanel from '@/components/admin/AuditRiskPolicyPanel.vue'
import AuditOutcomeTable from '@/components/admin/AuditOutcomeTable.vue'
import RiskControlView from './RiskControlView.vue'
import SecurityPolicyRankingView from './SecurityPolicyRankingView.vue'
import PromptAuditView from '@/features/prompt-audit/PromptAuditView.vue'
import LocalRulePanel from '@/features/prompt-audit/components/LocalRulePanel.vue'
import EndpointPool from '@/features/prompt-audit/components/EndpointPool.vue'
import PolicyPanel from '@/features/prompt-audit/components/PolicyPanel.vue'
import promptAuditAPI from '@/features/prompt-audit/api'
import { buildUpdateRequest, cloneData, configToDraft, draftFingerprint } from '@/features/prompt-audit/viewModel'
import type { PromptAuditDraft, PromptAuditEndpointDraft, PromptAuditGroup, PromptProbeResult } from '@/features/prompt-audit/types'
import { auditCenterAPI } from '@/api/admin/auditCenter'
import type { AuditCenterConfig, AuditCenterShared, AuditCenterUpdate } from '@/api/admin/auditCenter'
import type { UpdateContentModerationConfig, ModerationMode } from '@/api/admin/riskControl'
import type { AuditRiskPolicy } from '@/types/auditRiskPolicy'
import { validAuditRiskPolicy, validAuditScore } from '@/types/auditRiskPolicy'
import type { SecurityPolicyGroupRule, SecurityPolicyRankingSettings } from '@/types/securityPolicyRanking'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

type Source = 'content' | 'prompt' | 'ranking'
const { t, te } = useI18n()
const route = useRoute()
const app = useAppStore()
const steps = ['scope', 'engines', 'actions', 'evidence'] as const
const sources: Source[] = ['content', 'prompt', 'ranking']
const eventSources: Source[] = ['ranking', 'content', 'prompt']
const eventSource = ref<Source>(route.query.source === 'content' || route.query.source === 'prompt' ? route.query.source : 'ranking')
const snapshot = ref<AuditCenterConfig | null>(null)
const groups = ref<PromptAuditGroup[]>([])
const prompt = ref<PromptAuditDraft | null>(null)
const ranking = ref<SecurityPolicyRankingSettings | null>(null)
const riskControlEnabled = ref(false)
const legacyBanEnabled = ref(false)
const legacyBanThreshold = ref(10)
const legacyBanWindow = ref(720)
const contentEnabled = ref(false)
const contentMode = ref<ModerationMode>('off')
const loading = ref(false)
const saving = ref(false)
const loadError = ref('')
const generation = ref(0)
const contentAdvancedDirty = ref(false)
const contentEditor = ref<{ preparePayload(): UpdateContentModerationConfig | undefined } | null>(null)
const groupSearch = ref('')
const sharedGroups = ref<number[]>([])
const sharedAllowlist = ref<number[]>([])
const sharedPolicy = ref<AuditRiskPolicy | null>(null)
const groupsTouched = ref(false)
const allowlistTouched = ref(false)
const policyTouched = ref(false)
const probeResults = ref<Record<string, PromptProbeResult>>({})
const probingIds = ref<string[]>([])
let initialPrompt = ''
let initialRanking = ''
const sorted = (ids: number[]) => [...new Set(ids)].sort((a, b) => a - b)
const sourceLabel = (source: Source) => t(`admin.auditCenter.sources.${source}`)
const sourceGroups = (source: Source) => snapshot.value?.[source].group_ids ?? []
const sourceAllowlist = (source: Source) => snapshot.value?.[source].user_allowlist_ids ?? []
const sameIDs = (field: 'group_ids' | 'user_allowlist_ids') => sources.every(source => JSON.stringify(sorted(snapshot.value?.[source][field] ?? [])) === JSON.stringify(sorted(snapshot.value?.content[field] ?? [])))
const groupsResolved = computed(() => groupsTouched.value || sameIDs('group_ids'))
const allowlistResolved = computed(() => allowlistTouched.value || sameIDs('user_allowlist_ids'))
const policyResolved = computed(() => policyTouched.value || JSON.stringify(snapshot.value?.content.risk_policy ?? null) === JSON.stringify(snapshot.value?.prompt.risk_policy ?? null))
const hasDifferences = computed(() => !sameIDs('group_ids') || !sameIDs('user_allowlist_ids') || !policyResolved.value)
const visibleGroups = computed(() => groups.value.filter(group => `${group.name} ${group.id}`.toLowerCase().includes(groupSearch.value.trim().toLowerCase())))
const missingGroups = computed(() => sharedGroups.value.filter(id => !groups.value.some(group => group.id === id)))
const effectiveRankingGroups = computed(() => groupsTouched.value ? sharedGroups.value : ranking.value?.group_ids ?? [])
const penaltyGroupIDs = computed(() => sorted([...effectiveRankingGroups.value, ...(ranking.value?.group_rules ?? []).map(rule => rule.group_id)]))
const effectivePrompt = computed(() => ({ ...prompt.value!, ...(policyTouched.value ? { risk_policy: sharedPolicy.value } : {}) }))
const promptDirty = computed(() => draftFingerprint(prompt.value) !== initialPrompt)
const rankingDirty = computed(() => JSON.stringify(ranking.value) !== initialRanking)
const contentQuickDirty = computed(() => contentEnabled.value !== snapshot.value?.content.enabled || contentMode.value !== snapshot.value?.content.mode)
const globalDirty = computed(() => riskControlEnabled.value !== snapshot.value?.risk_control_enabled)
const legacyDirty = computed(() => legacyBanEnabled.value !== !!snapshot.value?.content.auto_ban_enabled || legacyBanThreshold.value !== (snapshot.value?.content.ban_threshold ?? 10) || legacyBanWindow.value !== (snapshot.value?.content.violation_window_hours ?? 720))
const dirty = computed(() => !!snapshot.value && (groupsTouched.value || allowlistTouched.value || policyTouched.value || contentAdvancedDirty.value || contentQuickDirty.value || globalDirty.value || legacyDirty.value || promptDirty.value || rankingDirty.value))
function groupName(id: number) { return groups.value.find(group => group.id === id)?.name ?? `#${id}` }
function idsLabel(ids: number[]) { return ids.length ? ids.map(id => `#${id}`).join(', ') : t('admin.auditCenter.none') }
function adoptGroups(source: Source) { sharedGroups.value = [...sourceGroups(source)]; groupsTouched.value = true }
function adoptAllowlist(source: Source) { setAllowlist([...sourceAllowlist(source)]) }
function setAllowlist(ids: number[]) { sharedAllowlist.value = sorted(ids); allowlistTouched.value = true }
function toggleGroup(id: number) { sharedGroups.value = sharedGroups.value.includes(id) ? sharedGroups.value.filter(value => value !== id) : sorted([...sharedGroups.value, id]); groupsTouched.value = true }
function adoptPolicy(source: 'content' | 'prompt') { setPolicy(cloneData(snapshot.value![source].risk_policy ?? null)) }
function setPolicy(policy: AuditRiskPolicy | null) { sharedPolicy.value = policy; policyTouched.value = true }
function policySummary(policy?: AuditRiskPolicy | null) { return policy ? `${policy.low_threshold}/${policy.medium_threshold}/${policy.high_threshold} · ${[policy.low_action, policy.medium_action, policy.high_action].map(action => t(`admin.promptAudit.riskPolicy.actions.${action}`)).join(' / ')}` : t('admin.auditCenter.legacyMode') }
function replacePrompt(value: PromptAuditDraft) { prompt.value = { ...value, risk_policy: prompt.value?.risk_policy } }
function ruleFor(id: number): SecurityPolicyGroupRule { return ranking.value?.group_rules?.find(rule => rule.group_id === id) ?? { group_id: id, multiplier: '1', auto_deduct_enabled: false } }
function updateRule(id: number, patch: Partial<SecurityPolicyGroupRule>) { if (!ranking.value) return; const rule = { ...ruleFor(id), ...patch }; ranking.value.group_rules = [...(ranking.value.group_rules ?? []).filter(item => item.group_id !== id), rule] }
function removeRule(id: number) { if (ranking.value) ranking.value.group_rules = (ranking.value.group_rules ?? []).filter(rule => rule.group_id !== id) }
function stateLabel(state: string) { const key = `admin.auditCenter.states.${state}`; return te(key) ? t(key) : t('admin.auditCenter.states.unknown') }
function observationStatus() { return t('admin.auditCenter.observationStatus') }
function observationSourceLabel(source: string) { return source === 'content_observation' ? sourceLabel('content') : source === 'prompt_observation' ? sourceLabel('prompt') : t('admin.auditCenter.none') }
function chargeStatus(status: string) { const key = `securityPolicyRanking.chargeStatuses.${status}`; return te(key) ? t(key) : stateLabel(status) }
function reasonLabel(reason: string) {
  const band = /^(content|prompt)_(low|medium|high)_(allow|block|charge_allow)$/.exec(reason)
  if (band) {
    const source = band[1] as 'content' | 'prompt'
    const level = band[2] as 'low' | 'medium' | 'high'
    return t('admin.auditCenter.riskBandAction', { source: sourceLabel(source), level: t('admin.promptAudit.riskLevels.' + level), threshold: snapshot.value?.[source].risk_policy?.[level + '_threshold' as keyof AuditRiskPolicy] ?? '—', action: t('admin.promptAudit.riskPolicy.actions.' + band[3]) })
  }
  const direct = `admin.auditCenter.reasons.${reason}`; if (te(direct)) return t(direct); const existing = `securityPolicyRanking.chargeReasons.${reason}`; return te(existing) ? t(existing) : t('admin.auditCenter.reasonUnknown', { reason }) }
function apply(config: AuditCenterConfig) {
  snapshot.value = cloneData({ ...config, groups: config.groups ?? [], charge_summary: config.charge_summary ?? [] })
  // Defaults remain available through explicit template buttons only.
  prompt.value = { ...configToDraft(config.prompt), engine: config.prompt.engine ?? 'remote', local_rules: cloneData(config.prompt.local_rules ?? []), audit_instructions: config.prompt.audit_instructions ?? '' }
  ranking.value = cloneData(config.ranking)
  riskControlEnabled.value = config.risk_control_enabled
  legacyBanEnabled.value = !!config.content.auto_ban_enabled
  legacyBanThreshold.value = config.content.ban_threshold ?? 10
  legacyBanWindow.value = config.content.violation_window_hours ?? 720
  contentEnabled.value = config.content.enabled
  contentMode.value = config.content.mode
  sharedGroups.value = [...(config.content.group_ids ?? [])]
  sharedAllowlist.value = [...(config.content.user_allowlist_ids ?? [])]
  sharedPolicy.value = cloneData(config.content.risk_policy ?? null)
  groupsTouched.value = allowlistTouched.value = policyTouched.value = contentAdvancedDirty.value = false
  initialPrompt = draftFingerprint(prompt.value)
  initialRanking = JSON.stringify(ranking.value)
  probeResults.value = {}
  generation.value++
}
function reset() { if (snapshot.value) apply(snapshot.value) }
async function load() {
  loading.value = true; loadError.value = ''
  try { const [config, allGroups] = await Promise.all([auditCenterAPI.getConfig(), promptAuditAPI.listGroups()]); groups.value = allGroups; apply(config) }
  catch (error) { loadError.value = extractApiErrorMessage(error, t('admin.auditCenter.loadFailed')) }
  finally { loading.value = false }
}
async function save() {
  if (!snapshot.value || !prompt.value || !ranking.value || saving.value) return
  if ((policyTouched.value && !validAuditRiskPolicy(sharedPolicy.value)) || !validAuditRiskPolicy(prompt.value.risk_policy) || prompt.value.local_rules.some(rule => rule.score !== undefined && !validAuditScore(rule.score))) { app.showError(t('admin.promptAudit.riskPolicy.invalid')); return }
  const minimum = ranking.value.multiplier.trim()
  if (rankingDirty.value && (!/^\d+(\.\d{1,6})?$/.test(minimum) || Number(minimum) < 0.000001 || Number(minimum) > 1000)) { app.showError(t('securityPolicyRanking.invalidMultiplier')); return }
  const shared: AuditCenterShared = {}
  if (groupsTouched.value) shared.group_ids = sorted(sharedGroups.value)
  if (allowlistTouched.value) shared.user_allowlist_ids = sorted(sharedAllowlist.value)
  if (policyTouched.value) shared.risk_policy = sharedPolicy.value
  const payload: AuditCenterUpdate = { expected_revision: snapshot.value.revision }
  if (globalDirty.value) payload.risk_control_enabled = riskControlEnabled.value
  if (Object.keys(shared).length) payload.shared = shared
  if (contentAdvancedDirty.value) { const content = contentEditor.value?.preparePayload(); if (!content) return; payload.content = content }
  if (contentQuickDirty.value || payload.content) payload.content = { ...payload.content, enabled: contentEnabled.value, mode: contentMode.value }
  if (legacyDirty.value) payload.content = { ...payload.content, auto_ban_enabled: legacyBanEnabled.value, ban_threshold: legacyBanThreshold.value, violation_window_hours: legacyBanWindow.value }
  if (promptDirty.value) payload.prompt = buildUpdateRequest(prompt.value)
  if (rankingDirty.value) payload.ranking = { ...cloneData(ranking.value), multiplier: minimum }
  saving.value = true
  try { const updated = await auditCenterAPI.updateConfig(payload); apply(updated); app.showSuccess(t('admin.auditCenter.saved')) }
  catch (error) { app.showError(extractApiErrorMessage(error, t('admin.auditCenter.saveFailed'))) }
  finally { saving.value = false }
}
async function probeEndpoint(endpoint: PromptAuditEndpointDraft) {
  if (probingIds.value.includes(endpoint.id)) return
  probingIds.value.push(endpoint.id)
  try { probeResults.value[endpoint.id] = await promptAuditAPI.probeEndpoint(endpoint) }
  catch (error) { app.showError(extractApiErrorMessage(error, t('admin.promptAudit.errors.probe'))) }
  finally { probingIds.value = probingIds.value.filter(id => id !== endpoint.id) }
}
onMounted(load)
</script>
