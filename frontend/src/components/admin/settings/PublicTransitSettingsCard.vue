<template>
  <div class="card" data-testid="public-transit-settings">
    <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <h2 id="settings-section-features-public-transit" tabindex="-1" class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('upstreamOps.transit.title') }}</h2>
      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('upstreamOps.transit.description') }}</p>
    </div>
    <form v-if="draft" class="space-y-4 p-6" @submit.prevent="save">
      <label class="flex items-start gap-3">
        <input v-model="draft.enabled" data-testid="transit-enabled" type="checkbox" role="switch" class="mt-1" @change="onEnabledChange" />
        <span><span class="font-medium text-gray-900 dark:text-white">{{ t('upstreamOps.transit.enabled') }}</span><small class="block text-gray-500">{{ t('upstreamOps.transit.enabledHint') }}</small></span>
      </label>
      <label class="flex items-center gap-3">
        <input v-model="draft.page_enabled" data-testid="transit-page-enabled" type="checkbox" role="switch" :disabled="!draft.enabled" />
        <span class="font-medium text-gray-900 dark:text-white">{{ t('upstreamOps.transit.pageEnabled') }}</span>
      </label>
      <div class="grid gap-4 sm:grid-cols-2">
        <label><span class="input-label">{{ t('upstreamOps.transit.siteName') }}</span><input v-model="draft.site_name" maxlength="80" class="input w-full" /></label>
        <label><span class="input-label">{{ t('upstreamOps.transit.availabilityRange') }}</span>
          <select v-model="draft.availability_range" class="input w-full"><option value="24h">24h</option><option value="7d">7d</option><option value="30d">30d</option></select>
        </label>
      </div>
      <p class="text-xs leading-5 text-gray-500">{{ t('upstreamOps.transit.availabilityHint') }}</p>
      <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
      <p v-if="notice" role="status" class="text-sm text-emerald-600">{{ notice }}</p>
      <div class="flex flex-wrap items-center gap-3">
        <button type="submit" class="btn btn-primary" :disabled="saving" data-testid="transit-save">{{ t('upstreamOps.transit.save') }}</button>
        <a v-if="draft.enabled && draft.page_enabled" href="/public/transit" target="_blank" rel="noopener" class="text-sm text-primary-600 hover:underline">{{ t('upstreamOps.transit.open') }}</a>
      </div>
    </form>
    <p v-else-if="error" role="alert" class="p-6 text-sm text-red-600">{{ error }}</p>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getPublicTransitSettings, savePublicTransitSettings, type PublicTransitSettings } from '@/api/admin/upstreamOps'

const { t } = useI18n()
const draft = ref<PublicTransitSettings | null>(null)
const saving = ref(false)
const error = ref('')
const notice = ref('')

function onEnabledChange() {
  if (draft.value && !draft.value.enabled) draft.value.page_enabled = false
}

async function save() {
  if (!draft.value) return
  saving.value = true
  error.value = ''
  notice.value = ''
  try {
    draft.value = await savePublicTransitSettings(draft.value)
    notice.value = t('upstreamOps.transit.saved')
  } catch (err: unknown) {
    error.value = (err as { message?: string })?.message || t('upstreamOps.transit.loadError')
  } finally {
    saving.value = false
  }
}

onMounted(async () => {
  try {
    draft.value = await getPublicTransitSettings()
  } catch {
    error.value = t('upstreamOps.transit.loadError')
  }
})
</script>
