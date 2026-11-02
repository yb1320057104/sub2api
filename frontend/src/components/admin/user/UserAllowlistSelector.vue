<template>
  <fieldset class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-700" :disabled="disabled" :data-test="testId">
    <legend class="px-1 text-sm font-medium text-gray-900 dark:text-white">{{ title }}</legend>
    <p class="text-sm text-gray-500 dark:text-gray-400">{{ hint }}</p>
    <div class="flex flex-wrap gap-2">
      <input v-model="query" type="search" class="input min-w-0 flex-1" :placeholder="t('admin.userAllowlist.search')" :aria-label="t('admin.userAllowlist.search')" data-test="allowlist-search" @input="invalidateSearch" @keydown.enter.prevent="search" />
      <button type="button" class="btn btn-secondary" :disabled="searching || !query.trim()" data-test="allowlist-search-button" @click="search">{{ searching ? t('common.loading') : t('common.search') }}</button>
      <button type="button" class="btn btn-secondary" :disabled="!selectedIds.length" data-test="allowlist-clear" @click="emit('update:modelValue', [])">{{ t('admin.userAllowlist.clear') }}</button>
    </div>
    <p v-if="searchError" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ t('admin.userAllowlist.searchFailed') }}</p>
    <div v-else-if="searched" class="max-h-52 space-y-1 overflow-auto rounded-lg bg-gray-50 p-2 dark:bg-dark-900/40" data-test="allowlist-results">
      <button v-for="user in results" :key="user.id" type="button" class="flex w-full items-center justify-between gap-3 rounded-md px-2 py-2 text-left text-sm hover:bg-white disabled:opacity-50 dark:hover:bg-dark-800" :disabled="selectedIds.includes(user.id) || selectedIds.length >= maxUsers" :data-test="`allowlist-add-${user.id}`" @click="add(user)">
        <span class="min-w-0 break-all">{{ user.email }}<span v-if="user.username" class="ml-2 text-gray-500">{{ user.username }}</span></span>
        <span class="shrink-0">#{{ user.id }}<span v-if="user.status === 'disabled'" class="ml-2 text-gray-500">{{ t('common.disabled') }}</span></span>
      </button>
      <p v-if="!results.length" class="px-2 py-2 text-sm text-gray-500">{{ t('admin.userAllowlist.noResults') }}</p>
      <p v-if="moreResults" class="px-2 py-2 text-xs text-gray-500">{{ t('admin.userAllowlist.refineSearch') }}</p>
    </div>
    <p class="text-xs text-gray-500" data-test="allowlist-count">{{ t('admin.userAllowlist.count', { count: selectedIds.length, max: maxUsers }) }}</p>
    <ul v-if="selectedIds.length" class="max-h-60 space-y-2 overflow-auto">
      <li v-for="id in selectedIds" :key="id" class="flex items-center justify-between gap-3 rounded-md bg-gray-50 px-3 py-2 text-sm dark:bg-dark-900/40" :data-test="`allowlist-selected-${id}`">
        <span class="min-w-0 break-all"><span class="font-medium">#{{ id }}</span><span v-if="users[id]" class="ml-2">{{ users[id]?.email }}</span><span v-if="users[id]?.deleted_at" class="ml-2 text-amber-700 dark:text-amber-300">{{ t('admin.userAllowlist.deleted') }}</span><span v-else-if="users[id] === null" class="ml-2 text-amber-700 dark:text-amber-300">{{ t('admin.userAllowlist.unresolved') }}</span><span v-else-if="users[id]?.status === 'disabled'" class="ml-2 text-gray-500">{{ t('common.disabled') }}</span></span>
        <button type="button" class="btn btn-secondary btn-sm shrink-0" :aria-label="t('admin.userAllowlist.remove', { id })" :data-test="`allowlist-remove-${id}`" @click="remove(id)">{{ t('common.delete') }}</button>
      </li>
    </ul>
    <p v-else class="text-xs text-gray-500">{{ t('admin.userAllowlist.empty') }}</p>
  </fieldset>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import usersAPI from '@/api/admin/users'
import type { AdminUser } from '@/types'

const props = withDefaults(defineProps<{ modelValue: number[]; title: string; hint: string; disabled?: boolean; testId?: string }>(), { disabled: false, testId: 'user-allowlist' })
const emit = defineEmits<{ 'update:modelValue': [value: number[]] }>()
const { t } = useI18n()
const maxUsers = 1000
const query = ref('')
const searching = ref(false)
const searched = ref(false)
const searchError = ref(false)
const moreResults = ref(false)
const results = ref<AdminUser[]>([])
const users = ref<Record<number, AdminUser | null>>({})
const loadingIds = new Set<number>()
const selectedIds = computed(() => [...new Set(props.modelValue)].sort((a, b) => a - b))
let revision = 0
let mounted = true
let controller: AbortController | undefined

function invalidateSearch() {
  revision++
  controller?.abort()
  controller = undefined
  searching.value = false
  searched.value = false
  searchError.value = false
  results.value = []
  moreResults.value = false
}

async function search() {
  if (props.disabled || !query.value.trim()) return
  invalidateSearch()
  const current = revision
  const value = query.value.trim()
  const requestController = new AbortController()
  controller = requestController
  searching.value = true
  try {
    let found: AdminUser[]
    let hasMore = false
    if (/^#?\d+$/.test(value)) {
      const id = Number(value.replace(/^#/, ''))
      found = Number.isSafeInteger(id) && id > 0 ? [await usersAPI.getById(id)] : []
    } else {
      const page = await usersAPI.list(1, 10, { search: value }, { signal: requestController.signal })
      found = page.items
      hasMore = page.total > page.items.length
    }
    if (!mounted || current !== revision) return
    results.value = found.filter((user) => !user.deleted_at)
    moreResults.value = hasMore
    for (const user of results.value) users.value[user.id] = user
    searched.value = true
  } catch {
    if (mounted && current === revision) searchError.value = true
  } finally {
    if (mounted && current === revision) searching.value = false
  }
}

function add(user: AdminUser) {
  if (props.disabled || user.deleted_at || selectedIds.value.includes(user.id) || selectedIds.value.length >= maxUsers) return
  users.value[user.id] = user
  emit('update:modelValue', [...selectedIds.value, user.id].sort((a, b) => a - b))
}

function remove(id: number) {
  if (!props.disabled) emit('update:modelValue', selectedIds.value.filter((selected) => selected !== id))
}

async function hydrate(ids: number[]) {
  const missing = ids.filter((id) => users.value[id] === undefined && !loadingIds.has(id))
  missing.forEach((id) => loadingIds.add(id))
  // Limit requests when an existing configuration contains many selected IDs.
  await Promise.all(Array.from({ length: Math.min(5, missing.length) }, async () => {
    while (mounted && missing.length) {
      const id = missing.shift()!
      try {
        const user = await usersAPI.getById(id, true)
        if (mounted) users.value[id] = user
      } catch {
        if (mounted) users.value[id] = null
      } finally {
        loadingIds.delete(id)
      }
    }
  }))
}

watch(selectedIds, (ids) => { void hydrate(ids) }, { immediate: true })
onUnmounted(() => { mounted = false; invalidateSearch() })
</script>
