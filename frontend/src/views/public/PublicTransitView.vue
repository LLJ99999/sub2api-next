<template>
  <div class="min-h-screen bg-gray-50 px-4 py-8 text-gray-900 dark:bg-dark-950 dark:text-gray-100">
    <div class="mx-auto max-w-6xl space-y-6">
      <header>
        <h1 class="text-2xl font-semibold">{{ snapshot?.site_name || t('upstreamOps.publicPage.title') }}</h1>
        <p v-if="snapshot" class="mt-1 text-sm text-gray-500">{{ t('upstreamOps.publicPage.generatedAt') }}: {{ new Date(snapshot.generated_at).toLocaleString() }}</p>
      </header>
      <p v-if="state === 'loading'" class="text-sm text-gray-500">{{ t('upstreamOps.publicPage.loading') }}</p>
      <p v-else-if="state === 'disabled'" class="rounded-xl bg-white p-6 text-sm text-gray-500 shadow-sm dark:bg-dark-900" data-testid="transit-disabled">{{ t('upstreamOps.publicPage.disabled') }}</p>
      <p v-else-if="state === 'error'" role="alert" class="text-sm text-red-600">{{ t('upstreamOps.publicPage.error') }}</p>
      <template v-else-if="snapshot">
        <section class="grid gap-4 sm:grid-cols-3">
          <div class="rounded-xl bg-white p-4 shadow-sm dark:bg-dark-900"><p class="text-xs text-gray-500">{{ t('upstreamOps.publicPage.recharge') }}</p><p class="mt-1 text-xl font-semibold tabular-nums">{{ snapshot.recharge_multiplier }}</p></div>
          <div class="rounded-xl bg-white p-4 shadow-sm dark:bg-dark-900"><p class="text-xs text-gray-500">{{ t('upstreamOps.publicPage.cacheHitRate') }}<span v-if="snapshot.cache"> · {{ snapshot.cache.range }}</span></p><p class="mt-1 text-xl font-semibold tabular-nums">{{ percent(snapshot.cache?.hit_rate) }}</p></div>
          <div class="rounded-xl bg-white p-4 shadow-sm dark:bg-dark-900"><p class="text-xs text-gray-500">{{ t('upstreamOps.publicPage.availability') }}<span v-if="snapshot.availability"> · {{ snapshot.availability.range }}</span></p><p class="mt-1 text-xl font-semibold tabular-nums">{{ percent(snapshot.availability?.uptime) }}</p><p v-if="snapshot.availability" class="text-xs text-gray-500">{{ snapshot.availability.status }}</p></div>
        </section>

        <section class="rounded-xl bg-white p-4 shadow-sm dark:bg-dark-900">
          <h2 class="mb-3 text-lg font-semibold">{{ t('upstreamOps.publicPage.groups') }}</h2>
          <div class="overflow-x-auto">
            <table class="w-full whitespace-nowrap text-left text-sm" data-testid="transit-groups">
              <thead><tr><th v-for="key in groupColumns" :key="key" class="px-3 py-2 text-xs font-medium text-gray-500">{{ t(`upstreamOps.publicPage.columns.${key}`) }}</th></tr></thead>
              <tbody>
                <tr v-for="g in snapshot.groups" :key="g.name" class="border-t border-gray-100 dark:border-dark-700">
                  <td class="px-3 py-2 font-medium">{{ g.name }}</td>
                  <td class="px-3 py-2">{{ g.platform }}</td>
                  <td class="px-3 py-2 tabular-nums">×{{ g.rate_multiplier }}<small v-if="g.peak_rate_enabled" class="block text-gray-400">{{ t('upstreamOps.publicPage.peak', { start: g.peak_start, end: g.peak_end, multiplier: g.peak_rate_multiplier }) }}</small></td>
                  <td class="px-3 py-2 tabular-nums">{{ g.model_count }}</td>
                  <td class="px-3 py-2">{{ g.availability?.status ?? noData }}</td>
                  <td class="px-3 py-2 tabular-nums">{{ percent(g.availability?.uptime) }}</td>
                  <td class="px-3 py-2 tabular-nums">{{ msText(g.availability?.latency_p50_ms) }}</td>
                  <td class="px-3 py-2 tabular-nums">{{ msText(g.availability?.ttft_p50_ms) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>

        <section class="rounded-xl bg-white p-4 shadow-sm dark:bg-dark-900">
          <h2 class="text-lg font-semibold">{{ t('upstreamOps.publicPage.models') }}</h2>
          <p class="mb-3 text-xs text-gray-500">{{ t('upstreamOps.publicPage.priceUnit') }}</p>
          <div class="overflow-x-auto">
            <table class="w-full whitespace-nowrap text-left text-sm" data-testid="transit-models">
              <thead><tr><th v-for="key in modelColumns" :key="key" class="px-3 py-2 text-xs font-medium text-gray-500">{{ t(`upstreamOps.publicPage.columns.${key}`) }}</th></tr></thead>
              <tbody>
                <tr v-for="m in snapshot.models" :key="`${m.group}/${m.name}`" class="border-t border-gray-100 dark:border-dark-700">
                  <td class="px-3 py-2 font-medium">{{ m.name }}</td>
                  <td class="px-3 py-2">{{ m.group }}</td>
                  <td class="px-3 py-2">{{ m.billing_mode }}</td>
                  <td class="px-3 py-2 tabular-nums">{{ price(m.input_price) }}</td>
                  <td class="px-3 py-2 tabular-nums">{{ price(m.output_price) }}</td>
                  <td class="px-3 py-2 tabular-nums">{{ price(m.cache_read_price) }}</td>
                  <td class="px-3 py-2 tabular-nums">{{ price(m.cache_write_price) }}</td>
                  <td class="px-3 py-2 tabular-nums">{{ price(m.per_request_price) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchPublicTransitDiscovery, fetchPublicTransitSnapshot, type PublicTransitSnapshot } from '@/api/publicTransit'

const { t } = useI18n()
const state = ref<'loading' | 'ready' | 'disabled' | 'error'>('loading')
const snapshot = ref<PublicTransitSnapshot | null>(null)
const noData = '—'
const groupColumns = ['name', 'platform', 'multiplier', 'models', 'status', 'uptime', 'latency', 'ttft'] as const
const modelColumns = ['name', 'group', 'billing', 'input', 'output', 'cacheRead', 'cacheWrite', 'perRequest'] as const

function percent(value: number | null | undefined) {
  return value == null ? noData : `${(value * 100).toFixed(2)}%`
}
function msText(value: number | null | undefined) {
  return value == null ? noData : `${value} ms`
}
function price(value: number | null | undefined) {
  return value == null ? noData : `$${Number(value.toFixed(6))}`
}

onMounted(async () => {
  try {
    // The page has its own switch; the discovery document lists the page only when it is on.
    const discovery = await fetchPublicTransitDiscovery()
    if (!discovery || !discovery.page_url) {
      state.value = 'disabled'
      return
    }
    snapshot.value = await fetchPublicTransitSnapshot()
    state.value = snapshot.value ? 'ready' : 'disabled'
  } catch {
    state.value = 'error'
  }
})
</script>
