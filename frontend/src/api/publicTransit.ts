// 公开中转快照（匿名、无响应信封）。路径在 /api/v1 之外，直接用 fetch 读取。

export interface PublicTransitAvailability {
  range: string
  status: string
  uptime: number | null
  latency_p50_ms: number | null
  ttft_p50_ms: number | null
  sample_count: number
}

export interface PublicTransitGroup {
  name: string
  platform: string
  subscription_type?: string
  rate_multiplier: number
  peak_rate_enabled?: boolean
  peak_start?: string
  peak_end?: string
  peak_rate_multiplier?: number
  model_count: number
  availability?: PublicTransitAvailability | null
}

export interface PublicTransitModel {
  name: string
  group: string
  platform: string
  billing_mode: string
  input_price?: number
  output_price?: number
  cache_read_price?: number
  cache_write_price?: number
  per_request_price?: number
}

export interface PublicTransitSnapshot {
  schema: string
  generated_at: string
  site_name?: string
  price_unit: string
  recharge_multiplier: number
  groups: PublicTransitGroup[]
  models: PublicTransitModel[]
  cache: { range: string; hit_rate: number | null } | null
  availability: PublicTransitAvailability | null
}

export interface PublicTransitDiscovery {
  schema: string
  snapshot_url: string
  page_url?: string
  site_name?: string
  cache_ttl_seconds: number
}

async function getJSON<T>(path: string): Promise<T | null> {
  const response = await fetch(path, { headers: { Accept: 'application/json' }, credentials: 'omit' })
  if (response.status === 404) return null
  if (!response.ok) throw new Error(`HTTP ${response.status}`)
  return (await response.json()) as T
}

export function fetchPublicTransitDiscovery(): Promise<PublicTransitDiscovery | null> {
  return getJSON<PublicTransitDiscovery>('/.well-known/ai-transit.json')
}

export function fetchPublicTransitSnapshot(): Promise<PublicTransitSnapshot | null> {
  return getJSON<PublicTransitSnapshot>('/api/public/transit/v1/snapshot')
}
