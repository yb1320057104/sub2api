<template>
  <div v-if="siteKey" class="turnstile-wrapper relative">
    <div
      v-if="loading"
      role="status"
      class="absolute inset-0 flex items-center justify-center rounded-lg border border-gray-200 bg-gray-50 text-sm text-gray-500 dark:border-dark-600 dark:bg-dark-700 dark:text-gray-400"
    >
      {{ t('auth.captchaLoading') }}
    </div>
    <div ref="containerRef" class="turnstile-container"></div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'

interface TurnstileRenderOptions {
  sitekey: string
  action?: string
  callback: (token: string) => void
  'expired-callback'?: () => void
  'error-callback'?: () => void
  theme?: 'light' | 'dark' | 'auto'
  size?: 'normal' | 'compact' | 'flexible'
}

interface TurnstileAPI {
  render: (container: HTMLElement, options: TurnstileRenderOptions) => string
  reset: (widgetId?: string) => void
  remove: (widgetId?: string) => void
}

declare global {
  interface Window {
    turnstile?: TurnstileAPI
    onTurnstileLoad?: () => void
  }
}

// All widgets on an auth page share one SDK load. Keeping the promise at
// module scope prevents concurrent components from overwriting the global
// onTurnstileLoad callback and leaving one widget permanently in loading state.
let sharedScriptPromise: Promise<void> | null = null
let sharedScriptLoaded = false

function loadTurnstileScript(): Promise<void> {
  if (window.turnstile) return Promise.resolve()
  if (sharedScriptPromise && sharedScriptLoaded) {
    // Test harnesses and hot reloads may remove the global SDK after the
    // original script completed. Allow a fresh load in that case.
    sharedScriptPromise = null
    sharedScriptLoaded = false
  }
  if (sharedScriptPromise) return sharedScriptPromise

  sharedScriptPromise = new Promise<void>((resolve, reject) => {
    let settled = false
    const finish = () => {
      if (settled) return
      settled = true
      if (!window.turnstile) {
        reject(new Error('Turnstile SDK initialized without a global API'))
        return
      }
      sharedScriptLoaded = true
      resolve()
    }
    const fail = () => {
      if (settled) return
      settled = true
      reject(new Error('Failed to load Turnstile script'))
    }

    const existingScript = document.querySelector<HTMLScriptElement>('script[src*="turnstile"]')
    if (existingScript) {
      existingScript.addEventListener('load', finish, { once: true })
      existingScript.addEventListener('error', fail, { once: true })
      return
    }

    const script = document.createElement('script')
    script.src = 'https://challenges.cloudflare.com/turnstile/v0/api.js?onload=onTurnstileLoad'
    script.async = true
    script.defer = true
    // Cloudflare invokes this callback after installing window.turnstile.
    window.onTurnstileLoad = finish
    script.addEventListener('load', finish, { once: true })
    script.addEventListener('error', fail, { once: true })
    document.head.appendChild(script)
  })

  sharedScriptPromise = sharedScriptPromise.catch((error) => {
    sharedScriptPromise = null
    sharedScriptLoaded = false
    throw error
  })
  return sharedScriptPromise
}

const props = withDefaults(
  defineProps<{
    siteKey: string
    action?: string
    theme?: 'light' | 'dark' | 'auto'
    size?: 'normal' | 'compact' | 'flexible'
  }>(),
  {
    theme: 'auto',
    size: 'flexible'
  }
)

const emit = defineEmits<{
  (e: 'verify', token: string): void
  (e: 'expire'): void
  (e: 'error'): void
}>()

const { t } = useI18n()
const loading = ref(true)
const containerRef = ref<HTMLElement | null>(null)
const widgetId = ref<string | null>(null)
const scriptLoaded = ref(false)
let disposed = false

const destroyWidget = () => {
  if (window.turnstile && widgetId.value) {
    try {
      window.turnstile.remove(widgetId.value)
    } catch {
      // Ignore errors when removing
    }
  }
  widgetId.value = null
  if (containerRef.value) containerRef.value.innerHTML = ''
}

const initialize = async () => {
  if (!props.siteKey || disposed) {
    loading.value = false
    return
  }

  loading.value = true
  try {
    await loadTurnstileScript()
    if (disposed || !props.siteKey) return
    scriptLoaded.value = true
    renderWidget()
  } catch (error) {
    console.error('Failed to initialize Turnstile:', error)
    if (!disposed) emit('error')
  } finally {
    loading.value = false
  }
}

const renderWidget = () => {
  if (!window.turnstile || !containerRef.value || !props.siteKey) {
    return
  }

  destroyWidget()

  widgetId.value = window.turnstile.render(containerRef.value, {
    sitekey: props.siteKey,
    ...(props.action ? { action: props.action } : {}),
    callback: (token: string) => {
      emit('verify', token)
    },
    'expired-callback': () => {
      emit('expire')
    },
    'error-callback': () => {
      emit('error')
    },
    theme: props.theme,
    size: props.size
  })
}

const reset = () => {
  if (window.turnstile && widgetId.value) {
    window.turnstile.reset(widgetId.value)
  }
}

// Expose reset method to parent
defineExpose({ reset })

onMounted(initialize)

onUnmounted(() => {
  disposed = true
  destroyWidget()
})

// Load lazily when settings populate the site key after the component mounts.
watch(
  () => props.siteKey,
  (newKey) => {
    if (newKey && !scriptLoaded.value) {
      void initialize()
    } else if (!newKey && scriptLoaded.value) {
      destroyWidget()
      scriptLoaded.value = false
    }
  }
)

// Re-render when any widget-defining prop changes. This is required when an
// auth flow switches between register/login actions without remounting.
watch(
  () => [props.action, props.theme, props.size],
  () => {
    if (!scriptLoaded.value) return
    if (props.siteKey) {
      renderWidget()
    } else {
      destroyWidget()
    }
  }
)
</script>

<style scoped>
.turnstile-wrapper {
  width: 100%;
}

.turnstile-container {
  width: 100%;
  min-height: 65px;
}

/* Make the Turnstile iframe fill the container width */
.turnstile-container :deep(iframe) {
  width: 100% !important;
}
</style>
