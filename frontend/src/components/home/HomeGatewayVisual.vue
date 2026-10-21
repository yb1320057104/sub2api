<template>
  <figure class="home-gateway" :aria-label="t('home.redesign.gatewayLabel', { siteName })">
    <div class="gateway-halo" aria-hidden="true"></div>
    <div class="gateway-orbit gateway-orbit-outer" aria-hidden="true"></div>
    <div class="gateway-orbit gateway-orbit-inner" aria-hidden="true"></div>

    <div class="gateway-flow">
      <div class="gateway-key gateway-node">
        <Icon name="key" size="xl" class="gateway-key-icon" aria-hidden="true" />
        <span>API Key</span>
      </div>

      <div class="gateway-input-line" aria-hidden="true"></div>

      <div class="gateway-center gateway-node">
        <img :src="siteLogo || '/dp-api-logo.png'" alt="" class="gateway-logo" />
        <strong>{{ siteName }}</strong>
      </div>

      <!-- Edges describe the connection between the real, responsive UI nodes. -->
      <svg class="gateway-connections" viewBox="0 0 100 400" preserveAspectRatio="none" fill="none" aria-hidden="true">
        <path d="M0 155 C75 155 22 50 100 50" />
        <path d="M0 200 C68 200 38 150 100 150" />
        <path d="M0 200 C68 200 38 250 100 250" />
        <path d="M0 245 C75 245 22 350 100 350" />
        <circle cx="2" cy="155" r="3" />
        <circle cx="2" cy="200" r="3" />
        <circle cx="2" cy="245" r="3" />
        <circle cx="98" cy="50" r="3" />
        <circle cx="98" cy="150" r="3" />
        <circle cx="98" cy="250" r="3" />
        <circle cx="98" cy="350" r="3" />
      </svg>

      <ul class="gateway-providers">
        <li v-for="provider in providers" :key="provider.platform" class="gateway-provider gateway-node">
          <PlatformIcon :platform="provider.platform" size="lg" :class="['gateway-provider-icon', provider.color]" aria-hidden="true" />
          <span>{{ provider.name }}</span>
        </li>
      </ul>
    </div>
  </figure>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import Icon from '@/components/icons/Icon.vue'

defineProps<{
  siteName: string
  siteLogo: string
}>()

const { t } = useI18n()

const providers = [
  { name: 'Claude', platform: 'anthropic', color: 'text-orange-600 dark:text-orange-400' },
  { name: 'GPT', platform: 'openai', color: 'text-emerald-600 dark:text-emerald-400' },
  { name: 'Gemini', platform: 'gemini', color: 'text-primary-500 dark:text-primary-400' },
  { name: 'Antigravity', platform: 'antigravity', color: 'text-violet-500 dark:text-violet-400' }
] as const
</script>

<style scoped>
.home-gateway {
  position: relative;
  display: grid;
  min-width: 0;
  min-height: 480px;
  align-items: center;
  isolation: isolate;
}

.gateway-halo {
  @apply pointer-events-none absolute -inset-x-4 -inset-y-6 -z-10 rounded-full bg-gradient-radial from-primary-200/80 via-primary-50/70 to-transparent dark:from-primary-800/30 dark:via-primary-950/20;
}

.gateway-orbit {
  @apply pointer-events-none absolute -z-10 rounded-full border border-primary-200/70 dark:border-primary-700/30;
  left: 40%;
  top: 50%;
  aspect-ratio: 1;
  transform: translate(-50%, -50%);
}

.gateway-orbit-outer {
  width: 65%;
}

.gateway-orbit-inner {
  @apply border-white/80 dark:border-primary-700/20;
  width: 46%;
}

.gateway-flow {
  display: grid;
  min-width: 0;
  min-height: 380px;
  grid-template-columns: minmax(0, 21fr) minmax(0, 7fr) minmax(0, 27fr) minmax(0, 18fr) minmax(0, 27fr);
  align-items: center;
}

.gateway-node {
  @apply relative rounded-2xl border border-white/80 bg-white text-gray-900 shadow-sm dark:border-dark-700 dark:bg-dark-900 dark:text-white;
  min-width: 0;
}

.gateway-key {
  display: flex;
  min-height: 3.75rem;
  align-items: center;
  justify-content: center;
  gap: 0.625rem;
  padding: 0.625rem;
  font-size: clamp(0.75rem, 1.35vw, 1.25rem);
  font-weight: 500;
  white-space: nowrap;
}

.gateway-key-icon {
  @apply shrink-0 text-primary-500 dark:text-primary-400;
  width: 2rem;
  height: 2rem;
}

.gateway-input-line {
  @apply relative h-0.5 bg-primary-500 dark:bg-primary-400;
}

.gateway-input-line::after {
  @apply absolute -right-1 top-1/2 h-2 w-2 -translate-y-1/2 rounded-full bg-primary-500 dark:bg-primary-400;
  content: '';
}

.gateway-center {
  @apply shadow-xl shadow-primary-300/20 dark:shadow-primary-950/40;
  display: flex;
  min-height: 10rem;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 0.5rem;
  border-radius: 1.5rem;
  padding: 1.25rem 0.875rem;
  text-align: center;
}

.gateway-logo {
  width: clamp(3rem, 5.2vw, 5rem);
  height: clamp(3rem, 5.2vw, 5rem);
  object-fit: contain;
}

.gateway-center strong {
  max-width: 100%;
  font-size: clamp(1.125rem, 1.9vw, 1.75rem);
  font-weight: 700;
  letter-spacing: -0.03em;
  line-height: 1.25;
  overflow-wrap: anywhere;
}

.gateway-connections {
  @apply w-full self-stretch text-primary-500 dark:text-primary-400;
  height: 100%;
  overflow: visible;
}

.gateway-connections path {
  stroke: currentColor;
  stroke-width: 1.7px;
  vector-effect: non-scaling-stroke;
}

.gateway-connections circle {
  fill: currentColor;
}

.gateway-providers {
  display: grid;
  min-width: 0;
  min-height: 380px;
  grid-template-rows: repeat(4, minmax(0, 1fr));
  align-items: center;
  align-self: stretch;
}

.gateway-provider {
  display: flex;
  min-height: 4.375rem;
  align-items: center;
  gap: clamp(0.625rem, 1vw, 1rem);
  padding: 0.75rem clamp(0.625rem, 1vw, 1rem);
  font-size: clamp(0.75rem, 1.2vw, 1.125rem);
  font-weight: 500;
  white-space: nowrap;
}

.gateway-provider-icon {
  width: clamp(1.5rem, 2.7vw, 2.5rem);
  height: clamp(1.5rem, 2.7vw, 2.5rem);
  flex-shrink: 0;
}

@media (min-width: 601px) and (max-width: 1280px) {
  .gateway-key,
  .gateway-provider {
    font-size: 0.875rem;
  }

  .gateway-provider {
    gap: 0.625rem;
    padding-inline: 0.75rem;
  }

  .gateway-provider-icon {
    width: 1.75rem;
    height: 1.75rem;
  }

  .gateway-key-icon {
    width: 1.5rem;
    height: 1.5rem;
  }
}

@media (max-width: 600px) {
  .home-gateway {
    min-height: auto;
    padding-block: 1.5rem 0;
  }

  .gateway-halo {
    inset: 0;
  }

  .gateway-orbit {
    left: 50%;
    top: 42%;
  }

  .gateway-orbit-outer {
    width: 90%;
  }

  .gateway-orbit-inner {
    width: 64%;
  }

  .gateway-flow {
    min-height: auto;
    grid-template-columns: minmax(0, 1fr);
    justify-items: center;
  }

  .gateway-key {
    min-height: 3rem;
    gap: 0.625rem;
    padding-inline: 1.125rem;
    font-size: 0.9375rem;
  }

  .gateway-key-icon {
    width: 1.5rem;
    height: 1.5rem;
  }

  .gateway-input-line {
    width: 2px;
    height: 1.5rem;
  }

  .gateway-input-line::after {
    top: auto;
    right: 50%;
    bottom: -0.25rem;
    transform: translateX(50%);
  }

  .gateway-center {
    width: 11rem;
    max-width: 100%;
    min-height: 8rem;
    padding: 1rem;
  }

  .gateway-logo {
    width: 3.5rem;
    height: 3.5rem;
  }

  .gateway-center strong {
    font-size: 1.25rem;
  }

  .gateway-connections {
    display: none;
  }

  .gateway-providers {
    position: relative;
    width: 100%;
    min-height: auto;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    grid-template-rows: none;
    gap: 0.75rem;
    margin-top: 2rem;
  }

  .gateway-providers::before {
    @apply absolute left-1/2 w-0.5 bg-primary-500 dark:bg-primary-400;
    top: -2rem;
    height: 1.125rem;
    content: '';
  }

  .gateway-providers::after {
    @apply pointer-events-none absolute border-x-2 border-t-2 border-primary-500 dark:border-primary-400;
    top: -0.875rem;
    left: 25%;
    right: 25%;
    height: 0.875rem;
    content: '';
  }

  .gateway-provider {
    min-height: 3.5rem;
    height: 100%;
    gap: 0.5rem;
    padding: 0.625rem;
    font-size: 0.875rem;
  }

  .gateway-provider-icon {
    width: 1.5rem;
    height: 1.5rem;
  }

  .gateway-provider:nth-child(n + 3)::before {
    @apply absolute left-1/2 w-0.5 bg-primary-500 dark:bg-primary-400;
    top: -0.8125rem;
    height: 0.75rem;
    content: '';
  }
}
</style>
