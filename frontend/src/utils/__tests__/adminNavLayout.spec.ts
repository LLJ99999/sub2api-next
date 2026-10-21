import { describe, expect, it } from 'vitest'
import { buildNavCatalog, groupSiblings, normalizeNavLayout, resolveNavLayout, type NavCatalogDef } from '../adminNavLayout'

let flagB = true
const defs: NavCatalogDef<string>[] = [
  { path: '/a', label: 'A', icon: 'i' },
  { path: '/ops', label: 'Ops', icon: 'g', expandOnly: true, flattenByDefault: true, children: [
    { path: '/o1', label: 'O1', icon: 'i' },
    { path: '/o2', label: 'O2', icon: 'i', featureFlag: () => flagB },
  ] },
  { path: '/ch', label: 'Channels', icon: 'g', expandOnly: true, hideInSimpleMode: true, children: [
    { path: '/ch/1', label: 'C1', icon: 'i' },
  ] },
  { path: '/z', label: 'Z', icon: 'i' },
]
const catalog = buildNavCatalog(defs)
const opts = { simpleMode: false, customGroupIcon: 'folder' }

describe('adminNavLayout', () => {
  it('flattens smart-ops style groups and keeps other built-in groups by default', () => {
    expect(catalog.defaultLayout.nodes).toEqual([
      { type: 'item', path: '/a' },
      { type: 'item', path: '/o1' },
      { type: 'item', path: '/o2' },
      { type: 'group', id: 'builtin:/ch', label: '', items: ['/ch/1'] },
      { type: 'item', path: '/z' },
    ])
    expect(resolveNavLayout(catalog.defaultLayout, catalog, opts).map(i => i.path)).toEqual(['/a', '/o1', '/o2', '/ch', '/z'])
  })

  it('falls back to default for empty or invalid stored layouts', () => {
    expect(normalizeNavLayout({}, catalog)).toEqual(catalog.defaultLayout)
    expect(normalizeNavLayout(null, catalog)).toEqual(catalog.defaultLayout)
  })

  it('applies custom groups, hidden entries, dedupe and drops unknown entries', () => {
    const layout = normalizeNavLayout({
      nodes: [
        { type: 'group', id: 'custom-x', label: '  Mine  ', items: ['/o2', '/z', '/nope', '/z'] },
        { type: 'item', path: '/a' },
        { type: 'item', path: '/a' },
        { type: 'group', id: 'evil', items: ['/o1'] },
      ],
      hidden: ['/a', '/missing'],
    }, catalog)
    expect(layout.hidden).toEqual(['/a'])
    expect(layout.nodes[0]).toEqual({ type: 'group', id: 'custom-x', label: 'Mine', items: ['/o2', '/z'] })
    // /o1 and the channels group were not mentioned: re-inserted next to default neighbours.
    const resolved = resolveNavLayout(layout, catalog, opts)
    expect(resolved.map(i => i.path)).toEqual(['group:custom-x', '/ch', '/o1'])
    expect(resolved[0].children?.map(c => c.path)).toEqual(['/o2', '/z'])
    expect(groupSiblings(resolved, '/z').map(c => c.path)).toEqual(['/o2', '/z'])
    expect(groupSiblings(resolved, '/o1')).toEqual([])
  })

  it('keeps parent feature flags and simple-mode rules when entries move', () => {
    const layout = normalizeNavLayout({ nodes: [{ type: 'item', path: '/ch/1' }, { type: 'item', path: '/o2' }] }, catalog)
    expect(resolveNavLayout(layout, catalog, { ...opts, simpleMode: true }).map(i => i.path)).not.toContain('/ch/1')
    flagB = false
    expect(resolveNavLayout(layout, catalog, opts).map(i => i.path)).not.toContain('/o2')
    flagB = true
  })

  it('drops groups whose entries are all hidden', () => {
    const layout = normalizeNavLayout({ nodes: [{ type: 'group', id: 'builtin:/ch', label: 'Renamed', items: ['/ch/1'] }], hidden: ['/ch/1'] }, catalog)
    expect(resolveNavLayout(layout, catalog, opts).find(i => i.path === '/ch')).toBeUndefined()
    const shown = resolveNavLayout({ ...layout, hidden: [] }, catalog, opts).find(i => i.path === '/ch')
    expect(shown?.label).toBe('Renamed')
  })
})
