<template>
  <BaseDialog :show="show" :title="t('tokenGuardV2.batchAdd')" width="wide" @close="close">
    <form id="token-guard-v2-batch" class="space-y-4" autocomplete="off" @submit.prevent="submit">
      <p class="text-sm leading-6 text-gray-600 dark:text-dark-300">{{ t('tokenGuardV2.batchHint') }}</p>
      <label class="block"><span class="input-label">{{ t('authorization.accountInput') }}</span><textarea v-model="raw" class="input min-h-48 font-mono text-sm" rows="8" :disabled="saving" :placeholder="t('tokenGuardV2.batchPlaceholder')" :maxlength="2 * 1024 * 1024" spellcheck="false" autocomplete="off" autocapitalize="off" /></label>
      <p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('tokenGuardV2.batchPrivacy') }}</p>
      <label class="block"><span class="input-label">{{ t('tokenGuardV2.loginProxy') }}</span>
        <select v-model="proxyChoice" data-testid="batch-login-proxy" class="input w-full" :disabled="saving || remoteEngine">
          <option value="random_managed">{{ t('tokenGuardV2.randomManagedProxy') }}</option>
          <option value="account">{{ t('tokenGuardV2.accountProxyDefault') }}</option>
          <option value="mihomo">{{ t('tokenGuardV2.mihomoManagedPool') }}</option>
          <option v-for="proxy in proxies" :key="proxy.id" :value="`proxy:${proxy.id}`">{{ proxy.name }} · {{ proxy.protocol.toUpperCase() }} · {{ proxy.host }}:{{ proxy.port }}</option>
        </select>
      </label>
      <p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t(remoteEngine ? 'tokenGuardV2.remoteEngineEgress' : 'tokenGuardV2.loginProxyHint') }}</p>
      <button type="button" class="text-sm font-medium text-primary-600 dark:text-primary-300" :disabled="saving" @click="openWorkbench">{{ t('tokenGuardV2.newAccountLink') }} →</button>
      <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
      <p v-if="notice" role="status" class="text-sm text-gray-600 dark:text-dark-300">{{ notice }}</p>
      <CredentialImportResults v-if="result" :result="result" />
    </form>
    <template #footer>
      <button type="button" class="btn btn-secondary" @click="close">{{ t(saving ? 'authorization.batchStop' : 'common.close') }}</button>
      <button type="submit" form="token-guard-v2-batch" class="btn btn-primary" :disabled="saving || !encryptionReady || !raw.trim()">{{ t(saving ? 'tokenGuardV2.saving' : 'tokenGuardV2.batchSave') }}</button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import CredentialImportResults from './CredentialImportResults.vue'
import { batchCreateTokenGuardV2Accounts, type TokenGuardV2BatchInput, type TokenGuardV2ImportResult, type TokenGuardV2ProxySource } from '@/api/admin/accountTokenGuardV2'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { Proxy } from '@/types'

const props = defineProps<{ show: boolean; proxies: Proxy[]; remoteEngine: boolean; encryptionReady: boolean }>()
const emit = defineEmits<{ close: []; saved: []; workbench: []; busy: [value: boolean] }>()
const { t } = useI18n()
const raw = ref('')
const proxyChoice = ref('random_managed')
const saving = ref(false)
const error = ref('')
const notice = ref('')
const result = ref<TokenGuardV2ImportResult | null>(null)
let request: AbortController | null = null
let generation = 0

function abort() {
  generation++
  request?.abort()
  request = null
  raw.value = ''
  saving.value = false
  emit('busy', false)
}
function close() {
  const wasSaving = saving.value
  abort()
  if (wasSaving) emit('saved')
  emit('close')
}
function openWorkbench() { close(); emit('workbench') }
async function submit() {
  if (saving.value || !props.encryptionReady || !raw.value.trim()) return
  error.value = ''; notice.value = ''; result.value = null
  if (raw.value.split(/\r?\n/).filter(line => line.trim()).length > 200 || new TextEncoder().encode(raw.value).length > 2 * 1024 * 1024) {
    error.value = t('tokenGuardV2.batchLimit'); return
  }
  const current = new AbortController()
  const version = ++generation
  const isSpecific = proxyChoice.value.startsWith('proxy:')
  const input: TokenGuardV2BatchInput = {
    accounts: raw.value,
    proxy_source: isSpecific ? 'managed_proxy' : proxyChoice.value as TokenGuardV2ProxySource,
    ...(isSpecific ? { proxy_id: Number(proxyChoice.value.slice(6)) } : {})
  }
  raw.value = ''
  request = current
  saving.value = true
  emit('busy', true)
  try {
    const value = await batchCreateTokenGuardV2Accounts(input, current.signal)
    if (version !== generation || current.signal.aborted || !props.show) return
    result.value = value
    emit('saved')
  } catch (cause) {
    if (version !== generation || current.signal.aborted || !props.show) return
    error.value = extractApiErrorMessage(cause, t('tokenGuardV2.batchFailed'))
    notice.value = t('tokenGuardV2.importUnconfirmed')
    emit('saved')
  } finally {
    input.accounts = ''
    if (version === generation) { saving.value = false; request = null; emit('busy', false) }
  }
}
function visibility() { if (document.hidden && props.show) close() }
watch(() => props.show, () => { abort(); error.value = ''; notice.value = ''; result.value = null; proxyChoice.value = 'random_managed' })
onMounted(() => document.addEventListener('visibilitychange', visibility))
onUnmounted(() => { abort(); document.removeEventListener('visibilitychange', visibility) })
</script>
