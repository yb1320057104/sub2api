<template>
  <!-- Custom Home Content: Full Page Mode -->
  <div v-if="hasHomeContent" class="min-h-screen">
    <nav v-if="showIntelligenceAnchor || hasContact" class="home-custom-links" :aria-label="t('home.redesign.home')">
      <a v-if="showIntelligenceAnchor" href="#home-intelligence-test" class="home-nav-link" @click="scrollToSection($event, 'home-intelligence-test')">{{ t('intelligenceTest.title') }}</a>
      <a v-if="hasContact" href="#home-contact" class="home-contact-link" @click="scrollToSection($event, 'home-contact')">{{ t('common.contactSection') }}</a>
    </nav>
    <!-- iframe mode -->
    <iframe
      v-if="isHomeContentUrl"
      :src="homeContent.trim()"
      class="h-screen w-full border-0"
      allowfullscreen
    ></iframe>
    <!-- HTML mode - SECURITY: homeContent is admin-only setting, XSS risk is acceptable -->
    <div v-else v-html="homeContent"></div>
    <div class="home-container">
      <HomeIntelligenceResults class="home-extra-section" @visibility-change="showIntelligenceAnchor = $event" />
      <SiteContact class="home-extra-section" section-id="home-contact" />
    </div>
  </div>

  <!-- Compact Home Page -->
  <div
    v-else-if="compactHomeEnabled"
    data-testid="compact-home"
    class="flex min-h-screen flex-col bg-gray-50 text-gray-900 dark:bg-dark-950 dark:text-white"
  >
    <header class="border-b border-gray-200 px-4 py-4 sm:px-6 dark:border-dark-800">
      <nav class="mx-auto flex max-w-5xl flex-wrap items-center justify-between gap-3 sm:gap-4">
        <div class="flex min-w-0 flex-1 items-center gap-3">
          <img
            :src="siteLogo || '/dp-api-logo.png'"
            alt="Logo"
            class="h-9 w-9 shrink-0 rounded-lg object-contain"
          />
          <span class="min-w-0 truncate text-base font-semibold">{{ siteName }}</span>
        </div>
        <div class="flex max-w-full shrink-0 flex-wrap items-center justify-end gap-2">
          <a v-if="showIntelligenceAnchor" href="#home-intelligence-test" class="home-nav-link" @click="scrollToSection($event, 'home-intelligence-test')">{{ t('intelligenceTest.title') }}</a>
          <a v-if="hasContact" href="#home-contact" class="home-contact-link" @click="scrollToSection($event, 'home-contact')">{{ t('common.contactSection') }}</a>
          <LocaleSwitcher />
          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg text-gray-500 hover:bg-gray-100 dark:text-dark-400 dark:hover:bg-dark-800"
            :title="t('home.viewDocs')"
          >
            <Icon name="book" size="md" />
          </a>
          <router-link
            v-if="showModelPlazaEntry"
            to="/model-plaza"
            class="flex h-10 shrink-0 items-center gap-1.5 rounded-lg px-2.5 text-sm font-medium text-gray-500 hover:bg-gray-100 hover:text-gray-700 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-white"
            :title="t('nav.modelPlaza')"
          >
            <Icon name="grid" size="md" />
            <span class="hidden sm:inline">{{ t('nav.modelPlaza') }}</span>
          </router-link>
          <button
            class="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg text-gray-500 hover:bg-gray-100 dark:text-dark-400 dark:hover:bg-dark-800"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            @click="toggleTheme"
          >
            <Icon v-if="isDark" name="sun" size="md" />
            <Icon v-else name="moon" size="md" />
          </button>
          <router-link
            :to="isAuthenticated ? dashboardPath : '/login'"
            class="inline-flex min-h-10 shrink-0 items-center justify-center rounded-lg bg-gray-900 px-4 py-2 text-sm font-medium text-white hover:bg-gray-800 dark:bg-white dark:text-gray-900 dark:hover:bg-gray-200"
          >
            {{ isAuthenticated ? t('home.dashboard') : t('home.login') }}
          </router-link>
        </div>
      </nav>
    </header>

    <main class="flex min-w-0 flex-1 items-center justify-center px-4 py-16 sm:px-6">
      <div class="min-w-0 max-w-2xl text-center">
        <img
          :src="siteLogo || '/dp-api-logo.png'"
          alt="Logo"
          class="mx-auto mb-6 h-20 w-20 rounded-2xl object-contain"
        />
        <h1 class="[overflow-wrap:anywhere] text-3xl font-bold md:text-4xl">{{ siteName }}</h1>
        <p class="mt-4 whitespace-pre-wrap [overflow-wrap:anywhere] text-base text-gray-600 dark:text-dark-300">{{ siteSubtitle }}</p>
        <router-link
          :to="isAuthenticated ? dashboardPath : '/login'"
          class="mt-8 inline-flex min-h-10 items-center justify-center rounded-lg bg-primary-600 px-5 py-2.5 text-sm font-medium text-white hover:bg-primary-700"
        >
          {{ isAuthenticated ? t('home.goToDashboard') : t('home.login') }}
        </router-link>
      </div>
    </main>

    <div class="mx-auto w-full max-w-5xl px-4 sm:px-6">
      <HomeIntelligenceResults class="home-extra-section" @visibility-change="showIntelligenceAnchor = $event" />
      <SiteContact class="home-extra-section" section-id="home-contact" />
    </div>

    <footer class="min-w-0 border-t border-gray-200 px-4 py-5 text-center text-sm text-gray-500 [overflow-wrap:anywhere] sm:px-6 dark:border-dark-800 dark:text-dark-400">
      &copy; {{ currentYear }} {{ siteName }}
    </footer>
  </div>

  <!-- Default Home Page -->
  <div v-else data-testid="default-home" class="default-home">
    <header class="home-header">
      <div class="home-container home-header-inner">
        <router-link to="/" class="home-brand" :title="siteName">
          <img :src="siteLogo || '/dp-api-logo.png'" alt="" class="home-brand-logo" />
          <span class="home-brand-name">{{ siteName }}</span>
        </router-link>

        <nav class="home-navigation" :aria-label="t('home.redesign.home')">
          <router-link to="/" class="home-nav-link home-nav-link-active" aria-current="page">
            {{ t('home.redesign.home') }}
          </router-link>
          <router-link v-if="showModelPlazaEntry" to="/model-plaza" class="home-nav-link">
            {{ t('nav.modelPlaza') }}
          </router-link>
          <a v-if="showIntelligenceAnchor" href="#home-intelligence-test" class="home-nav-link" @click="scrollToSection($event, 'home-intelligence-test')">{{ t('intelligenceTest.title') }}</a>
          <a v-if="hasContact" href="#home-contact" class="home-contact-link" @click="scrollToSection($event, 'home-contact')">{{ t('common.contactSection') }}</a>
          <a v-if="docUrl" :href="docUrl" target="_blank" rel="noopener noreferrer" class="home-nav-link">
            {{ t('home.redesign.docs') }}
          </a>
        </nav>

        <div class="home-header-actions">
          <LocaleSwitcher />
          <button
            type="button"
            class="home-theme-toggle"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            :aria-label="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            @click="toggleTheme"
          >
            <Icon :name="isDark ? 'moon' : 'sun'" size="lg" aria-hidden="true" />
          </button>
          <router-link :to="isAuthenticated ? dashboardPath : '/login'" class="home-header-login">
            {{ isAuthenticated ? t('home.dashboard') : t('home.login') }}
          </router-link>
        </div>
      </div>
    </header>

    <main class="home-main">
      <div class="home-container">
        <section class="home-hero" aria-labelledby="home-hero-title">
          <div class="home-hero-copy">
            <p class="home-eyebrow">{{ t('home.redesign.eyebrow') }}</p>
            <h1 id="home-hero-title" class="home-title">
              <span>{{ t('home.redesign.titleLead') }}</span>
              <span class="home-title-accent">{{ t('home.redesign.titleAccent') }}</span>
            </h1>
            <p class="home-description">{{ heroDescription }}</p>

            <div class="home-hero-actions">
              <router-link
                :to="isAuthenticated ? dashboardPath : '/login'"
                data-testid="home-primary-action"
                class="home-primary-action"
              >
                <span>{{ isAuthenticated ? t('home.goToDashboard') : t('home.getStarted') }}</span>
                <Icon name="arrowRight" size="lg" aria-hidden="true" />
              </router-link>
              <router-link
                v-if="showModelPlazaEntry"
                to="/model-plaza"
                data-testid="home-models-action"
                class="home-models-action"
              >
                <span>{{ t('home.redesign.browseModels') }}</span>
                <Icon name="arrowRight" size="lg" aria-hidden="true" />
              </router-link>
            </div>

            <ul class="home-benefits">
              <li>{{ t('home.redesign.unifiedApi') }}</li>
              <li>{{ t('home.redesign.smartRouting') }}</li>
              <li>{{ t('home.redesign.usageTracking') }}</li>
            </ul>
          </div>
          <HomeGatewayVisual :site-name="siteName" :site-logo="siteLogo" />
        </section>

        <section class="home-features">
          <article class="home-feature">
            <Icon name="link" size="xl" class="home-feature-icon" aria-hidden="true" />
            <div>
              <h2>{{ t('home.redesign.features.connectTitle') }}</h2>
              <p>{{ t('home.redesign.features.connectDescription') }}</p>
            </div>
          </article>
          <article class="home-feature">
            <Icon name="swap" size="xl" class="home-feature-icon" aria-hidden="true" />
            <div>
              <h2>{{ t('home.redesign.features.routingTitle') }}</h2>
              <p>{{ t('home.redesign.features.routingDescription') }}</p>
            </div>
          </article>
          <article class="home-feature">
            <Icon name="document" size="xl" class="home-feature-icon" aria-hidden="true" />
            <div>
              <h2>{{ t('home.redesign.features.usageTitle') }}</h2>
              <p>{{ t('home.redesign.features.usageDescription') }}</p>
            </div>
          </article>
        </section>
        <HomeIntelligenceResults class="home-extra-section" @visibility-change="showIntelligenceAnchor = $event" />
        <SiteContact class="home-extra-section" section-id="home-contact" />
      </div>
    </main>

    <footer class="home-footer">
      <div class="home-container home-footer-inner">
        <p class="home-copyright">
          <img :src="siteLogo || '/dp-api-logo.png'" alt="" />
          <span>&copy; {{ currentYear }} {{ siteName }}</span>
        </p>
        <a v-if="docUrl" :href="docUrl" target="_blank" rel="noopener noreferrer" class="home-footer-docs">
          {{ t('home.redesign.docs') }}
        </a>
      </div>
    </footer>
  </div>
</template>
<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore, useAppStore } from '@/stores'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import Icon from '@/components/icons/Icon.vue'
import HomeGatewayVisual from '@/components/home/HomeGatewayVisual.vue'
import HomeIntelligenceResults from '@/components/home/HomeIntelligenceResults.vue'
import SiteContact from '@/components/common/SiteContact.vue'
import { useSiteContact } from '@/composables/useSiteContact'
import { sanitizeUrl } from '@/utils/url'
import { resolveSiteName } from '@/utils/branding'
import { FeatureFlags, isFeatureFlagEnabled } from '@/utils/featureFlags'

const { t } = useI18n()

const authStore = useAuthStore()
const appStore = useAppStore()
const { hasContact } = useSiteContact()
const showIntelligenceAnchor = ref(false)

function scrollToSection(event: MouseEvent, id: string) {
  const section = document.getElementById(id)
  if (!section) return
  event.preventDefault()
  section.scrollIntoView({
    behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth',
    block: 'start',
  })
  section.focus({ preventScroll: true })
}

// Site settings - directly from appStore (already initialized from injected config)
const siteName = computed(() => resolveSiteName(appStore.cachedPublicSettings?.site_name || appStore.siteName))
const siteLogo = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const siteSubtitle = computed(() => appStore.cachedPublicSettings?.site_subtitle || 'AI API Gateway Platform')
const heroDescription = computed(() => {
  const subtitle = siteSubtitle.value.trim()
  const isDefault = subtitle === 'AI API Gateway Platform'
    || subtitle === 'Subscription to API Conversion Platform'
  return isDefault ? t('home.redesign.description') : siteSubtitle.value
})
const docUrl = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.doc_url || appStore.docUrl || ''))
const homeContent = computed(() => appStore.cachedPublicSettings?.home_content || '')
const hasHomeContent = computed(() => homeContent.value.trim().length > 0)
const compactHomeEnabled = computed(() => appStore.cachedPublicSettings?.compact_home_enabled === true)
const modelPlazaEnabled = computed(() => isFeatureFlagEnabled(FeatureFlags.modelPlaza))

// Check if homeContent is a URL (for iframe display)
const isHomeContentUrl = computed(() => {
  const content = homeContent.value.trim()
  return content.startsWith('http://') || content.startsWith('https://')
})

// Theme
const isDark = ref(document.documentElement.classList.contains('dark'))

// Auth state
const isAuthenticated = computed(() => authStore.isAuthenticated)
const modelPlazaRequiresAuth = computed(
  () => appStore.cachedPublicSettings?.model_plaza_require_auth === true,
)
const showModelPlazaEntry = computed(
  () => modelPlazaEnabled.value && (isAuthenticated.value || !modelPlazaRequiresAuth.value),
)
const isAdmin = computed(() => authStore.isAdmin)
const dashboardPath = computed(() => isAdmin.value ? '/admin/dashboard' : '/dashboard')

// Current year for footer
const currentYear = computed(() => new Date().getFullYear())

// Toggle theme
function toggleTheme() {
  isDark.value = !isDark.value
  document.documentElement.classList.toggle('dark', isDark.value)
  localStorage.setItem('theme', isDark.value ? 'dark' : 'light')
}

// Initialize theme
function initTheme() {
  const savedTheme = localStorage.getItem('theme')
  if (
    savedTheme === 'dark' ||
    (!savedTheme && window.matchMedia('(prefers-color-scheme: dark)').matches)
  ) {
    isDark.value = true
    document.documentElement.classList.add('dark')
  }
}

onMounted(() => {
  initTheme()

  // Check auth state
  authStore.checkAuth()

  // Ensure public settings are loaded (will use cache if already loaded from injected config)
  if (!appStore.publicSettingsLoaded) {
    appStore.fetchPublicSettings()
  }
})
</script>
<style scoped>
.home-extra-section { margin-block: clamp(2.5rem, 5vw, 4.5rem); }
.home-custom-links {
  @apply flex flex-wrap items-center justify-center gap-5 border-b border-primary-100 bg-white px-4 py-4 dark:border-dark-800 dark:bg-dark-950;
}
.home-contact-link {
  @apply inline-flex shrink-0 items-center justify-center rounded-full bg-primary-100 px-4 py-2 text-sm font-semibold text-primary-700 transition-colors hover:bg-primary-200 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary-500 motion-reduce:transition-none dark:bg-primary-900 dark:text-primary-200 dark:hover:bg-primary-800;
  white-space: nowrap;
}
.default-home {
  @apply flex min-h-screen flex-col bg-white text-gray-900 dark:bg-dark-950 dark:text-white;
}

.home-container {
  width: 100%;
  max-width: 1536px;
  margin-inline: auto;
  padding-inline: clamp(1.25rem, 5vw, 5rem);
}

.home-header {
  @apply relative z-20 border-b border-primary-100/80 bg-white dark:border-dark-800 dark:bg-dark-950;
}

.home-header-inner {
  display: flex;
  min-height: 104px;
  align-items: center;
  gap: clamp(1.5rem, 4.5vw, 4.5rem);
}

.home-brand {
  display: flex;
  min-width: 0;
  max-width: 30%;
  align-items: center;
  gap: 0.75rem;
}

.home-brand-logo {
  width: 3.25rem;
  height: 3.25rem;
  flex-shrink: 0;
  object-fit: contain;
}

.home-brand-name {
  overflow: hidden;
  font-size: clamp(1.25rem, 2vw, 2rem);
  font-weight: 750;
  letter-spacing: -0.035em;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.home-navigation {
  display: flex;
  min-width: 0;
  align-self: stretch;
  align-items: center;
  gap: clamp(1.25rem, 3.5vw, 3.5rem);
}

.home-nav-link {
  @apply relative flex min-h-11 items-center text-gray-700 hover:text-primary-600 dark:text-dark-200 dark:hover:text-primary-300;
  font-size: 1.0625rem;
  font-weight: 500;
  white-space: nowrap;
}

.home-nav-link-active {
  @apply text-primary-600 dark:text-primary-300;
}

.home-nav-link-active::after {
  @apply absolute inset-x-0 -bottom-1 h-0.5 rounded-full bg-primary-500;
  content: '';
}

.home-header-actions {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  gap: clamp(0.5rem, 1.75vw, 1.75rem);
  margin-left: auto;
}

.home-theme-toggle {
  @apply flex h-11 w-11 shrink-0 items-center justify-center rounded-xl text-gray-600 hover:bg-primary-50 dark:text-dark-300 dark:hover:bg-dark-800;
}

.home-header-login,
.home-primary-action {
  @apply inline-flex items-center justify-center rounded-xl bg-primary-600 font-medium text-white hover:bg-primary-700;
}

.home-header-login {
  min-height: 3rem;
  padding: 0.625rem 1.625rem;
  font-size: 1rem;
}

.home-main {
  flex: 1;
  min-width: 0;
}

.home-hero {
  display: grid;
  min-height: 634px;
  grid-template-columns: minmax(0, 0.92fr) minmax(0, 1.08fr);
  align-items: center;
  gap: 1.5rem;
  padding-block: 4.5rem 3.25rem;
}

.home-hero-copy {
  position: relative;
  z-index: 1;
  min-width: 0;
}

.home-eyebrow {
  @apply text-primary-600 dark:text-primary-300;
  margin-bottom: 1.25rem;
  font-size: clamp(0.9375rem, 1.35vw, 1.25rem);
  font-weight: 600;
  letter-spacing: 0.06em;
  overflow-wrap: anywhere;
}

.home-title {
  font-size: clamp(2.5rem, 4.5vw, 4.375rem);
  font-weight: 750;
  letter-spacing: -0.045em;
  line-height: 1.3;
  overflow-wrap: anywhere;
  text-wrap: balance;
}

.home-title span {
  display: block;
}

.home-title-accent {
  @apply text-primary-600 dark:text-primary-400;
}

.home-description {
  @apply text-gray-500 dark:text-dark-300;
  margin-top: 1.25rem;
  font-size: clamp(1rem, 1.65vw, 1.5rem);
  line-height: 1.7;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.home-hero-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 1rem 2rem;
  margin-top: 1.875rem;
}

.home-primary-action,
.home-models-action {
  min-height: 3.75rem;
  max-width: 100%;
  gap: 1rem;
  padding: 0.875rem 1.75rem;
  font-size: clamp(1rem, 1.55vw, 1.375rem);
  overflow-wrap: anywhere;
}

.home-primary-action svg,
.home-models-action svg {
  flex-shrink: 0;
}

.home-models-action {
  @apply inline-flex items-center rounded-xl font-medium text-primary-600 hover:bg-primary-50 dark:text-primary-300 dark:hover:bg-primary-950;
  padding-inline: 0.5rem;
}

.home-benefits {
  @apply flex flex-wrap text-gray-500 dark:text-dark-400;
  gap: 0.625rem 1.25rem;
  margin-top: 1.5rem;
  font-size: clamp(0.8125rem, 1.25vw, 1.125rem);
}

.home-benefits li + li::before {
  content: '·';
  margin-right: 1.25rem;
}

.home-features {
  @apply grid border-t border-primary-100/80 dark:border-dark-800;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  padding-block: 3rem 3.5rem;
}

.home-feature {
  display: flex;
  min-width: 0;
  align-items: flex-start;
  gap: clamp(1rem, 2vw, 2rem);
  padding: 0.5rem clamp(1.25rem, 3vw, 3rem);
}

.home-feature:first-child {
  padding-left: 0;
}

.home-feature:last-child {
  padding-right: 0;
}

.home-feature + .home-feature {
  @apply border-l border-primary-100/80 dark:border-dark-800;
}

.home-feature-icon {
  @apply shrink-0 text-primary-500 dark:text-primary-400;
  width: 2.75rem;
  height: 2.75rem;
}

.home-feature h2 {
  margin: 0.25rem 0 0.75rem;
  font-size: clamp(1.125rem, 1.65vw, 1.5rem);
  font-weight: 650;
  overflow-wrap: anywhere;
}

.home-feature p {
  @apply text-gray-500 dark:text-dark-400;
  font-size: 1rem;
  line-height: 1.8;
  overflow-wrap: anywhere;
}

.home-footer {
  @apply border-t border-primary-100/80 dark:border-dark-800;
}

.home-footer-inner {
  @apply flex min-h-20 flex-wrap items-center justify-between text-sm text-gray-500 dark:text-dark-400;
  gap: 1rem 2rem;
  padding-block: 1rem;
}

.home-copyright {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 0.75rem;
  overflow-wrap: anywhere;
}

.home-copyright img {
  width: 1.875rem;
  height: 1.875rem;
  flex-shrink: 0;
  object-fit: contain;
}

.home-footer-docs {
  @apply inline-flex min-h-11 items-center hover:text-primary-600 dark:hover:text-primary-300;
}

.default-home :is(a, button):focus-visible {
  @apply rounded-lg outline-none ring-2 ring-primary-500 ring-offset-4 dark:ring-offset-dark-950;
}

@media (max-width: 1100px) {
  .home-header-inner {
    flex-wrap: wrap;
    gap: 1rem 1.75rem;
    padding-block: 1rem;
  }

  .home-navigation {
    order: 3;
    width: 100%;
    flex-wrap: wrap;
    gap: 0.25rem 1.5rem;
  }

  .home-header-actions {
    gap: 0.5rem;
  }

  .home-nav-link {
    font-size: 0.9375rem;
  }

  .home-hero {
    min-height: auto;
    grid-template-columns: minmax(0, 1fr);
    gap: 1.75rem;
    padding-block: 3.5rem 2.5rem;
  }

  .home-hero-copy {
    max-width: 42rem;
  }

  .home-title {
    font-size: clamp(2.5rem, 6.25vw, 4.375rem);
  }

  .home-hero :deep(.home-gateway) {
    width: 100%;
    max-width: 48rem;
    justify-self: center;
  }

  .home-feature {
    flex-direction: column;
    gap: 0.75rem;
  }
}

@media (max-width: 767px) {
  .home-header-inner {
    min-height: auto;
    flex-wrap: wrap;
    gap: 0.875rem 0.75rem;
    padding-top: 1rem;
    padding-bottom: 0.75rem;
  }

  .home-brand {
    flex: 1;
    max-width: none;
    gap: 0.5rem;
  }

  .home-brand-logo {
    width: 2rem;
    height: 2rem;
  }

  .home-brand-name {
    font-size: 1.125rem;
  }

  .home-navigation {
    order: 3;
    width: 100%;
    flex-wrap: wrap;
    gap: 0.25rem 1.5rem;
  }

  .home-nav-link {
    min-height: 2.5rem;
    font-size: 0.875rem;
  }

  .home-nav-link-active::after {
    bottom: 0;
  }

  .home-header-actions {
    gap: 0.125rem;
  }

  .home-theme-toggle {
    width: 2.25rem;
    height: 2.5rem;
  }

  .home-header-login {
    min-height: 2.5rem;
    padding: 0.5rem 0.75rem;
    font-size: 0.8125rem;
  }

  .home-hero {
    padding-block: 2.75rem 2rem;
  }

  .home-title {
    font-size: clamp(2.125rem, 6.25vw, 3rem);
  }

  .home-primary-action,
  .home-models-action {
    min-height: 3.25rem;
    gap: 0.75rem;
    padding: 0.75rem 1.25rem;
    font-size: 1rem;
  }

  .home-models-action {
    padding-inline: 0;
  }

  .home-hero-actions {
    gap: 0.75rem 1.25rem;
  }

  .home-benefits {
    gap: 0.5rem 0.75rem;
  }

  .home-benefits li + li::before {
    margin-right: 0.75rem;
  }

  .home-features {
    grid-template-columns: minmax(0, 1fr);
    gap: 1.75rem;
    padding-block: 2rem 2.5rem;
  }

  .home-feature {
    flex-direction: row;
    gap: 1.25rem;
    padding: 0;
  }

  .home-feature + .home-feature {
    border-left: 0;
  }

  .home-feature-icon {
    width: 2.25rem;
    height: 2.25rem;
  }

  .home-feature h2 {
    margin-top: 0;
    margin-bottom: 0.375rem;
  }

  .home-feature p {
    font-size: 0.9375rem;
  }
}
</style>
