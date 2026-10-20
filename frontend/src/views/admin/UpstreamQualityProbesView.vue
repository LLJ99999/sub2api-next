<template>
  <AppLayout>
    <div class="min-w-0 space-y-5">
      <header>
        <h1 class="text-2xl font-semibold">{{ t('upstreamOps.quality.title') }}</h1>
        <p class="mt-2 text-sm text-gray-500">{{ t('upstreamOps.quality.description') }}</p>
      </header>
      <SmartOpsNav />
      <p class="rounded-xl bg-amber-50 p-4 text-sm leading-6 text-amber-800 dark:bg-amber-950/30 dark:text-amber-200" data-testid="quality-cost-warning">{{ t('upstreamOps.quality.costWarning') }}</p>
      <p v-if="error" role="alert" class="text-red-600">{{ error }}</p>
      <p v-if="notice" role="status" class="text-emerald-600">{{ notice }}</p>

      <div class="grid gap-5 lg:grid-cols-2">
        <form v-if="settings" class="card space-y-4 p-5" data-testid="quality-settings" @submit.prevent="saveSettings">
          <h2 class="text-lg font-semibold">{{ t('upstreamOps.quality.settingsTitle') }}</h2>
          <label class="flex items-center gap-3 font-medium"><input v-model="settings.enabled" data-testid="quality-enabled" type="checkbox" role="switch" />{{ t('upstreamOps.quality.enabled') }}</label>
          <div class="grid gap-4 sm:grid-cols-2">
            <label><span class="field-title">{{ t('upstreamOps.quality.maxResults') }}</span><input v-model.number="settings.max_results_per_account" type="number" min="10" max="1000" required class="input w-full" /></label>
            <label><span class="field-title">{{ t('upstreamOps.quality.tolerance') }}</span><input v-model.number="settings.input_token_tolerance_pct" type="number" min="50" max="10000" required class="input w-full" /></label>
          </div>
          <p class="text-xs leading-5 text-gray-500">{{ t('upstreamOps.quality.toleranceHint') }}</p>
          <button class="btn btn-primary" type="submit" :disabled="busy">{{ t('upstreamOps.quality.save') }}</button>
        </form>

        <form class="card space-y-4 p-5" data-testid="quality-account-form" @submit.prevent="saveAccount">
          <h2 class="text-lg font-semibold">{{ t('upstreamOps.quality.addTitle') }}</h2>
          <div class="grid gap-4 sm:grid-cols-3">
            <label><span class="field-title">{{ t('upstreamOps.quality.accountId') }}</span><input v-model.number="form.account_id" data-testid="quality-account-id" type="number" min="1" required class="input w-full" /></label>
            <label class="sm:col-span-2"><span class="field-title">{{ t('upstreamOps.quality.model') }}</span><input v-model.trim="form.model" data-testid="quality-model" required maxlength="128" class="input w-full" /></label>
            <label><span class="field-title">{{ t('upstreamOps.quality.interval') }}</span><input v-model.number="form.interval_minutes" type="number" min="5" max="1440" required class="input w-full" /></label>
            <label class="flex items-end gap-3 pb-2 font-medium sm:col-span-2"><input v-model="form.enabled" type="checkbox" role="switch" />{{ t('upstreamOps.quality.accountEnabled') }}</label>
          </div>
          <p class="text-xs leading-5 text-gray-500">{{ t('upstreamOps.quality.modelHint') }}</p>
          <button class="btn btn-primary" type="submit" :disabled="busy">{{ t('upstreamOps.quality.saveAccount') }}</button>
        </form>
      </div>

      <section class="card p-5">
        <h2 class="text-lg font-semibold">{{ t('upstreamOps.quality.listTitle') }}</h2>
        <p v-if="!configs.length" class="py-8 text-center text-sm text-gray-500" data-testid="quality-empty">{{ t('upstreamOps.quality.empty') }}</p>
        <div v-else class="mt-4 overflow-x-auto">
          <table class="w-full whitespace-nowrap text-left text-sm" data-testid="quality-table">
            <thead><tr><th v-for="key in listColumns" :key="key" class="px-3 py-3 text-xs font-medium text-gray-500">{{ t(`upstreamOps.quality.columns.${key}`) }}</th></tr></thead>
            <tbody>
              <tr v-for="cfg in configs" :key="cfg.account_id" class="border-t border-gray-100 align-top dark:border-dark-700">
                <td class="px-3 py-3"><strong class="font-medium">{{ cfg.account_name || '—' }}</strong><small class="block text-gray-400">#{{ cfg.account_id }} · {{ cfg.platform }}</small><small v-if="!cfg.enabled" class="block text-amber-600">{{ t('upstreamOps.quality.disabledBadge') }}</small></td>
                <td class="px-3 py-3">{{ cfg.model }}</td>
                <td class="px-3 py-3 tabular-nums">{{ cfg.interval_minutes }}m<small v-if="cfg.next_run_at && cfg.enabled" class="block text-gray-400">{{ t('upstreamOps.quality.nextRun') }} {{ formatTime(cfg.next_run_at) }}</small></td>
                <td class="px-3 py-3"><ResultStatus :result="cfg.latest" /></td>
                <td class="px-3 py-3 tabular-nums">{{ ms(cfg.latest?.ttft_ms) }}</td>
                <td class="px-3 py-3 tabular-nums">{{ tps(cfg.latest?.output_tps) }}</td>
                <td class="px-3 py-3 tabular-nums">{{ tokens(cfg.latest) }}</td>
                <td class="px-3 py-3"><AnomalyList :items="cfg.latest?.anomalies" /></td>
                <td class="px-3 py-3"><div class="flex flex-wrap gap-2">
                  <button class="btn btn-secondary btn-sm" :disabled="runningId === cfg.account_id" @click="run(cfg.account_id)">{{ t(runningId === cfg.account_id ? 'upstreamOps.quality.running' : 'upstreamOps.quality.runNow') }}</button>
                  <button class="btn btn-secondary btn-sm" @click="openHistory(cfg.account_id)">{{ t('upstreamOps.quality.history') }}</button>
                  <button class="btn btn-secondary btn-sm" @click="edit(cfg)">{{ t('upstreamOps.quality.edit') }}</button>
                  <button class="btn btn-secondary btn-sm text-red-600" @click="remove(cfg.account_id)">{{ t('upstreamOps.quality.remove') }}</button>
                </div></td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <section v-if="historyId" class="card p-5" data-testid="quality-history">
        <header class="flex items-center justify-between"><h2 class="text-lg font-semibold">{{ t('upstreamOps.quality.historyTitle', { id: historyId }) }}</h2><button class="btn btn-secondary btn-sm" @click="historyId = null">{{ t('upstreamOps.quality.close') }}</button></header>
        <div class="mt-4 max-h-[32rem] overflow-auto">
          <table class="w-full text-left text-sm">
            <thead><tr><th v-for="key in historyColumns" :key="key" class="px-3 py-2 text-xs font-medium text-gray-500">{{ t(`upstreamOps.quality.columns.${key}`) }}</th></tr></thead>
            <tbody>
              <tr v-for="r in history" :key="r.id" class="border-t border-gray-100 align-top dark:border-dark-700">
                <td class="whitespace-nowrap px-3 py-2 tabular-nums">{{ formatTime(r.created_at) }}<small class="block text-gray-400">{{ t(`upstreamOps.quality.triggers.${r.trigger}`) }}</small></td>
                <td class="px-3 py-2">{{ r.model }}<small v-if="r.upstream_model" class="block text-gray-400">→ {{ r.upstream_model }}</small></td>
                <td class="px-3 py-2"><ResultStatus :result="r" /></td>
                <td class="whitespace-nowrap px-3 py-2 tabular-nums">{{ ms(r.ttft_ms) }}<small class="block text-gray-400">{{ ms(r.total_ms) }}</small></td>
                <td class="whitespace-nowrap px-3 py-2 tabular-nums">{{ tps(r.output_tps) }}</td>
                <td class="whitespace-nowrap px-3 py-2 tabular-nums">{{ tokens(r) }}<small class="block text-gray-400">{{ t('upstreamOps.quality.estimated') }} {{ r.estimated_input_tokens }}</small></td>
                <td class="px-3 py-2"><AnomalyList :items="r.anomalies" /><small v-if="r.response_excerpt" class="mt-1 block max-w-md break-words text-gray-400">{{ t('upstreamOps.quality.excerpt') }}: {{ r.response_excerpt }}</small></td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { defineComponent, h, onMounted, reactive, ref, type PropType } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import SmartOpsNav from '@/components/admin/operations/SmartOpsNav.vue'
import {
  getUpstreamQualitySettings, saveUpstreamQualitySettings, listUpstreamQualityConfigs, getUpstreamQualityAccount,
  saveUpstreamQualityAccount, deleteUpstreamQualityAccount, runUpstreamQualityAccount,
  type UpstreamQualityProbeSettings, type UpstreamQualityProbeConfig, type UpstreamQualityProbeResult,
} from '@/api/admin/upstreamOps'

const { t } = useI18n()
const listColumns = ['account', 'model', 'interval', 'status', 'ttft', 'tps', 'tokens', 'anomalies', 'actions'] as const
const historyColumns = ['time', 'model', 'status', 'ttft', 'tps', 'tokens', 'anomalies'] as const

const settings = ref<UpstreamQualityProbeSettings | null>(null)
const configs = ref<UpstreamQualityProbeConfig[]>([])
const history = ref<UpstreamQualityProbeResult[]>([])
const historyId = ref<number | null>(null)
const runningId = ref<number | null>(null)
const busy = ref(false)
const error = ref('')
const notice = ref('')
const form = reactive({ account_id: undefined as number | undefined, model: '', interval_minutes: 60, enabled: true })

function errorText(err: unknown) {
  return (err as { message?: string })?.message || t('upstreamOps.quality.loadError')
}
function formatTime(value: string) {
  return new Date(value).toLocaleString()
}
function ms(value: number | null | undefined) {
  return value == null ? '—' : `${value} ms`
}
function tps(value: number | null | undefined) {
  return value == null ? '—' : `${value.toFixed(1)} tok/s`
}
function tokens(r?: UpstreamQualityProbeResult | null) {
  if (!r) return '—'
  const v = (n: number | null) => (n == null ? '—' : String(n))
  const cache = r.cache_read_tokens != null || r.cache_write_tokens != null ? ` / ${v(r.cache_read_tokens)}+${v(r.cache_write_tokens)}` : ''
  return `${v(r.input_tokens)} / ${v(r.output_tokens)}${cache}`
}

const ResultStatus = defineComponent({
  props: { result: { type: Object as PropType<UpstreamQualityProbeResult | null | undefined>, default: null } },
  setup(props) {
    return () => {
      const r = props.result
      if (!r) return h('span', { class: 'text-gray-400' }, '—')
      const ok = r.status === 'success'
      return h('span', [
        h('span', { class: ok ? 'text-emerald-600' : 'text-red-600' }, t(`upstreamOps.quality.statuses.${r.status}`)),
        r.error_message ? h('small', { class: 'block max-w-xs break-words text-gray-400' }, r.error_message) : null,
        h('small', { class: 'block text-gray-400' }, formatTime(r.created_at)),
      ])
    }
  },
})

const AnomalyList = defineComponent({
  props: { items: { type: Array as PropType<string[] | undefined>, default: () => [] } },
  setup(props) {
    return () => {
      const items = props.items ?? []
      if (!items.length) return h('span', { class: 'text-gray-400' }, '—')
      return h('ul', { class: 'space-y-0.5' }, items.map((code) => h('li', { class: 'text-amber-600', key: code }, t(`upstreamOps.quality.anomalies.${code}`))))
    }
  },
})

async function loadAll() {
  try {
    const [s, list] = await Promise.all([getUpstreamQualitySettings(), listUpstreamQualityConfigs()])
    settings.value = s
    configs.value = list
  } catch (err) {
    error.value = errorText(err)
  }
}

async function saveSettings() {
  if (!settings.value) return
  busy.value = true
  error.value = ''
  try {
    settings.value = await saveUpstreamQualitySettings(settings.value)
    notice.value = t('upstreamOps.quality.saved')
  } catch (err) {
    error.value = errorText(err)
  } finally {
    busy.value = false
  }
}

async function saveAccount() {
  if (!form.account_id) return
  busy.value = true
  error.value = ''
  try {
    await saveUpstreamQualityAccount(form.account_id, { enabled: form.enabled, model: form.model, interval_minutes: form.interval_minutes })
    notice.value = t('upstreamOps.quality.saved')
    configs.value = await listUpstreamQualityConfigs()
  } catch (err) {
    error.value = errorText(err)
  } finally {
    busy.value = false
  }
}

function edit(cfg: UpstreamQualityProbeConfig) {
  Object.assign(form, { account_id: cfg.account_id, model: cfg.model, interval_minutes: cfg.interval_minutes, enabled: cfg.enabled })
}

async function remove(accountId: number) {
  if (!window.confirm(t('upstreamOps.quality.confirmRemove'))) return
  try {
    await deleteUpstreamQualityAccount(accountId)
    configs.value = configs.value.filter((c) => c.account_id !== accountId)
  } catch (err) {
    error.value = errorText(err)
  }
}

async function run(accountId: number) {
  runningId.value = accountId
  error.value = ''
  try {
    await runUpstreamQualityAccount(accountId)
    configs.value = await listUpstreamQualityConfigs()
    if (historyId.value === accountId) await openHistory(accountId)
  } catch (err) {
    error.value = errorText(err)
  } finally {
    runningId.value = null
  }
}

async function openHistory(accountId: number) {
  historyId.value = accountId
  try {
    history.value = (await getUpstreamQualityAccount(accountId, 100)).results
  } catch (err) {
    error.value = errorText(err)
  }
}

onMounted(loadAll)
</script>
