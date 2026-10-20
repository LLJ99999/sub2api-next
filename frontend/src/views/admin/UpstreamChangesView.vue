<template>
  <AppLayout>
    <div class="min-w-0 space-y-5">
      <header>
        <h1 class="text-2xl font-semibold">{{ t('upstreamOps.changes.title') }}</h1>
        <p class="mt-2 text-sm text-gray-500">{{ t('upstreamOps.changes.description') }}</p>
      </header>
      <SmartOpsNav />
      <p v-if="error" role="alert" class="text-red-600">{{ error }}</p>
      <p v-if="notice" role="status" class="text-emerald-600">{{ notice }}</p>

      <form v-if="draft" class="card space-y-5 p-5" data-testid="upstream-change-settings" @submit.prevent="save">
        <div>
          <h2 class="text-lg font-semibold">{{ t('upstreamOps.changes.settingsTitle') }}</h2>
          <p class="mt-1 text-sm leading-6 text-gray-500">{{ t('upstreamOps.changes.settingsHint') }}</p>
        </div>
        <fieldset :disabled="saving" class="min-w-0 space-y-5">
          <section class="grid gap-4 sm:grid-cols-2">
            <label class="flex items-center gap-3 font-medium"><input v-model="draft.record_history" data-testid="record-history" type="checkbox" role="switch" />{{ t('upstreamOps.changes.recordHistory') }}</label>
            <label class="block max-w-xs"><span class="field-title">{{ t('upstreamOps.changes.retentionDays') }}</span><input v-model.number="draft.retention_days" type="number" min="1" max="365" required class="input w-full" /></label>
          </section>
          <section class="rounded-xl border border-gray-200 p-4 dark:border-dark-700">
            <label class="flex items-center gap-3 font-medium"><input v-model="draft.rate_alert_enabled" data-testid="rate-alert" type="checkbox" role="switch" />{{ t('upstreamOps.changes.rateAlert') }}</label>
            <div class="mt-4 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
              <label><span class="field-title">{{ t('upstreamOps.changes.direction') }}</span>
                <select v-model="draft.rate_alert_direction" class="input w-full" data-testid="rate-direction">
                  <option v-for="d in directionOptions" :key="d" :value="d">{{ t(`upstreamOps.changes.directions.${d}`) }}</option>
                </select>
              </label>
              <label><span class="field-title">{{ t('upstreamOps.changes.minChange') }}</span><input v-model.number="draft.rate_min_change_percent" type="number" min="0" max="1000" step="0.1" class="input w-full" /></label>
              <label><span class="field-title">{{ t('upstreamOps.changes.mergeWindow') }}</span><input v-model.number="draft.merge_window_minutes" type="number" min="0" max="1440" class="input w-full" /></label>
              <label><span class="field-title">{{ t('upstreamOps.changes.cooldown') }}</span><input v-model.number="draft.cooldown_minutes" type="number" min="0" max="10080" class="input w-full" /></label>
            </div>
            <p class="mt-3 text-xs leading-5 text-gray-500">{{ t('upstreamOps.changes.minChangeHint') }} {{ t('upstreamOps.changes.mergeWindowHint') }}</p>
          </section>
          <section class="rounded-xl border border-gray-200 p-4 dark:border-dark-700">
            <label class="flex items-center gap-3 font-medium"><input v-model="draft.balance_alert_enabled" data-testid="balance-alert" type="checkbox" role="switch" />{{ t('upstreamOps.changes.balanceAlert') }}</label>
            <div class="mt-4 grid gap-4 sm:grid-cols-3">
              <label><span class="field-title">{{ t('upstreamOps.changes.balanceThreshold') }}</span><input v-model.number="draft.balance_threshold" type="number" min="0" step="0.01" class="input w-full" /></label>
              <label><span class="field-title">{{ t('upstreamOps.changes.balanceUnit') }}</span><input v-model="draft.balance_unit" maxlength="16" class="input w-full" /></label>
              <label><span class="field-title">{{ t('upstreamOps.changes.balanceCooldown') }}</span><input v-model.number="draft.balance_cooldown_minutes" type="number" min="5" max="10080" class="input w-full" /></label>
            </div>
            <p class="mt-3 text-xs leading-5 text-gray-500">{{ t('upstreamOps.changes.balanceHint') }}</p>
          </section>
          <label class="block"><span class="field-title">{{ t('upstreamOps.changes.muted') }}</span><input v-model="mutedText" data-testid="muted" class="input w-full" placeholder="12, 34" /><small class="text-gray-500">{{ t('upstreamOps.changes.mutedHint') }}</small></label>
          <button class="btn btn-primary" type="submit">{{ t(saving ? 'upstreamOps.changes.saving' : 'upstreamOps.changes.save') }}</button>
        </fieldset>
      </form>

      <section class="card p-5">
        <h2 class="text-lg font-semibold">{{ t('upstreamOps.changes.historyTitle') }}</h2>
        <form class="mt-4 grid gap-3 sm:grid-cols-3 lg:grid-cols-7" @submit.prevent="search(1)">
          <input v-model.number="filters.account_id" type="number" min="1" class="input" :placeholder="t('upstreamOps.changes.filters.account')" :aria-label="t('upstreamOps.changes.filters.account')" />
          <select v-model="filters.site" class="input" :aria-label="t('upstreamOps.changes.filters.site')">
            <option value="">{{ t('upstreamOps.changes.filters.site') }} · {{ t('upstreamOps.changes.filters.all') }}</option>
            <option v-for="site in sites" :key="site" :value="site">{{ site }}</option>
          </select>
          <select v-model="filters.kind" class="input" :aria-label="t('upstreamOps.changes.filters.kind')">
            <option value="">{{ t('upstreamOps.changes.filters.kind') }} · {{ t('upstreamOps.changes.filters.all') }}</option>
            <option value="rate_multiplier">{{ t('upstreamOps.changes.kinds.rate_multiplier') }}</option>
            <option value="balance_low">{{ t('upstreamOps.changes.kinds.balance_low') }}</option>
          </select>
          <select v-model="filters.direction" class="input" :aria-label="t('upstreamOps.changes.filters.direction')">
            <option value="">{{ t('upstreamOps.changes.filters.direction') }} · {{ t('upstreamOps.changes.filters.all') }}</option>
            <option v-for="d in eventDirections" :key="d" :value="d">{{ t(`upstreamOps.changes.eventDirections.${d}`) }}</option>
          </select>
          <input v-model="filters.from" type="datetime-local" class="input" :aria-label="t('upstreamOps.changes.filters.from')" />
          <input v-model="filters.to" type="datetime-local" class="input" :aria-label="t('upstreamOps.changes.filters.to')" />
          <div class="flex gap-2"><button class="btn btn-primary" type="submit">{{ t('upstreamOps.changes.filters.apply') }}</button><button class="btn btn-secondary" type="button" @click="resetFilters">{{ t('upstreamOps.changes.filters.reset') }}</button></div>
        </form>
        <p v-if="!events.length && !loadingEvents" class="py-8 text-center text-sm text-gray-500" data-testid="changes-empty">{{ t('upstreamOps.changes.empty') }}</p>
        <div v-else class="mt-4 overflow-x-auto">
          <table class="w-full whitespace-nowrap text-left text-sm" data-testid="changes-table">
            <thead><tr><th v-for="key in columns" :key="key" class="px-3 py-3 text-xs font-medium text-gray-500">{{ t(`upstreamOps.changes.columns.${key}`) }}</th></tr></thead>
            <tbody>
              <tr v-for="ev in events" :key="ev.id" class="border-t border-gray-100 dark:border-dark-700">
                <td class="px-3 py-3 tabular-nums">{{ formatTime(ev.observed_at) }}</td>
                <td class="px-3 py-3"><button type="button" class="text-left text-primary-600 hover:underline" @click="loadTrend(ev.account_id)">{{ ev.account_name || '—' }}<small class="block text-gray-400">#{{ ev.account_id }}</small></button></td>
                <td class="px-3 py-3 text-gray-500">{{ ev.site || '—' }}</td>
                <td class="px-3 py-3">{{ t(`upstreamOps.changes.kinds.${ev.kind}`) }}</td>
                <td class="px-3 py-3 tabular-nums"><span :class="directionClass(ev.direction)">{{ t(`upstreamOps.changes.eventDirections.${ev.direction}`) }}</span> {{ formatChange(ev) }}</td>
                <td class="px-3 py-3">{{ t(`upstreamOps.changes.notifyStates.${ev.notify_state}`) }}<small v-if="ev.notified_at" class="block text-gray-400">{{ formatTime(ev.notified_at) }}</small></td>
              </tr>
            </tbody>
          </table>
          <div class="mt-3 flex items-center justify-between text-sm text-gray-500">
            <span>{{ t('upstreamOps.changes.total', { count: total }) }}</span>
            <span class="flex gap-2"><button class="btn btn-secondary" :disabled="page <= 1" @click="search(page - 1)">{{ t('upstreamOps.changes.prev') }}</button><button class="btn btn-secondary" :disabled="page * pageSize >= total" @click="search(page + 1)">{{ t('upstreamOps.changes.next') }}</button></span>
          </div>
        </div>
      </section>

      <section class="card p-5">
        <h2 class="text-lg font-semibold">{{ t('upstreamOps.changes.trendTitle') }}<span v-if="trendAccount"> · #{{ trendAccount }}</span></h2>
        <p v-if="!trendAccount" class="mt-2 text-sm text-gray-500">{{ t('upstreamOps.changes.trendHint') }}</p>
        <div v-else-if="trend.length" class="mt-4 max-h-96 overflow-auto">
          <table class="w-full whitespace-nowrap text-left text-sm">
            <thead><tr><th v-for="key in trendColumns" :key="key" class="px-3 py-2 text-xs font-medium text-gray-500">{{ t(`upstreamOps.changes.trendColumns.${key}`) }}</th></tr></thead>
            <tbody>
              <tr v-for="row in trend" :key="row.id" class="border-t border-gray-100 dark:border-dark-700">
                <td class="px-3 py-2 tabular-nums">{{ formatTime(row.observed_at) }}</td>
                <td class="px-3 py-2">{{ row.upstream_group || '—' }}</td>
                <td class="px-3 py-2 tabular-nums">{{ formatNumber(row.group_multiplier) }}</td>
                <td class="px-3 py-2 tabular-nums">{{ formatNumber(row.resolved_multiplier) }}</td>
                <td class="px-3 py-2 tabular-nums">{{ row.balance == null ? '—' : `${formatNumber(row.balance)} ${row.balance_unit || ''}` }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <p v-else class="mt-2 text-sm text-gray-500">{{ t('upstreamOps.changes.empty') }}</p>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import SmartOpsNav from '@/components/admin/operations/SmartOpsNav.vue'
import {
  getUpstreamChangeSettings, saveUpstreamChangeSettings, listUpstreamChangeEvents, listUpstreamChangeSites, listUpstreamSnapshots,
  type UpstreamChangeAlertSettings, type UpstreamChangeEvent, type UpstreamRateSnapshot, type UpstreamChangeDirection, type UpstreamChangeKind,
} from '@/api/admin/upstreamOps'

const { t } = useI18n()
const directionOptions = ['both', 'up', 'down'] as const
const eventDirections: UpstreamChangeDirection[] = ['up', 'down', 'low', 'recovered']
const columns = ['time', 'account', 'site', 'kind', 'change', 'notify'] as const
const trendColumns = ['time', 'group', 'groupRate', 'resolved', 'balance'] as const

const draft = ref<UpstreamChangeAlertSettings | null>(null)
const mutedText = ref('')
const saving = ref(false)
const error = ref('')
const notice = ref('')
const events = ref<UpstreamChangeEvent[]>([])
const sites = ref<string[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 50
const loadingEvents = ref(false)
const trend = ref<UpstreamRateSnapshot[]>([])
const trendAccount = ref<number | null>(null)
const filters = reactive<{ account_id: number | undefined; site: string; kind: UpstreamChangeKind | ''; direction: UpstreamChangeDirection | ''; from: string; to: string }>({
  account_id: undefined, site: '', kind: '', direction: '', from: '', to: '',
})

function formatTime(value: string) {
  return new Date(value).toLocaleString()
}
function formatNumber(value: number | null | undefined) {
  if (value == null) return '—'
  return Number(value.toFixed(6)).toString()
}
function formatChange(ev: UpstreamChangeEvent) {
  const unit = ev.unit ? ` ${ev.unit}` : ''
  const from = ev.old_value == null ? '—' : formatNumber(ev.old_value)
  const to = ev.new_value == null ? '—' : formatNumber(ev.new_value)
  const threshold = ev.threshold == null ? '' : ` (≤ ${formatNumber(ev.threshold)}${unit})`
  return `${from} → ${to}${ev.kind === 'balance_low' ? unit + threshold : '×'}`
}
function directionClass(direction: string) {
  if (direction === 'up' || direction === 'low') return 'text-red-600'
  if (direction === 'down' || direction === 'recovered') return 'text-emerald-600'
  return ''
}
function parseMuted(text: string): number[] {
  return [...new Set(text.split(/[\s,，]+/).map((v) => Number(v)).filter((v) => Number.isInteger(v) && v > 0))]
}
function toISO(local: string) {
  if (!local) return undefined
  const d = new Date(local)
  return Number.isNaN(d.getTime()) ? undefined : d.toISOString()
}

async function loadSettings() {
  try {
    const settings = await getUpstreamChangeSettings()
    draft.value = { ...settings, muted_account_ids: settings.muted_account_ids ?? [] }
    mutedText.value = draft.value.muted_account_ids.join(', ')
  } catch {
    error.value = t('upstreamOps.changes.loadError')
  }
}

async function save() {
  if (!draft.value) return
  saving.value = true
  error.value = ''
  notice.value = ''
  try {
    const saved = await saveUpstreamChangeSettings({ ...draft.value, muted_account_ids: parseMuted(mutedText.value) })
    draft.value = { ...saved, muted_account_ids: saved.muted_account_ids ?? [] }
    mutedText.value = draft.value.muted_account_ids.join(', ')
    notice.value = t('upstreamOps.changes.saved')
  } catch (err: unknown) {
    error.value = (err as { message?: string })?.message || t('upstreamOps.changes.loadError')
  } finally {
    saving.value = false
  }
}

async function search(nextPage = 1) {
  loadingEvents.value = true
  try {
    const result = await listUpstreamChangeEvents({
      account_id: filters.account_id || undefined, site: filters.site, kind: filters.kind, direction: filters.direction,
      from: toISO(filters.from), to: toISO(filters.to), page: nextPage, page_size: pageSize,
    })
    events.value = result.items
    total.value = result.total
    page.value = result.page
  } catch {
    error.value = t('upstreamOps.changes.loadError')
  } finally {
    loadingEvents.value = false
  }
}

function resetFilters() {
  Object.assign(filters, { account_id: undefined, site: '', kind: '', direction: '', from: '', to: '' })
  void search(1)
}

async function loadTrend(accountId: number) {
  trendAccount.value = accountId
  try {
    trend.value = await listUpstreamSnapshots(accountId, 7)
  } catch {
    trend.value = []
  }
}

onMounted(async () => {
  await Promise.all([loadSettings(), search(1), listUpstreamChangeSites().then((s) => { sites.value = s }).catch(() => undefined)])
})
</script>
