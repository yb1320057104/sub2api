<template>
  <div>
    <label :for="inputId" class="input-label">{{ t('admin.accounts.hiddenRateMultiplier') }}</label>
    <input
      :id="inputId"
      v-model.number="multiplier"
      type="number"
      min="0"
      max="1000"
      step="0.000001"
      required
      :disabled="disabled"
      :aria-invalid="!valid"
      :aria-describedby="`${inputId}-hint`"
      class="input"
      data-testid="hidden-rate-multiplier"
    />
    <p :id="`${inputId}-hint`" class="input-hint">{{ t('admin.accounts.hiddenRateMultiplierHint') }}</p>
    <p v-if="!disabled && !valid" class="mt-1 text-sm text-red-500" role="alert">
      {{ t('admin.accounts.hiddenRateMultiplierInvalid') }}
    </p>
  </div>
</template>

<script setup lang="ts">
import { computed, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import { isHiddenBillingMultiplierValid } from '@/utils/hiddenBilling'

withDefaults(defineProps<{ disabled?: boolean }>(), { disabled: false })
const multiplier = defineModel<number | string>({ required: true })
const { t } = useI18n()
const inputId = useId()
const valid = computed(() => isHiddenBillingMultiplierValid(multiplier.value))
</script>
