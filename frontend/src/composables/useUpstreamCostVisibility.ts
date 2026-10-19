import { readonly, ref, type Ref } from 'vue'

/**
 * 管理端用量页「上游成本」显示开关。
 *
 * 关闭后统一隐藏所有上游成本信息（表格 A $ 行、费用提示里的账号倍率/账号计费、
 * 统计卡片的账号成本、导出的账号成本列、聚合分析里的上游成本/毛利），
 * 方便运营截图时不暴露上游成本。状态存 localStorage，跨页面与刷新保持；默认显示。
 */
export const UPSTREAM_COST_VISIBILITY_STORAGE_KEY = 'admin-usage-upstream-cost-visible'

let state: Ref<boolean> | null = null

function readPersisted(): boolean {
  try {
    const raw = window.localStorage.getItem(UPSTREAM_COST_VISIBILITY_STORAGE_KEY)
    return raw !== '0' && raw !== 'false'
  } catch {
    return true
  }
}

function getState(): Ref<boolean> {
  if (!state) state = ref(readPersisted())
  return state
}

export function useUpstreamCostVisibility() {
  const visible = getState()
  const setVisible = (value: boolean) => {
    visible.value = value
    try {
      window.localStorage.setItem(UPSTREAM_COST_VISIBILITY_STORAGE_KEY, value ? '1' : '0')
    } catch (error) {
      console.warn('Failed to persist upstream cost visibility:', error)
    }
  }
  const toggle = () => setVisible(!visible.value)
  return { visible: readonly(visible), setVisible, toggle }
}

/** 仅供测试：清空模块级状态，下次调用时重新读取 localStorage。 */
export function resetUpstreamCostVisibilityForTests(): void {
  state = null
}
