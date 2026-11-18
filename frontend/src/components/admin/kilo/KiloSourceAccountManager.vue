<script setup lang="ts">
import { defineAsyncComponent, onBeforeUnmount, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { accountsAPI } from '@/api/admin/accounts'
import { getAllWithCount } from '@/api/admin/proxies'
import { getAll } from '@/api/admin/groups'
import { isKiloRequestCanceled, kiloErrorMessage } from '@/api/admin/kilo'
import type { Account, AdminGroup, Proxy } from '@/types'

const EditAccountModal = defineAsyncComponent(() => import('@/components/account/EditAccountModal.vue'))
const props = defineProps<{ sourceId: number }>()
const emit = defineEmits<{ close: []; updated: [account: Account] }>()
const { t } = useI18n()
const account = shallowRef<Account | null>(null)
const proxies = shallowRef<Proxy[]>([])
const groups = shallowRef<AdminGroup[]>([])
const loading = shallowRef(false)
const error = shallowRef('')
let generation = 0
let controller: AbortController | undefined

async function load() {
  const id = props.sourceId
  const current = ++generation
  controller?.abort()
  const request = new AbortController()
  controller = request
  account.value = null
  error.value = ''
  loading.value = true
  try {
    const [source, proxyItems, groupItems] = await Promise.all([
      accountsAPI.getById(id), getAllWithCount(), getAll('openai', request.signal)
    ])
    if (current !== generation || request.signal.aborted || id !== props.sourceId) return
    account.value = source
    proxies.value = proxyItems
    groups.value = groupItems
  } catch (cause) {
    if (current === generation && !request.signal.aborted && !isKiloRequestCanceled(cause)) error.value = kiloErrorMessage(cause, t('kilo.errors.sourceLoad'))
  } finally {
    if (current === generation) loading.value = false
  }
}
function close() {
  generation++
  controller?.abort()
  emit('close')
}
function updated(value: Account) {
  emit('updated', value)
  close()
}
watch(() => props.sourceId, () => { void load() }, { immediate: true })
onBeforeUnmount(() => { generation++; controller?.abort() })
</script>

<template>
  <EditAccountModal v-if="account" :show="true" :account="account" :proxies="proxies" :groups="groups" @close="close" @updated="updated" />
  <BaseDialog v-else :show="true" :title="t('kilo.identity.manageSource')" width="narrow" @close="close">
    <p class="text-sm leading-6 text-gray-600 dark:text-dark-300">{{ t('kilo.identity.sourcePurpose') }}</p>
    <p v-if="loading" class="mt-3 text-sm text-gray-500 dark:text-dark-400" role="status">{{ t('common.loading') }}</p>
    <p v-if="error" class="mt-3 break-words text-sm text-red-600 dark:text-red-400" role="alert">{{ error }}</p>
    <template #footer>
      <button type="button" class="btn btn-secondary" @click="close">{{ t('common.close') }}</button>
      <button v-if="error" type="button" class="btn btn-primary" :disabled="loading" @click="load">{{ t('common.refresh') }}</button>
    </template>
  </BaseDialog>
</template>
