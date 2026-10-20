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
import { CAP_I18N_ZH, isCapSiteKey, parseCapSiteKey, readPageCspNonce } from '@/utils/capCaptcha'

interface TurnstileRenderOptions {
  sitekey: string
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
    CAP_CUSTOM_WASM_URL?: string
    CAP_CUSTOM_HASHWX_URL?: string
    CAP_SCRIPT_NONCE?: string
    CAP_CSS_NONCE?: string
  }
}

const props = withDefaults(
  defineProps<{
    siteKey: string
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

const { t, locale } = useI18n()
const loading = ref(true)
const containerRef = ref<HTMLElement | null>(null)
const widgetId = ref<string | null>(null)
const scriptLoaded = ref(false)

const loadScript = (): Promise<void> => {
  return new Promise((resolve, reject) => {
    if (window.turnstile) {
      scriptLoaded.value = true
      resolve()
      return
    }

    // Check if script is already loading
    const existingScript = document.querySelector('script[src*="turnstile"]')
    if (existingScript) {
      window.onTurnstileLoad = () => {
        scriptLoaded.value = true
        resolve()
      }
      return
    }

    const script = document.createElement('script')
    script.src = 'https://challenges.cloudflare.com/turnstile/v0/api.js?onload=onTurnstileLoad'
    script.async = true
    script.defer = true

    window.onTurnstileLoad = () => {
      scriptLoaded.value = true
      resolve()
    }

    script.onerror = () => {
      reject(new Error('Failed to load Turnstile script'))
    }

    document.head.appendChild(script)
  })
}

const renderWidget = () => {
  if (!window.turnstile || !containerRef.value || !props.siteKey) {
    return
  }

  // Remove existing widget if any
  if (widgetId.value) {
    try {
      window.turnstile.remove(widgetId.value)
    } catch {
      // Ignore errors when removing
    }
    widgetId.value = null
  }

  // Clear container
  containerRef.value.innerHTML = ''

  widgetId.value = window.turnstile.render(containerRef.value, {
    sitekey: props.siteKey,
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

// ---------------------------------------------------------------------------
// 自建 Cap 验证码：站点密钥为 cap:<key> 或 cap:<base>|<key> 时启用，
// 通过同一组 verify / expire / error 事件向父组件交付 token。
// ---------------------------------------------------------------------------
const capElement = ref<HTMLElement | null>(null)
const capScriptPromises = new Map<string, Promise<void>>()

const loadCapScript = (src: string): Promise<void> => {
  if (typeof customElements !== 'undefined' && customElements.get('cap-widget')) {
    return Promise.resolve()
  }
  const cached = capScriptPromises.get(src)
  if (cached) return cached
  const promise = new Promise<void>((resolve, reject) => {
    const done = () => {
      if (typeof customElements !== 'undefined' && !customElements.get('cap-widget')) {
        customElements.whenDefined('cap-widget').then(() => resolve(), reject)
        return
      }
      resolve()
    }
    const existing = document.querySelector<HTMLScriptElement>(`script[data-cap-widget-src="${src}"]`)
    if (existing) {
      existing.addEventListener('load', done, { once: true })
      existing.addEventListener('error', () => reject(new Error('Failed to load Cap widget script')), { once: true })
      return
    }
    const script = document.createElement('script')
    script.src = src
    script.async = true
    script.dataset.capWidgetSrc = src
    script.onload = done
    script.onerror = () => {
      capScriptPromises.delete(src)
      reject(new Error('Failed to load Cap widget script'))
    }
    document.head.appendChild(script)
  })
  capScriptPromises.set(src, promise)
  return promise
}

const removeCapWidget = () => {
  if (capElement.value) {
    capElement.value.remove()
    capElement.value = null
  }
}

const renderCapWidget = () => {
  const cfg = parseCapSiteKey(props.siteKey)
  if (!cfg || !containerRef.value) return
  removeCapWidget()
  containerRef.value.innerHTML = ''
  const el = document.createElement('cap-widget')
  el.setAttribute('data-cap-api-endpoint', cfg.apiEndpoint)
  if (String(locale?.value ?? '').toLowerCase().startsWith('zh')) {
    for (const [attr, label] of Object.entries(CAP_I18N_ZH)) {
      el.setAttribute(attr, label)
    }
  }
  el.addEventListener('solve', (event: Event) => {
    const token = (event as CustomEvent<{ token?: string }>).detail?.token
    if (token) emit('verify', token)
  })
  el.addEventListener('error', () => emit('error'))
  el.addEventListener('reset', () => emit('expire'))
  containerRef.value.appendChild(el)
  capElement.value = el
}

const initCap = async () => {
  const cfg = parseCapSiteKey(props.siteKey)
  if (!cfg) {
    console.error('Invalid Cap site key, expected cap:<key> or cap:<base>|<key>')
    emit('error')
    return
  }
  // 求解器 WASM 也从自建 Cap 资源服务加载（避免依赖 jsdelivr），运维可预先覆盖。
  if (!window.CAP_CUSTOM_WASM_URL) window.CAP_CUSTOM_WASM_URL = cfg.wasmUrl
  if (!window.CAP_CUSTOM_HASHWX_URL) window.CAP_CUSTOM_HASHWX_URL = cfg.hashwxUrl
  const nonce = readPageCspNonce()
  if (nonce) {
    if (!window.CAP_SCRIPT_NONCE) window.CAP_SCRIPT_NONCE = nonce
    if (!window.CAP_CSS_NONCE) window.CAP_CSS_NONCE = nonce
  }
  await loadCapScript(cfg.scriptSrc)
  scriptLoaded.value = true
  renderCapWidget()
}

const reset = () => {
  if (isCapSiteKey(props.siteKey)) {
    // Cap token 只能使用一次：重建组件即重新求解
    if (capElement.value) renderCapWidget()
    return
  }
  if (window.turnstile && widgetId.value) {
    window.turnstile.reset(widgetId.value)
  }
}

// Expose reset method to parent
defineExpose({ reset })

onMounted(async () => {
  if (!props.siteKey) {
    return
  }

  try {
    if (isCapSiteKey(props.siteKey)) {
      await initCap()
      return
    }
    await loadScript()
    renderWidget()
  } catch (error) {
    console.error('Failed to initialize Turnstile:', error)
    emit('error')
  } finally {
    loading.value = false
  }
})

onUnmounted(() => {
  removeCapWidget()
  if (window.turnstile && widgetId.value) {
    try {
      window.turnstile.remove(widgetId.value)
    } catch {
      // Ignore errors when removing
    }
  }
})

// Re-render when siteKey changes
watch(
  () => props.siteKey,
  (newKey) => {
    if (!newKey) return
    if (isCapSiteKey(newKey)) {
      if (widgetId.value && window.turnstile) {
        try {
          window.turnstile.remove(widgetId.value)
        } catch {
          // Ignore errors when removing
        }
        widgetId.value = null
      }
      void initCap().catch((error) => {
        console.error('Failed to initialize Cap:', error)
        emit('error')
      })
      return
    }
    removeCapWidget()
    if (scriptLoaded.value) {
      renderWidget()
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
