<template>
  <div class="space-y-3">
    <p class="text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('keys.groupOrderHint', { max: maxGroups }) }}</p>
    <div v-if="modelValue.length" class="space-y-2 rounded-lg border border-gray-200 p-3 dark:border-dark-700" data-test="key-group-priority-list">
      <div v-for="(id, index) in modelValue" :key="id" class="flex items-center gap-2" :data-test="`key-group-priority-${id}`">
        <span class="w-5 shrink-0 text-xs text-gray-500">{{ index + 1 }}.</span>
        <KeyGroupBadge v-if="groupById.get(id)" class="min-w-0 flex-1" :group="groupById.get(id)!" :user-rate="userRates[id]" />
        <span v-else class="min-w-0 flex-1 text-sm text-amber-700 dark:text-amber-300">{{ t('keys.selectedGroupUnavailable', { id }) }}</span>
        <div class="flex shrink-0 gap-1">
          <button type="button" class="btn btn-secondary btn-sm" :disabled="disabled || index === 0" :aria-label="t('keys.moveGroupUp', { id })" :data-test="`key-group-up-${id}`" @click="move(index, -1)">↑</button>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="disabled || index === modelValue.length - 1" :aria-label="t('keys.moveGroupDown', { id })" :data-test="`key-group-down-${id}`" @click="move(index, 1)">↓</button>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="disabled" :aria-label="t('keys.removeGroup', { id })" :data-test="`key-group-remove-${id}`" @click.stop="remove(id)">×</button>
        </div>
      </div>
    </div>
    <input v-model="search" type="search" class="input w-full" :disabled="disabled" :placeholder="t('keys.searchGroup')" :aria-label="t('keys.searchGroup')" data-test="key-group-search" />
    <div class="max-h-64 space-y-2 overflow-auto rounded-lg border border-gray-200 p-2 dark:border-dark-700">
      <label v-for="group in filteredGroups" :key="group.id" class="flex cursor-pointer items-start gap-3 rounded-md p-2 hover:bg-gray-50 dark:hover:bg-dark-800">
        <input type="checkbox" class="mt-1" :checked="modelValue.includes(group.id)" :disabled="disabled || (!modelValue.includes(group.id) && modelValue.length >= maxGroups)" :aria-label="group.name" :data-test="`key-group-option-${group.id}`" @change="toggle(group.id)" />
        <span class="min-w-0 flex-1"><KeyGroupBadge :group="group" :user-rate="userRates[group.id]" /><span v-if="group.description" class="mt-1 block whitespace-pre-line text-xs text-gray-500">{{ group.description }}</span></span>
      </label>
      <p v-if="!filteredGroups.length" class="p-2 text-sm text-gray-500">{{ t('keys.noGroupFound') }}</p>
    </div>
    <p v-if="modelValue.length >= maxGroups" role="status" class="text-xs text-amber-700 dark:text-amber-300">{{ t('keys.groupSelectionLimit', { max: maxGroups }) }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import KeyGroupBadge from './KeyGroupBadge.vue'
import type { Group } from '@/types'

const props = withDefaults(defineProps<{ modelValue: number[]; groups: Group[]; userRates?: Record<number, number>; disabled?: boolean }>(), { userRates: () => ({}), disabled: false })
const emit = defineEmits<{ 'update:modelValue': [value: number[]] }>()
const { t } = useI18n()
const maxGroups = 20
const search = ref('')
const groupById = computed(() => new Map(props.groups.map((group) => [group.id, group])))
const filteredGroups = computed(() => {
  const query = search.value.trim().toLowerCase()
  return props.groups.filter((group) => `${group.id} ${group.name} ${group.description ?? ''}`.toLowerCase().includes(query))
})
function toggle(id: number) {
  if (props.disabled) return
  if (props.modelValue.includes(id)) remove(id)
  else if (props.modelValue.length < maxGroups) emit('update:modelValue', [...props.modelValue, id])
}
function remove(id: number) {
  if (props.disabled || !props.modelValue.includes(id)) return
  emit('update:modelValue', props.modelValue.filter((selected) => selected !== id))
}
function move(index: number, direction: number) {
  const target = index + direction
  if (props.disabled || target < 0 || target >= props.modelValue.length) return
  const ids = [...props.modelValue]
  ;[ids[index], ids[target]] = [ids[target], ids[index]]
  emit('update:modelValue', ids)
}
</script>
