<template>
  <main class="login-layout">
    <section class="login-showcase" :aria-label="siteName">
      <header class="login-brand-row">
        <router-link to="/" class="login-brand" :aria-label="siteName">
          <img :src="siteLogo || '/dp-api-logo.png'" alt="" class="login-brand-logo" />
          <span>{{ siteName }}</span>
        </router-link>
        <div class="login-clock" aria-hidden="true">
          <span>{{ currentDate }}</span>
          <span>{{ t('auth.loginShowcase.localTime') }} <b>{{ currentTime }}</b></span>
        </div>
      </header>

      <div class="login-story">
        <p class="login-eyebrow"><span></span>{{ t('auth.loginShowcase.eyebrow') }}</p>
        <h1>
          <span>{{ t('auth.loginShowcase.headlineFirst') }}</span>
          <strong>{{ t('auth.loginShowcase.headlineSecond') }}</strong>
        </h1>
        <p class="login-description">{{ siteSubtitle }}</p>
        <div class="login-benefits">
          <span><Icon name="checkCircle" size="sm" />{{ t('auth.loginShowcase.unified') }}</span>
          <span><Icon name="checkCircle" size="sm" />{{ t('auth.loginShowcase.visible') }}</span>
        </div>
      </div>

      <LoginDial class="login-dial-position" :site-name="siteName" :site-logo="siteLogo" />

      <div class="login-marquee" aria-hidden="true">
        <div class="login-marquee-track">
          <div v-for="copy in 2" :key="copy" class="login-marquee-copy">
            <span v-for="item in marqueeItems" :key="item">{{ item }}<i></i></span>
          </div>
        </div>
      </div>

      <footer class="login-showcase-footer">
        <span>{{ siteName }} / AI WORKSPACE</span>
        <span>{{ t('auth.loginShowcase.footer') }}</span>
      </footer>
    </section>

    <section class="login-panel" :aria-label="t('auth.signIn')">
      <button
        type="button"
        class="login-theme-toggle"
        :aria-label="isDark ? t('nav.lightMode') : t('nav.darkMode')"
        :title="isDark ? t('nav.lightMode') : t('nav.darkMode')"
        @click="toggleTheme"
      >
        <Icon :name="isDark ? 'sun' : 'moon'" size="md" />
      </button>

      <div class="login-panel-content">
        <div class="login-card">
          <div class="login-card-status">
            <span><i></i>{{ t('auth.loginShowcase.accountAccess') }}</span>
            <span>ACCOUNT / LOGIN</span>
          </div>
          <slot />
        </div>
        <div v-if="$slots.footer" class="login-panel-footer"><slot name="footer" /></div>
        <p class="login-copyright">&copy; {{ currentYear }} {{ siteName }}. All rights reserved.</p>
      </div>
    </section>
  </main>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores'
import { resolveSiteName } from '@/utils/branding'
import { sanitizeUrl } from '@/utils/url'
import Icon from '@/components/icons/Icon.vue'
import LoginDial from '@/components/auth/LoginDial.vue'

const { t, locale } = useI18n()
const appStore = useAppStore()
const siteName = computed(() => resolveSiteName(appStore.siteName))
const siteLogo = computed(() => sanitizeUrl(appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const siteSubtitle = computed(() => appStore.cachedPublicSettings?.site_subtitle || t('auth.loginShowcase.description'))
const marqueeItems = computed(() => [
  t('auth.loginShowcase.models'),
  t('nav.apiKeys'),
  t('nav.usage'),
  t('auth.loginShowcase.quota'),
  t('auth.loginShowcase.unified')
])

const now = ref(new Date())
const currentYear = computed(() => now.value.getFullYear())
const currentDate = computed(() => now.value.toLocaleDateString(locale.value === 'zh' ? 'zh-CN' : 'en-CA'))
const currentTime = computed(() => now.value.toLocaleTimeString(locale.value === 'zh' ? 'zh-CN' : 'en-GB', { hour12: false }))
const isDark = ref(document.documentElement.classList.contains('dark'))
let clockInterval: ReturnType<typeof setInterval> | undefined

function toggleTheme() {
  isDark.value = !document.documentElement.classList.contains('dark')
  document.documentElement.classList.toggle('dark', isDark.value)
  try {
    localStorage.setItem('theme', isDark.value ? 'dark' : 'light')
  } catch {
    // The current-page toggle still works when persistent storage is unavailable.
  }
}

onMounted(() => {
  appStore.fetchPublicSettings()
  clockInterval = setInterval(() => {
    if (!document.hidden) now.value = new Date()
  }, 1000)
})

onBeforeUnmount(() => {
  if (clockInterval !== undefined) clearInterval(clockInterval)
})
</script>

<style scoped>
.login-layout {
  --login-accent: theme('colors.primary.600');
  --login-on-accent: theme('colors.white');
  --login-ink: theme('colors.primary.950');
  --login-muted: theme('colors.accent.500');
  --login-rule: theme('colors.primary.200 / 55%');
  --login-grid: theme('colors.primary.600 / 5%');
  --login-dial-ink: theme('colors.primary.800 / 65%');
  --login-dial-muted: theme('colors.primary.400 / 70%');
  --login-card: theme('colors.white / 92%');
  min-height: 100vh;
  min-height: 100svh;
  display: grid;
  grid-template-columns: minmax(0, 1.65fr) minmax(420px, 1fr);
  color: var(--login-ink);
  background-color: theme('colors.canvas');
  background-image: linear-gradient(var(--login-grid) 1px, transparent 1px), linear-gradient(90deg, var(--login-grid) 1px, transparent 1px);
  background-size: 60px 60px;
}

.dark .login-layout {
  --login-accent: theme('colors.primary.400');
  --login-on-accent: theme('colors.primary.950');
  --login-ink: theme('colors.dark.100');
  --login-muted: theme('colors.dark.400');
  --login-rule: theme('colors.dark.500 / 30%');
  --login-grid: theme('colors.primary.300 / 5%');
  --login-dial-ink: theme('colors.primary.100 / 75%');
  --login-dial-muted: theme('colors.primary.300 / 45%');
  --login-card: theme('colors.dark.900 / 94%');
  background-color: theme('colors.dark.950');
}

.login-showcase {
  position: sticky;
  top: 0;
  height: 100vh;
  height: 100svh;
  min-height: 700px;
  align-self: start;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  border-right: 1px solid var(--login-rule);
  padding: 42px clamp(36px, 4.2vw, 80px) 26px;
}

.login-brand-row { position: relative; z-index: 2; display: flex; align-items: center; justify-content: space-between; gap: 24px; }
.login-brand { display: inline-flex; align-items: center; gap: 13px; min-width: 0; max-width: 55%; font-size: 25px; font-weight: 800; letter-spacing: -.045em; }
.login-brand span { overflow-wrap: anywhere; }
.login-brand-logo { width: 43px; height: 43px; flex: none; object-fit: contain; border-radius: 12px; }
.login-clock { display: flex; align-items: center; gap: 20px; flex-shrink: 0; color: var(--login-muted); font: 11px/1.5 theme('fontFamily.mono'); }
.login-clock b { margin-left: 5px; color: var(--login-ink); font-weight: 500; font-variant-numeric: tabular-nums; }
.login-story { position: relative; z-index: 2; width: 67%; margin-top: auto; margin-bottom: auto; padding-top: 60px; padding-bottom: 90px; }
.login-eyebrow { display: flex; align-items: center; gap: 10px; margin-bottom: 26px; color: var(--login-muted); font-size: 13px; }
.login-eyebrow > span { width: 7px; height: 7px; flex: none; border-radius: 50%; background: var(--login-accent); }
.login-story h1 { font-size: clamp(44px, 4.2vw, 80px); font-weight: 800; line-height: 1.22; letter-spacing: -.065em; }
.login-story h1 > span, .login-story h1 > strong { display: block; }
.login-story h1 > strong { color: var(--login-accent); font-weight: inherit; }
.login-description { max-width: 430px; margin-top: 30px; color: var(--login-muted); font-size: 16px; line-height: 1.9; overflow-wrap: anywhere; }
.login-benefits { display: flex; flex-wrap: wrap; gap: 10px; margin-top: 30px; }
.login-benefits > span { display: inline-flex; align-items: center; gap: 7px; padding: 8px 12px; border: 1px solid var(--login-rule); border-radius: 99px; background: theme('colors.canvas / 75%'); font-size: 11px; white-space: nowrap; }
.dark .login-benefits > span { background: theme('colors.dark.950 / 75%'); }
.login-benefits svg { color: var(--login-accent); }
.login-dial-position { position: absolute; width: min(70vh, 40vw, 700px); right: calc(min(70vh, 40vw, 700px) * -.34); top: 49%; transform: translateY(-50%); z-index: 1; }
.login-marquee { position: relative; z-index: 3; flex: none; margin: 0 calc(clamp(36px, 4.2vw, 80px) * -1) 58px; padding: 15px 0; overflow: hidden; color: var(--login-on-accent); background: var(--login-accent); transform: rotate(-2.2deg) scale(1.02); }
.login-marquee-track { display: flex; width: max-content; animation: login-marquee 36s linear infinite; }
.login-marquee-copy { display: flex; flex-shrink: 0; align-items: center; }
.login-marquee-copy > span { display: inline-flex; align-items: center; gap: 34px; padding-right: 34px; font-size: 16px; font-weight: 700; white-space: nowrap; }
.login-marquee-copy i { width: 4px; height: 4px; background: currentColor; }
.login-showcase-footer { display: flex; justify-content: space-between; gap: 20px; flex: none; color: var(--login-muted); font: 10px/1.5 theme('fontFamily.mono'); letter-spacing: .04em; }
.login-showcase-footer span:first-child { overflow-wrap: anywhere; }
.login-showcase-footer span:last-child { flex-shrink: 0; }
.login-panel { position: relative; min-width: 0; display: flex; align-items: center; justify-content: center; padding: 88px clamp(28px, 4.4vw, 84px) 40px; }
.login-theme-toggle { position: absolute; right: 32px; top: 28px; width: 42px; height: 42px; display: grid; place-items: center; border: 1px solid var(--login-rule); border-radius: 14px; color: var(--login-muted); background: var(--login-card); transition: color .2s, border-color .2s; }
.login-theme-toggle:hover { color: var(--login-accent); border-color: var(--login-accent); }
.login-theme-toggle:focus-visible, .login-brand:focus-visible { outline: 2px solid var(--login-accent); outline-offset: 5px; }
.login-panel-content { width: 100%; max-width: 540px; }
.login-card { padding: 28px clamp(24px, 2.2vw, 42px) 36px; border: 1px solid var(--login-rule); border-radius: 30px; background: var(--login-card); box-shadow: 0 24px 70px -40px theme('colors.primary.950 / 35%'); }
.login-card-status { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 30px; padding-bottom: 17px; border-bottom: 1px solid var(--login-rule); color: var(--login-muted); font: 10px/1.5 theme('fontFamily.mono'); letter-spacing: .08em; }
.login-card-status > span:first-child { display: inline-flex; align-items: center; gap: 9px; color: var(--login-accent); }
.login-card-status i { width: 5px; height: 5px; background: currentColor; }
.login-card-status > span:last-child { font-size: 9px; }
.login-panel-footer { margin-top: 28px; text-align: center; font-size: 13px; }
.login-copyright { margin-top: 24px; color: var(--login-muted); font-size: 10px; line-height: 1.7; text-align: center; overflow-wrap: anywhere; }

@keyframes login-marquee { to { transform: translateX(-50%); } }

@media (min-width: 1024px) and (max-width: 1279px) {
  .login-showcase { padding-inline: 32px; }
  .login-clock > span:first-child { display: none; }
  .login-brand { font-size: 22px; max-width: 62%; }
  .login-story { width: 79%; }
  .login-story h1 { font-size: 44px; }
  .login-description { max-width: 300px; font-size: 14px; }
  .login-dial-position { width: 34vw; right: -11vw; }
  .login-marquee { margin-inline: -34px; }
  .login-panel { padding-inline: 25px; }
}

@media (max-width: 1023px) {
  .login-layout { grid-template-columns: minmax(0, 1fr); }
  .login-showcase { position: relative; height: auto; min-height: 0; padding: 28px 32px 0; border: 0; overflow: visible; }
  .login-brand-row { padding-right: 60px; }
  .login-brand { max-width: 100%; font-size: 22px; }
  .login-brand-logo { width: 38px; height: 38px; }
  .login-clock, .login-story, .login-dial-position, .login-marquee, .login-showcase-footer { display: none; }
  .login-panel { position: static; padding: 36px 24px 32px; align-items: start; }
  .login-theme-toggle { top: 26px; right: 28px; }
  .login-card { padding: 28px 30px 32px; }
  .login-panel-content { max-width: 480px; }
}

@media (max-width: 479px) {
  .login-showcase { padding: 23px 22px 0; }
  .login-theme-toggle { top: 22px; right: 22px; }
  .login-panel { padding: 30px 16px 24px; }
  .login-card { padding: 25px 23px 30px; border-radius: 24px; }
  .login-card-status { margin-bottom: 24px; }
}

@media (prefers-reduced-motion: reduce) {
  .login-marquee-track { animation: none; }
  .login-theme-toggle { transition: none; }
}
</style>
