<template>
  <div class="space-y-3 p-4" data-testid="usage-analytics-panel">
    <div class="flex flex-wrap items-center gap-2">
      <span class="text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('usage.analytics.dimension') }}:</span>
      <div class="inline-flex overflow-hidden rounded-lg border border-gray-200 dark:border-dark-600">
        <button
          v-for="option in dimensionOptions"
          :key="option.value"
          type="button"
          :data-testid="`usage-analytics-dimension-${option.value}`"
          class="px-3 py-1.5 text-sm transition-colors"
          :class="dimension === option.value
            ? 'bg-primary-500 text-white'
            : 'bg-white text-gray-600 hover:bg-gray-50 dark:bg-dark-800 dark:text-gray-300 dark:hover:bg-dark-700'"
          @click="setDimension(option.value)"
        >
          {{ option.label }}
        </button>
      </div>
      <span v-if="!showUpstreamCost" data-testid="usage-analytics-cost-hidden" class="ml-auto text-xs text-gray-400 dark:text-gray-500">
        {{ t('usage.analytics.costHiddenNote') }}
      </span>
    </div>

    <p v-if="errorMessage" data-testid="usage-analytics-error" class="rounded-lg bg-red-50 px-3 py-2 text-sm text-red-600 dark:bg-red-900/20 dark:text-red-400">
      {{ errorMessage }}
    </p>

    <div class="overflow-x-auto">
      <table class="min-w-full text-sm">
        <thead>
          <tr class="border-b border-gray-200 text-left text-xs uppercase tracking-wide text-gray-500 dark:border-dark-700 dark:text-gray-400">
            <th
              v-for="col in visibleColumns"
              :key="col.key"
              scope="col"
              class="cursor-pointer select-none whitespace-nowrap px-3 py-2 font-medium"
              :class="col.align === 'right' ? 'text-right' : 'text-left'"
              :title="col.hint"
              :data-testid="`usage-analytics-col-${col.key}`"
              @click="toggleSort(col.key)"
            >
              {{ col.label }}
              <span v-if="sortKey === col.key" aria-hidden="true">{{ sortOrder === 'asc' ? '↑' : '↓' }}</span>
            </th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="loading">
            <td :colspan="visibleColumns.length" class="px-3 py-6 text-center text-gray-400">{{ t('common.loading') }}</td>
          </tr>
          <tr v-else-if="sortedRows.length === 0">
            <td :colspan="visibleColumns.length" class="px-3 py-6 text-center text-gray-400" data-testid="usage-analytics-empty">{{ t('usage.analytics.empty') }}</td>
          </tr>
          <template v-else>
            <tr
              v-for="row in sortedRows"
              :key="row.key"
              data-testid="usage-analytics-row"
              class="border-b border-gray-100 hover:bg-gray-50 dark:border-dark-700/50 dark:hover:bg-dark-800/50"
            >
              <td v-for="col in visibleColumns" :key="col.key" class="whitespace-nowrap px-3 py-2" :class="cellClass(col, row)">
                {{ formatCell(col.key, row) }}
              </td>
            </tr>
            <tr v-if="data" data-testid="usage-analytics-total" class="border-t-2 border-gray-200 font-semibold dark:border-dark-600">
              <td v-for="col in visibleColumns" :key="col.key" class="whitespace-nowrap px-3 py-2" :class="cellClass(col, data.totals)">
                {{ col.key === 'name' ? t('usage.analytics.total') : formatCell(col.key, data.totals) }}
              </td>
            </tr>
          </template>
        </tbody>
      </table>
    </div>
    <p v-if="data?.truncated" class="text-xs text-gray-400 dark:text-gray-500">
      {{ t('usage.analytics.truncated', { count: data.rows.length }) }}
    </p>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminUsageAPI } from '@/api/admin/usage'
import type { UsageAnalyticsDimension, UsageAnalyticsResponse, UsageAnalyticsRow } from '@/api/admin/usage'

const props = defineProps<{
  startDate: string
  endDate: string
  groupId?: number | null
  accountId?: number | null
  model?: string | null
  /** 管理端「上游成本」开关；关闭时不渲染上游成本 / 毛利 / 毛利率列 */
  showUpstreamCost: boolean
}>()

const { t } = useI18n()

type ColumnKey =
  | 'name' | 'requests' | 'error_count' | 'cache_hit_rate'
  | 'first_token_p50_ms' | 'first_token_p95_ms' | 'output_tps_p50' | 'output_tps_p95'
  | 'total_tokens' | 'revenue' | 'upstream_cost' | 'gross_margin' | 'margin_pct'

interface AnalyticsColumn {
  key: ColumnKey
  label: string
  hint?: string
  align: 'left' | 'right'
  /** 上游成本相关列：开关关闭时整列隐藏 */
  upstreamCost?: boolean
}

const UPSTREAM_COST_COLUMNS: ReadonlySet<ColumnKey> = new Set(['upstream_cost', 'gross_margin', 'margin_pct'])

const dimension = ref<UsageAnalyticsDimension>('account')
const data = ref<UsageAnalyticsResponse | null>(null)
const loading = ref(false)
const errorMessage = ref('')
const sortKey = ref<ColumnKey>('revenue')
const sortOrder = ref<'asc' | 'desc'>('desc')
let controller: AbortController | null = null

const dimensionOptions = computed(() => [
  { value: 'account' as const, label: t('usage.analytics.byAccount') },
  { value: 'group' as const, label: t('usage.analytics.byGroup') },
  { value: 'model' as const, label: t('usage.analytics.byModel') },
])

const columns = computed<AnalyticsColumn[]>(() => [
  { key: 'name', label: t('usage.analytics.name'), align: 'left' },
  { key: 'requests', label: t('usage.analytics.requests'), align: 'right' },
  { key: 'error_count', label: t('usage.analytics.errors'), hint: t('usage.analytics.errorsHint'), align: 'right' },
  { key: 'cache_hit_rate', label: t('usage.analytics.cacheHitRate'), hint: t('usage.analytics.cacheHitRateHint'), align: 'right' },
  { key: 'first_token_p50_ms', label: t('usage.analytics.ttftP50'), align: 'right' },
  { key: 'first_token_p95_ms', label: t('usage.analytics.ttftP95'), align: 'right' },
  { key: 'output_tps_p50', label: t('usage.analytics.tpsP50'), hint: t('usage.latencyTpsHint'), align: 'right' },
  { key: 'output_tps_p95', label: t('usage.analytics.tpsP95'), hint: t('usage.latencyTpsHint'), align: 'right' },
  { key: 'total_tokens', label: t('usage.analytics.tokens'), align: 'right' },
  { key: 'revenue', label: t('usage.analytics.revenue'), hint: t('usage.analytics.revenueHint'), align: 'right' },
  { key: 'upstream_cost', label: t('usage.analytics.upstreamCost'), hint: t('usage.analytics.upstreamCostHint'), align: 'right', upstreamCost: true },
  { key: 'gross_margin', label: t('usage.analytics.grossMargin'), align: 'right', upstreamCost: true },
  { key: 'margin_pct', label: t('usage.analytics.marginPct'), align: 'right', upstreamCost: true },
])

const visibleColumns = computed(() =>
  columns.value.filter((col) => props.showUpstreamCost || !col.upstreamCost)
)

// 关闭上游成本后，若当前按成本类列排序，回落到按收入排序，避免排序泄露成本顺序。
watch(() => props.showUpstreamCost, (visible) => {
  if (!visible && UPSTREAM_COST_COLUMNS.has(sortKey.value)) {
    sortKey.value = 'revenue'
    sortOrder.value = 'desc'
  }
})

const rowName = (row: UsageAnalyticsRow): string => {
  if (dimension.value === 'group' && row.key === '0') return t('usage.analytics.noGroup')
  if (row.name) return row.name
  if (dimension.value === 'model') return row.key || t('usage.analytics.unknown')
  return row.key ? `#${row.key}` : t('usage.analytics.unknown')
}

const sortValue = (row: UsageAnalyticsRow, key: ColumnKey): number | string | null => {
  if (key === 'name') return rowName(row).toLowerCase()
  return row[key] ?? null
}

const sortedRows = computed(() => {
  const rows = [...(data.value?.rows ?? [])]
  const key = sortKey.value
  const dir = sortOrder.value === 'asc' ? 1 : -1
  return rows.sort((a, b) => {
    const av = sortValue(a, key)
    const bv = sortValue(b, key)
    // 空值无论升降序都排最后。
    if (av == null && bv == null) return 0
    if (av == null) return 1
    if (bv == null) return -1
    if (av < bv) return -dir
    if (av > bv) return dir
    return 0
  })
})

const toggleSort = (key: ColumnKey) => {
  if (sortKey.value === key) {
    sortOrder.value = sortOrder.value === 'asc' ? 'desc' : 'asc'
  } else {
    sortKey.value = key
    sortOrder.value = key === 'name' ? 'asc' : 'desc'
  }
}

const formatTokens = (value: number) => {
  if (value >= 1e9) return `${(value / 1e9).toFixed(2)}B`
  if (value >= 1e6) return `${(value / 1e6).toFixed(2)}M`
  if (value >= 1e3) return `${(value / 1e3).toFixed(2)}K`
  return value.toLocaleString()
}
const formatPct = (value: number | null) => (value == null ? '-' : `${(value * 100).toFixed(1)}%`)
const formatMs = (value: number | null) => {
  if (value == null) return '-'
  return value < 1000 ? `${Math.round(value)}ms` : `${(value / 1000).toFixed(2)}s`
}
const formatTps = (value: number | null) => {
  if (value == null) return '-'
  return `${value >= 100 ? Math.round(value) : value.toFixed(1)} t/s`
}
const formatMoney = (value: number) => {
  const sign = value < 0 ? '-' : ''
  return `${sign}$${Math.abs(value).toFixed(4)}`
}

const formatCell = (key: ColumnKey, row: UsageAnalyticsRow): string => {
  switch (key) {
    case 'name': return rowName(row)
    case 'requests': return row.requests.toLocaleString()
    case 'error_count': return row.error_count == null ? '-' : row.error_count.toLocaleString()
    case 'cache_hit_rate': return formatPct(row.cache_hit_rate)
    case 'first_token_p50_ms':
    case 'first_token_p95_ms': return formatMs(row[key])
    case 'output_tps_p50':
    case 'output_tps_p95': return formatTps(row[key])
    case 'total_tokens': return formatTokens(row.total_tokens)
    case 'revenue':
    case 'upstream_cost':
    case 'gross_margin': return formatMoney(row[key])
    case 'margin_pct': return formatPct(row.margin_pct)
  }
}

const cellClass = (col: AnalyticsColumn, row: UsageAnalyticsRow): string[] => {
  const classes = [col.align === 'right' ? 'text-right tabular-nums' : 'text-left']
  if (col.key === 'name') classes.push('font-medium text-gray-900 dark:text-white')
  else classes.push('text-gray-700 dark:text-gray-300')
  if ((col.key === 'gross_margin' || col.key === 'margin_pct') && row.gross_margin < 0) {
    classes.push('text-red-600 dark:text-red-400')
  }
  if (col.key === 'revenue') classes.push('text-green-600 dark:text-green-400')
  return classes
}

const load = async () => {
  if (!props.startDate || !props.endDate) return
  controller?.abort()
  const c = new AbortController()
  controller = c
  loading.value = true
  errorMessage.value = ''
  try {
    const res = await adminUsageAPI.getAnalytics({
      dimension: dimension.value,
      start_date: props.startDate,
      end_date: props.endDate,
      group_id: props.groupId || undefined,
      account_id: props.accountId || undefined,
      model: props.model || undefined,
    }, { signal: c.signal })
    if (c.signal.aborted) return
    data.value = res
  } catch (error: any) {
    if (error?.name === 'AbortError' || error?.name === 'CanceledError' || c.signal.aborted) return
    data.value = null
    const reason = error?.reason ?? error?.response?.data?.reason
    errorMessage.value = reason === 'USAGE_ANALYTICS_RANGE_TOO_LARGE'
      ? t('usage.analytics.rangeTooLarge')
      : t('usage.analytics.loadFailed')
  } finally {
    if (controller === c) loading.value = false
  }
}

const setDimension = (value: UsageAnalyticsDimension) => {
  if (dimension.value === value) return
  dimension.value = value
  data.value = null
  void load()
}

watch(
  () => [props.startDate, props.endDate, props.groupId, props.accountId, props.model],
  () => { void load() },
)

onMounted(() => { void load() })
onUnmounted(() => controller?.abort())

defineExpose({ reload: load })
</script>
