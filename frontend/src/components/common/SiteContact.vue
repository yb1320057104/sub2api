<template>
  <section
    v-if="hasContact"
    :id="sectionId"
    class="site-contact"
    :class="compact ? 'site-contact-compact' : 'site-contact-featured'"
    :aria-labelledby="`${sectionId}-title`"
    tabindex="-1"
  >
    <div class="site-contact-copy">
      <div class="site-contact-heading">
        <span class="site-contact-icon" aria-hidden="true"><Icon name="mail" size="lg" /></span>
        <h2 :id="`${sectionId}-title`">{{ t('common.contactUs') }}</h2>
      </div>
      <p v-if="contactText" class="site-contact-text">{{ contactText }}</p>
      <p v-if="contactQRCode" class="site-contact-scan">{{ t('common.scanToContact') }}</p>
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
  min-width: 0;
  align-items: center;
  justify-content: space-between;
  gap: 2rem;
  scroll-margin-top: 2rem;
}
.site-contact:focus-visible {
  outline: 2px solid theme('colors.primary.400');
  outline-offset: 5px;
}
.site-contact-featured {
  @apply rounded-3xl border border-primary-800 bg-primary-950 text-white shadow-lg dark:border-primary-700;
  padding: clamp(1.5rem, 4vw, 3rem);
}
.site-contact-copy { min-width: 0; }
.site-contact-heading { display: flex; align-items: center; gap: 0.75rem; }
.site-contact-heading h2 { font-size: 1.125rem; font-weight: 650; }
.site-contact-icon {
  @apply flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-primary-800 text-primary-200;
}
.site-contact-text {
  margin-top: 1rem;
  font-size: clamp(1.375rem, 2.5vw, 2rem);
  font-weight: 650;
  line-height: 1.5;
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}
.site-contact-scan { @apply mt-3 text-sm text-primary-200; }
.site-contact-qr {
  flex-shrink: 0;
  width: 196px;
  max-width: 100%;
  padding: 12px;
  border-radius: 16px;
  background: white;
}
.site-contact-qr img { display: block; width: 100%; height: auto; max-height: 240px; object-fit: contain; }
.site-contact-compact {
  @apply rounded-2xl border border-primary-200 bg-primary-50/70 p-4 text-gray-900 dark:border-primary-800 dark:bg-primary-950/40 dark:text-white;
  gap: 1rem;
}
.site-contact-compact .site-contact-heading h2 { font-size: 0.875rem; }
.site-contact-compact .site-contact-icon { @apply h-8 w-8 bg-primary-100 text-primary-600 dark:bg-primary-900 dark:text-primary-300; }
.site-contact-compact .site-contact-text { margin-top: 0.75rem; font-size: 1rem; }
.site-contact-compact .site-contact-scan { @apply text-primary-600 dark:text-primary-300; }
.site-contact-compact .site-contact-qr { width: 128px; padding: 8px; border-radius: 12px; }
@media (max-width: 540px) {
  .site-contact-featured { flex-direction: column; align-items: flex-start; }
}
@media (max-width: 380px) {
  .site-contact-compact { flex-direction: column; align-items: flex-start; }
}
</style>
