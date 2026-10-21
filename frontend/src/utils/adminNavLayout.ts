/**
 * Admin sidebar layout: which menu entries are shown, how they are grouped and
 * in what order. The layout is stored server-side (admin_nav_layout setting)
 * as an opaque JSON object and always normalized against the live menu
 * catalog, so menu entries added or removed by later releases never break it.
 */

/** A built-in menu definition as declared by the sidebar. */
export interface NavCatalogDef<TIcon = unknown> {
  path: string
  label: string
  icon: TIcon
  hideInSimpleMode?: boolean
  featureFlag?: () => boolean | undefined
  badge?: () => number
  expandOnly?: boolean
  children?: NavCatalogDef<TIcon>[]
  /** Built-in groups listed here are dissolved by default (children become top-level items). */
  flattenByDefault?: boolean
}

/** A leaf menu entry: the unit users can show, hide, move and group. */
export interface NavLeaf<TIcon = unknown> {
  path: string
  label: string
  icon: TIcon
  hideInSimpleMode?: boolean
  /** Every flag that must not be false for the entry to show (own + parent group's). */
  featureFlags: Array<() => boolean | undefined>
  badge?: () => number
  /** Label of the built-in group this entry belongs to by default, if any. */
  defaultGroupLabel?: string
}

export interface NavBuiltinGroup<TIcon = unknown> {
  id: string
  path: string
  label: string
  icon: TIcon
  expandOnly: boolean
}

export interface NavCatalog<TIcon = unknown> {
  leaves: NavLeaf<TIcon>[]
  builtinGroups: NavBuiltinGroup<TIcon>[]
  defaultLayout: NavLayout
}

export interface NavLayoutItemNode {
  type: 'item'
  path: string
}

export interface NavLayoutGroupNode {
  type: 'group'
  /** "builtin:<path>" for built-in groups, "custom-<random>" for user groups. */
  id: string
  /** Custom display name. Empty keeps the built-in group's name. */
  label: string
  items: string[]
}

export type NavLayoutNode = NavLayoutItemNode | NavLayoutGroupNode

export interface NavLayout {
  version: 1
  nodes: NavLayoutNode[]
  hidden: string[]
}

/** Entries that can never be hidden, so the layout editor stays reachable. */
export const NAV_LAYOUT_PINNED = new Set<string>(['/admin/settings'])

export const BUILTIN_GROUP_PREFIX = 'builtin:'
export const CUSTOM_GROUP_PREFIX = 'custom-'
export const MAX_GROUP_LABEL_LENGTH = 32

export function builtinGroupId(path: string): string {
  return BUILTIN_GROUP_PREFIX + path
}

export function newCustomGroupId(): string {
  return CUSTOM_GROUP_PREFIX + Math.random().toString(36).slice(2, 10)
}

export function isCustomGroup(node: NavLayoutNode): node is NavLayoutGroupNode {
  return node.type === 'group' && node.id.startsWith(CUSTOM_GROUP_PREFIX)
}

/** Build the catalog (leaves, built-in groups, default layout) from sidebar definitions. */
export function buildNavCatalog<TIcon>(defs: NavCatalogDef<TIcon>[]): NavCatalog<TIcon> {
  const leaves: NavLeaf<TIcon>[] = []
  const builtinGroups: NavBuiltinGroup<TIcon>[] = []
  const nodes: NavLayoutNode[] = []
  const seen = new Set<string>()

  const pushLeaf = (def: NavCatalogDef<TIcon>, parent?: NavCatalogDef<TIcon>): boolean => {
    if (seen.has(def.path)) return false
    seen.add(def.path)
    leaves.push({
      path: def.path,
      label: def.label,
      icon: def.icon,
      hideInSimpleMode: def.hideInSimpleMode || parent?.hideInSimpleMode,
      featureFlags: [parent?.featureFlag, def.featureFlag].filter((f): f is () => boolean | undefined => !!f),
      badge: def.badge,
      defaultGroupLabel: parent && !parent.flattenByDefault ? parent.label : undefined,
    })
    return true
  }

  for (const def of defs) {
    if (def.children?.length) {
      if (def.flattenByDefault) {
        for (const child of def.children) if (pushLeaf(child, def)) nodes.push({ type: 'item', path: child.path })
        continue
      }
      const id = builtinGroupId(def.path)
      builtinGroups.push({ id, path: def.path, label: def.label, icon: def.icon, expandOnly: def.expandOnly === true })
      const items: string[] = []
      for (const child of def.children) if (pushLeaf(child, def)) items.push(child.path)
      nodes.push({ type: 'group', id, label: '', items })
      continue
    }
    if (pushLeaf(def)) nodes.push({ type: 'item', path: def.path })
  }

  return { leaves, builtinGroups, defaultLayout: { version: 1, nodes, hidden: [] } }
}

function cloneLayout(layout: NavLayout): NavLayout {
  return {
    version: 1,
    nodes: layout.nodes.map(n => (n.type === 'group' ? { ...n, items: [...n.items] } : { ...n })),
    hidden: [...layout.hidden],
  }
}

/**
 * Normalize an untrusted stored layout against the catalog:
 * unknown entries are dropped, duplicates collapse to their first position,
 * and catalog entries the layout does not mention (new features) are inserted
 * next to their default neighbour.
 */
export function normalizeNavLayout(raw: unknown, catalog: NavCatalog): NavLayout {
  const known = new Set(catalog.leaves.map(l => l.path))
  const builtinIds = new Set(catalog.builtinGroups.map(g => g.id))
  const src = raw && typeof raw === 'object' ? (raw as Record<string, unknown>) : {}
  if (!Array.isArray(src.nodes)) return cloneLayout(catalog.defaultLayout)

  const placed = new Set<string>()
  const groupIds = new Set<string>()
  const nodes: NavLayoutNode[] = []
  const take = (path: unknown): string | null => {
    if (typeof path !== 'string' || !known.has(path) || placed.has(path)) return null
    placed.add(path)
    return path
  }

  for (const entry of src.nodes as unknown[]) {
    if (!entry || typeof entry !== 'object') continue
    const node = entry as Record<string, unknown>
    if (node.type === 'item') {
      const path = take(node.path)
      if (path) nodes.push({ type: 'item', path })
    } else if (node.type === 'group' && typeof node.id === 'string') {
      const id = node.id
      const valid = builtinIds.has(id) || (id.startsWith(CUSTOM_GROUP_PREFIX) && id.length <= 48)
      if (!valid || groupIds.has(id)) continue
      groupIds.add(id)
      const items = (Array.isArray(node.items) ? node.items : []).map(take).filter((p): p is string => !!p)
      const label = typeof node.label === 'string' ? node.label.trim().slice(0, MAX_GROUP_LABEL_LENGTH) : ''
      nodes.push({ type: 'group', id, label, items })
    }
  }

  // Insert entries the stored layout does not know about yet (new features),
  // next to their default neighbour. Entries of a built-in group go back into
  // that group, recreating it when the stored layout has none.
  const defaultOrder: string[] = []
  const defaultGroupOf = new Map<string, string>()
  for (const n of catalog.defaultLayout.nodes) {
    if (n.type === 'item') defaultOrder.push(n.path)
    else for (const p of n.items) { defaultOrder.push(p); defaultGroupOf.set(p, n.id) }
  }
  const indexAfterPredecessor = (i: number): number => {
    for (let j = i - 1; j >= 0; j--) {
      const prev = defaultOrder[j]
      const k = nodes.findIndex(n => (n.type === 'item' ? n.path === prev : n.items.includes(prev)))
      if (k >= 0) return k + 1
    }
    return 0
  }
  for (let i = 0; i < defaultOrder.length; i++) {
    const path = defaultOrder[i]
    if (placed.has(path)) continue
    placed.add(path)
    const gid = defaultGroupOf.get(path)
    if (gid) {
      const group = nodes.find((n): n is NavLayoutGroupNode => n.type === 'group' && n.id === gid)
      if (group) { group.items.push(path); continue }
      nodes.splice(indexAfterPredecessor(i), 0, { type: 'group', id: gid, label: '', items: [path] })
      continue
    }
    nodes.splice(indexAfterPredecessor(i), 0, { type: 'item', path })
  }

  const hidden = Array.isArray(src.hidden)
    ? [...new Set((src.hidden as unknown[]).filter((p): p is string => typeof p === 'string' && known.has(p) && !NAV_LAYOUT_PINNED.has(p)))]
    : []
  return { version: 1, nodes, hidden }
}

export interface ResolvedNavItem<TIcon = unknown> {
  path: string
  label: string
  icon: TIcon
  badge?: () => number
  expandOnly?: boolean
  children?: ResolvedNavItem<TIcon>[]
}

export interface ResolveOptions<TIcon> {
  simpleMode: boolean
  /** Icon for user-created groups. */
  customGroupIcon: TIcon
  /** Include hidden entries (used by previews); feature flags still apply. */
  includeHidden?: boolean
}

export function isLeafAvailable(leaf: NavLeaf, simpleMode: boolean): boolean {
  if (simpleMode && leaf.hideInSimpleMode) return false
  return leaf.featureFlags.every(flag => flag() !== false)
}

/** Turn a normalized layout into the sidebar tree, applying visibility, feature flags and simple mode. */
export function resolveNavLayout<TIcon>(layout: NavLayout, catalog: NavCatalog<TIcon>, opts: ResolveOptions<TIcon>): ResolvedNavItem<TIcon>[] {
  const leafByPath = new Map(catalog.leaves.map(l => [l.path, l]))
  const groupById = new Map(catalog.builtinGroups.map(g => [g.id, g]))
  const hidden = new Set(opts.includeHidden ? [] : layout.hidden)
  const show = (path: string): ResolvedNavItem<TIcon> | null => {
    const leaf = leafByPath.get(path)
    if (!leaf || hidden.has(path) || !isLeafAvailable(leaf, opts.simpleMode)) return null
    return { path: leaf.path, label: leaf.label, icon: leaf.icon, badge: leaf.badge }
  }

  const out: ResolvedNavItem<TIcon>[] = []
  for (const node of layout.nodes) {
    if (node.type === 'item') {
      const item = show(node.path)
      if (item) out.push(item)
      continue
    }
    const children = node.items.map(show).filter((c): c is ResolvedNavItem<TIcon> => !!c)
    if (!children.length) continue
    const builtin = groupById.get(node.id)
    if (builtin) {
      out.push({ path: builtin.path, label: node.label || builtin.label, icon: builtin.icon, expandOnly: builtin.expandOnly, children })
    } else {
      out.push({ path: `group:${node.id}`, label: node.label || '—', icon: opts.customGroupIcon, expandOnly: true, children })
    }
  }
  return out
}

/** The visible siblings of a route inside its group (empty when the route is not grouped). */
export function groupSiblings<TIcon>(resolved: ResolvedNavItem<TIcon>[], routePath: string): ResolvedNavItem<TIcon>[] {
  for (const item of resolved) {
    if (item.children?.some(c => c.path === routePath)) return item.children
  }
  return []
}

export function layoutEquals(a: NavLayout, b: NavLayout): boolean {
  return JSON.stringify(a) === JSON.stringify(b)
}
