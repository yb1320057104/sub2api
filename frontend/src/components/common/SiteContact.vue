<template>
  <section
    v-if="hasContact"
    :id="sectionId"
    class="card site-contact motion-reduce:transition-none"
    :class="compact ? 'site-contact-compact' : 'site-contact-featured'"
    :aria-labelledby="`${sectionId}-title`"
    tabindex="-1"
  >
    <div class="site-contact-copy">
      <span class="site-contact-icon" aria-hidden="true"><Icon name="mail" size="md" /></span>
      <div class="site-contact-content">
        <h2 :id="`${sectionId}-title`" class="site-contact-heading">{{ t('common.contactUs') }}</h2>
        <p v-if="contactText" class="site-contact-text">{{ contactText }}</p>
        <p v-if="contactQRCode" class="site-contact-scan">{{ t('common.scanToContact') }}</p>
      </div>
    </div>
    <figure v-if="contactQRCode" class="site-contact-qr">
      <img :src="contactQRCode" :alt="t('common.contactQRCode')" loading="lazy" decoding="async" />
    </figure>
  </section>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useSiteContact } from '@/composables/useSiteContact'
import Icon from '@/components/icons/Icon.vue'

withDefaults(defineProps<{ compact?: boolean; sectionId?: string }>(), {
  compact: false,
  sectionId: 'site-contact',
})
const { t } = useI18n()
const { contactText, contactQRCode, hasContact } = useSiteContact()
</script>

<style scoped>
.site-contact {
  display: flex;
  flex-wrap: wrap;
  min-width: 0;
  align-items: center;
  justify-content: space-between;
  gap: 1.25rem;
  scroll-margin-top: 2rem;
}
.site-contact:focus-visible {
  outline: 2px solid theme('colors.primary.400');
  outline-offset: 5px;
}
.site-contact-featured {
  @apply border-primary-100 dark:border-dark-700/50;
  padding: 1.25rem 1.5rem;
}
.site-contact-copy {
  display: flex;
  flex: 1 1 15rem;
  min-width: 0;
  align-items: center;
  gap: 0.875rem;
}
.site-contact-content { min-width: 0; }
.site-contact-heading { @apply text-sm font-medium text-gray-500 dark:text-dark-400; }
.site-contact-icon {
  @apply flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-primary-50 text-primary-600 dark:bg-primary-900/30 dark:text-primary-400;
}
.site-contact-text {
  @apply font-semibold text-gray-900 dark:text-gray-100;
  margin-top: 0.25rem;
  font-size: 1.125rem;
  line-height: 1.5;
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}
.site-contact-scan { @apply mt-1 text-xs leading-5 text-gray-400 dark:text-dark-400; }
.site-contact-qr {
  @apply border border-gray-100;
  flex-shrink: 0;
  width: 128px;
  max-width: 100%;
  padding: 8px;
  border-radius: 12px;
  background: white;
}
.site-contact-qr img { display: block; width: 100%; height: auto; max-height: 160px; object-fit: contain; }
.site-contact-compact {
  @apply rounded-2xl bg-canvas/70 p-4 dark:bg-dark-800/50;
  gap: 1rem;
}
.site-contact-compact .site-contact-copy { flex-basis: 11rem; gap: 0.75rem; }
.site-contact-compact .site-contact-icon { @apply h-9 w-9; }
.site-contact-compact .site-contact-text { font-size: 1rem; }
.site-contact-compact .site-contact-qr { width: 112px; }
</style>
