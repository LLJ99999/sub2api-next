import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import {
  UPSTREAM_COST_VISIBILITY_STORAGE_KEY,
  resetUpstreamCostVisibilityForTests,
  useUpstreamCostVisibility,
} from '../useUpstreamCostVisibility'

describe('useUpstreamCostVisibility', () => {
  let store: Record<string, string>

  beforeEach(() => {
    store = {}
    vi.stubGlobal('localStorage', {
      getItem: vi.fn((key: string) => store[key] ?? null),
      setItem: vi.fn((key: string, value: string) => { store[key] = value }),
      removeItem: vi.fn((key: string) => { delete store[key] }),
    })
    resetUpstreamCostVisibilityForTests()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    resetUpstreamCostVisibilityForTests()
  })

  it('defaults to visible', () => {
    expect(useUpstreamCostVisibility().visible.value).toBe(true)
  })

  it('persists the toggle and shares state between callers', () => {
    const a = useUpstreamCostVisibility()
    const b = useUpstreamCostVisibility()
    a.toggle()
    expect(a.visible.value).toBe(false)
    expect(b.visible.value).toBe(false)
    expect(store[UPSTREAM_COST_VISIBILITY_STORAGE_KEY]).toBe('0')
    b.setVisible(true)
    expect(a.visible.value).toBe(true)
    expect(store[UPSTREAM_COST_VISIBILITY_STORAGE_KEY]).toBe('1')
  })

  it('restores the hidden state after reload', () => {
    store[UPSTREAM_COST_VISIBILITY_STORAGE_KEY] = '0'
    expect(useUpstreamCostVisibility().visible.value).toBe(false)
  })

  it('falls back to visible when storage is unavailable', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    vi.stubGlobal('localStorage', {
      getItem: () => { throw new Error('denied') },
      setItem: () => { throw new Error('denied') },
    })
    resetUpstreamCostVisibilityForTests()
    const vis = useUpstreamCostVisibility()
    expect(vis.visible.value).toBe(true)
    vis.toggle()
    expect(vis.visible.value).toBe(false)
    expect(warn).toHaveBeenCalled()
    warn.mockRestore()
  })
})
