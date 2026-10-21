import { defineStore } from 'pinia'
import { computed, ref, shallowRef } from 'vue'
import { getAdminNavLayout, updateAdminNavLayout } from '@/api/admin/settings'
import { normalizeNavLayout, type NavCatalog, type NavLayout, type ResolvedNavItem } from '@/utils/adminNavLayout'

/**
 * Admin sidebar layout. The sidebar owns the menu definitions, so it publishes
 * the catalog and the resolved tree here; the settings editor and in-page tab
 * strips read them back.
 */
export const useAdminNavLayoutStore = defineStore('adminNavLayout', () => {
  const raw = ref<Record<string, unknown>>({})
  const loaded = ref(false)
  const catalog = shallowRef<NavCatalog | null>(null)
  const resolved = shallowRef<ResolvedNavItem[]>([])
  let pending: Promise<void> | null = null

  const layout = computed<NavLayout | null>(() => (catalog.value ? normalizeNavLayout(raw.value, catalog.value) : null))

  async function fetch(force = false): Promise<void> {
    if (loaded.value && !force) return
    if (pending) return pending
    pending = (async () => {
      try {
        raw.value = await getAdminNavLayout()
        loaded.value = true
      } catch (err) {
        // Keep the current (default) layout on transient failures.
        console.error('[adminNavLayout] Failed to fetch layout:', err)
      } finally {
        pending = null
      }
    })()
    return pending
  }

  async function save(next: NavLayout | null): Promise<void> {
    raw.value = await updateAdminNavLayout(next ? { ...next } : {})
    loaded.value = true
  }

  function setCatalog(next: NavCatalog) { catalog.value = next }
  function setResolved(next: ResolvedNavItem[]) { resolved.value = next }

  return { raw, loaded, catalog, resolved, layout, fetch, save, setCatalog, setResolved }
})
