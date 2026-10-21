import { apiClient } from '../client'

// ---- 上游倍率 / 余额变动 ----

export type UpstreamChangeDirectionFilter = 'both' | 'up' | 'down'

export interface UpstreamChangeAlertSettings {
  record_history: boolean
  retention_days: number
  rate_alert_enabled: boolean
  rate_alert_direction: UpstreamChangeDirectionFilter
  rate_min_change_percent: number
  merge_window_minutes: number
  cooldown_minutes: number
  balance_alert_enabled: boolean
  balance_threshold: number
  balance_unit: string
  balance_cooldown_minutes: number
  muted_account_ids: number[]
}

export type UpstreamChangeKind = 'rate_multiplier' | 'balance_low'
export type UpstreamChangeDirection = 'up' | 'down' | 'low' | 'recovered'

export interface UpstreamChangeEvent {
  id: number
  account_id: number
  account_name: string
  site: string
  kind: UpstreamChangeKind
  direction: UpstreamChangeDirection
  old_value: number | null
  new_value: number | null
  threshold?: number | null
  unit?: string
  notify_state: string
  notified_at?: string | null
  observed_at: string
}

export interface UpstreamChangeEventPage {
  items: UpstreamChangeEvent[]
  total: number
  page: number
  page_size: number
}

export interface UpstreamChangeEventQuery {
  account_id?: number
  site?: string
  kind?: UpstreamChangeKind | ''
  direction?: UpstreamChangeDirection | ''
  from?: string
  to?: string
  page?: number
  page_size?: number
}

export interface UpstreamRateSnapshot {
  id: number
  account_id: number
  site: string
  provider: string
  upstream_group?: string
  group_multiplier: number | null
  resolved_multiplier: number | null
  effective_multiplier: number | null
  balance: number | null
  balance_unit?: string
  observed_at: string
}

function compactParams(query: object): Record<string, string | number> {
  const out: Record<string, string | number> = {}
  for (const [key, value] of Object.entries(query)) {
    if (value === undefined || value === null || value === '' || value === 0) continue
    out[key] = value as string | number
  }
  return out
}

export async function getUpstreamChangeSettings(): Promise<UpstreamChangeAlertSettings> {
  const { data } = await apiClient.get<UpstreamChangeAlertSettings>('/admin/upstream-changes/settings')
  return data
}

export async function saveUpstreamChangeSettings(settings: UpstreamChangeAlertSettings): Promise<UpstreamChangeAlertSettings> {
  const { data } = await apiClient.put<UpstreamChangeAlertSettings>('/admin/upstream-changes/settings', settings)
  return data
}

export async function listUpstreamChangeEvents(query: UpstreamChangeEventQuery): Promise<UpstreamChangeEventPage> {
  const { data } = await apiClient.get<UpstreamChangeEventPage>('/admin/upstream-changes/events', { params: compactParams(query) })
  return { items: data?.items ?? [], total: data?.total ?? 0, page: data?.page ?? 1, page_size: data?.page_size ?? 50 }
}

export async function listUpstreamSnapshots(accountId: number, days = 7): Promise<UpstreamRateSnapshot[]> {
  const { data } = await apiClient.get<{ items: UpstreamRateSnapshot[] }>('/admin/upstream-changes/snapshots', { params: { account_id: accountId, days } })
  return data?.items ?? []
}

export async function listUpstreamChangeSites(): Promise<string[]> {
  const { data } = await apiClient.get<{ items: string[] }>('/admin/upstream-changes/sites')
  return data?.items ?? []
}

// ---- 上游质量探测 ----

export interface UpstreamQualityProbeSettings {
  enabled: boolean
  max_results_per_account: number
  input_token_tolerance_pct: number
}

export interface UpstreamQualityProbeResult {
  id: number
  account_id: number
  trigger: 'scheduled' | 'manual'
  protocol: string
  model: string
  upstream_model: string
  status: 'success' | 'failed'
  http_status: number
  error_message?: string
  ttft_ms: number | null
  total_ms: number
  output_tps: number | null
  input_tokens: number | null
  output_tokens: number | null
  cache_read_tokens: number | null
  cache_write_tokens: number | null
  estimated_input_tokens: number
  anomalies: string[]
  response_excerpt?: string
  created_at: string
}

export interface UpstreamQualityProbeConfig {
  account_id: number
  account_name?: string
  platform?: string
  enabled: boolean
  model: string
  interval_minutes: number
  /** Optional custom prompts used round-robin; disables the pong check. */
  prompts?: string[]
  next_run_at: string | null
  last_run_at: string | null
  updated_at: string
  latest?: UpstreamQualityProbeResult | null
}

export interface UpstreamQualityBatchResult {
  account_id: number
  status: 'enabled' | 'disabled' | 'skipped_duplicate' | 'failed'
  message?: string
  duplicate_of?: number
}

/** Bulk toggle 上游质量探测; saved model/interval/prompts are kept per account. */
export async function batchSetUpstreamQualityAccounts(body: { account_ids: number[]; enabled: boolean; model?: string; interval_minutes?: number }): Promise<UpstreamQualityBatchResult[]> {
  const { data } = await apiClient.post<{ results: UpstreamQualityBatchResult[] }>('/admin/upstream-quality-probes/accounts/batch', body)
  return data?.results ?? []
}

export interface UpstreamQualityProbeAccountView {
  config: UpstreamQualityProbeConfig | null
  results: UpstreamQualityProbeResult[]
}

export async function getUpstreamQualitySettings(): Promise<UpstreamQualityProbeSettings> {
  const { data } = await apiClient.get<UpstreamQualityProbeSettings>('/admin/upstream-quality-probes/settings')
  return data
}

export async function saveUpstreamQualitySettings(settings: UpstreamQualityProbeSettings): Promise<UpstreamQualityProbeSettings> {
  const { data } = await apiClient.put<UpstreamQualityProbeSettings>('/admin/upstream-quality-probes/settings', settings)
  return data
}

export async function listUpstreamQualityConfigs(): Promise<UpstreamQualityProbeConfig[]> {
  const { data } = await apiClient.get<{ items: UpstreamQualityProbeConfig[] }>('/admin/upstream-quality-probes/accounts')
  return data?.items ?? []
}

export async function getUpstreamQualityAccount(accountId: number, limit = 100): Promise<UpstreamQualityProbeAccountView> {
  const { data } = await apiClient.get<UpstreamQualityProbeAccountView>(`/admin/upstream-quality-probes/accounts/${accountId}`, { params: { limit } })
  return { config: data?.config ?? null, results: data?.results ?? [] }
}

export async function saveUpstreamQualityAccount(accountId: number, body: { enabled: boolean; model: string; interval_minutes: number; prompts?: string[] }): Promise<UpstreamQualityProbeConfig> {
  const { data } = await apiClient.put<UpstreamQualityProbeConfig>(`/admin/upstream-quality-probes/accounts/${accountId}`, body)
  return data
}

export async function deleteUpstreamQualityAccount(accountId: number): Promise<void> {
  await apiClient.delete(`/admin/upstream-quality-probes/accounts/${accountId}`)
}

export async function runUpstreamQualityAccount(accountId: number, model = ''): Promise<UpstreamQualityProbeResult> {
  const { data } = await apiClient.post<UpstreamQualityProbeResult>(`/admin/upstream-quality-probes/accounts/${accountId}/run`, { model }, { timeout: 120000 })
  return data
}

// ---- 公开中转快照 ----

export interface PublicTransitSettings {
  enabled: boolean
  page_enabled: boolean
  site_name: string
  availability_range: '24h' | '7d' | '30d'
}

export async function getPublicTransitSettings(): Promise<PublicTransitSettings> {
  const { data } = await apiClient.get<PublicTransitSettings>('/admin/public-transit/settings')
  return data
}

export async function savePublicTransitSettings(settings: PublicTransitSettings): Promise<PublicTransitSettings> {
  const { data } = await apiClient.put<PublicTransitSettings>('/admin/public-transit/settings', settings)
  return data
}
