<template>
  <div class="space-y-6">
    <div class="card p-6">
      <div class="mb-4 flex flex-wrap items-start justify-between gap-3">
        <div>
          <h3 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.settings.navLayout.title') }}</h3>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.settings.navLayout.description') }}</p>
        </div>
        <div class="flex flex-wrap gap-2">
          <button type="button" class="btn btn-secondary" data-testid="nav-layout-add-group" @click="addGroup">
            <Icon name="plus" size="sm" />{{ t('admin.settings.navLayout.addGroup') }}
          </button>
          <button type="button" class="btn btn-secondary" data-testid="nav-layout-reset" @click="resetDraft">
            <Icon name="refresh" size="sm" />{{ t('admin.settings.navLayout.reset') }}
          </button>
        </div>
      </div>

      <div v-if="!draft" class="py-8 text-center text-sm text-gray-500">{{ t('common.loading') }}</div>
      <ul v-else class="space-y-2" data-testid="nav-layout-nodes">
        <li v-for="(node, index) in draft.nodes" :key="nodeKey(node)" class="rounded-xl border border-gray-200 dark:border-dark-700">
          <!-- Top-level item -->
          <div v-if="node.type === 'item'" class="flex flex-wrap items-center gap-3 px-3 py-2">
            <ItemRow :path="node.path" />
            <MoveButtons :up="index > 0" :down="index < draft.nodes.length - 1" @up="moveNode(index, -1)" @down="moveNode(index, 1)" />
          </div>

          <!-- Group -->
          <template v-else>
            <div class="flex flex-wrap items-center gap-3 rounded-t-xl bg-gray-50 px-3 py-2 dark:bg-dark-800">
              <Icon name="grid" size="sm" class="text-gray-400" />
              <input
                v-model="node.label"
                class="input h-8 w-48 py-1 text-sm"
                :maxlength="MAX_GROUP_LABEL_LENGTH"
                :placeholder="builtinLabel(node.id) || t('admin.settings.navLayout.groupNamePlaceholder')"
                :aria-label="t('admin.settings.navLayout.groupName')"
                data-testid="nav-layout-group-name"
              />
              <span class="text-xs text-gray-400">{{ t('admin.settings.navLayout.groupCount', { n: node.items.length }) }}</span>
              <div class="ml-auto flex items-center gap-1">
                <MoveButtons :up="index > 0" :down="index < draft.nodes.length - 1" @up="moveNode(index, -1)" @down="moveNode(index, 1)" />
                <button type="button" class="btn btn-ghost btn-sm text-red-500" :title="t('admin.settings.navLayout.dissolve')" data-testid="nav-layout-dissolve" @click="dissolveGroup(index)">
                  <Icon name="trash" size="sm" />
                </button>
              </div>
            </div>
            <ul class="divide-y divide-gray-100 dark:divide-dark-700">
              <li v-if="!node.items.length" class="px-6 py-3 text-xs text-gray-400">{{ t('admin.settings.navLayout.emptyGroup') }}</li>
              <li v-for="(path, childIndex) in node.items" :key="path" class="flex flex-wrap items-center gap-3 py-2 pl-8 pr-3">
                <ItemRow :path="path" />
                <MoveButtons :up="childIndex > 0" :down="childIndex < node.items.length - 1" @up="moveChild(node, childIndex, -1)" @down="moveChild(node, childIndex, 1)" />
              </li>
            </ul>
          </template>
        </li>
      </ul>

      <div class="mt-6 flex justify-end gap-2">
        <button type="button" class="btn btn-primary" :disabled="!draft || saving || !dirty" data-testid="nav-layout-save" @click="save">
          {{ saving ? t('common.saving') : t('common.save') }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, ref, watch, type PropType } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores'
import { useAdminNavLayoutStore } from '@/stores/adminNavLayout'
import {
  MAX_GROUP_LABEL_LENGTH,
  NAV_LAYOUT_PINNED,
  layoutEquals,
  newCustomGroupId,
  normalizeNavLayout,
  type NavLayout,
  type NavLayoutGroupNode,
  type NavLayoutNode,
} from '@/utils/adminNavLayout'

const { t } = useI18n()
const appStore = useAppStore()
const store = useAdminNavLayoutStore()

const draft = ref<NavLayout | null>(null)
const saving = ref(false)

const leafByPath = computed(() => new Map((store.catalog?.leaves ?? []).map(l => [l.path, l])))
const builtinById = computed(() => new Map((store.catalog?.builtinGroups ?? []).map(g => [g.id, g])))
const groups = computed(() => (draft.value?.nodes ?? []).filter((n): n is NavLayoutGroupNode => n.type === 'group'))
const dirty = computed(() => !!draft.value && !!store.layout && !layoutEquals(draft.value, store.layout))

function cloneLayout(layout: NavLayout): NavLayout {
  return JSON.parse(JSON.stringify(layout)) as NavLayout
}

// Load (and reload after a save) whenever the stored layout or catalog changes, unless editing.
watch(() => store.layout, (layout) => {
  if (layout && (!draft.value || !dirty.value)) draft.value = cloneLayout(layout)
}, { immediate: true })
void store.fetch()

function nodeKey(node: NavLayoutNode): string {
  return node.type === 'item' ? node.path : node.id
}

function builtinLabel(id: string): string {
  return builtinById.value.get(id)?.label ?? ''
}

function leafLabel(path: string): string {
  return leafByPath.value.get(path)?.label ?? path
}

function isHidden(path: string): boolean {
  return draft.value?.hidden.includes(path) ?? false
}

function setVisible(path: string, visible: boolean) {
  if (!draft.value || NAV_LAYOUT_PINNED.has(path)) return
  const hidden = new Set(draft.value.hidden)
  if (visible) hidden.delete(path)
  else hidden.add(path)
  draft.value.hidden = [...hidden]
}

function locationOf(path: string): string {
  const group = groups.value.find(g => g.items.includes(path))
  return group ? group.id : ''
}

function groupDisplayName(group: NavLayoutGroupNode): string {
  return group.label || builtinLabel(group.id) || t('admin.settings.navLayout.unnamedGroup')
}

/** Move an entry to the top level (target '') or to the end of a group. */
function relocate(path: string, target: string) {
  if (!draft.value) return
  const nodes = draft.value.nodes
  let anchor = nodes.length
  for (let i = 0; i < nodes.length; i++) {
    const n = nodes[i]
    if (n.type === 'item' && n.path === path) { nodes.splice(i, 1); anchor = i; break }
    if (n.type === 'group' && n.items.includes(path)) { n.items.splice(n.items.indexOf(path), 1); anchor = i + 1; break }
  }
  if (!target) {
    nodes.splice(Math.min(anchor, nodes.length), 0, { type: 'item', path })
    return
  }
  const group = nodes.find((n): n is NavLayoutGroupNode => n.type === 'group' && n.id === target)
  if (group) group.items.push(path)
  else nodes.splice(Math.min(anchor, nodes.length), 0, { type: 'item', path })
}

function moveNode(index: number, delta: number) {
  if (!draft.value) return
  const nodes = draft.value.nodes
  const to = index + delta
  if (to < 0 || to >= nodes.length) return
  const [node] = nodes.splice(index, 1)
  nodes.splice(to, 0, node)
}

function moveChild(group: NavLayoutGroupNode, index: number, delta: number) {
  const to = index + delta
  if (to < 0 || to >= group.items.length) return
  const [path] = group.items.splice(index, 1)
  group.items.splice(to, 0, path)
}

function addGroup() {
  if (!draft.value) return
  const n = groups.value.length + 1
  draft.value.nodes.unshift({ type: 'group', id: newCustomGroupId(), label: t('admin.settings.navLayout.newGroupName', { n }), items: [] })
}

function dissolveGroup(index: number) {
  if (!draft.value) return
  const node = draft.value.nodes[index]
  if (node?.type !== 'group') return
  draft.value.nodes.splice(index, 1, ...node.items.map(path => ({ type: 'item' as const, path })))
}

function resetDraft() {
  if (store.catalog) draft.value = cloneLayout(store.catalog.defaultLayout)
}

async function save() {
  if (!draft.value || !store.catalog) return
  saving.value = true
  try {
    const normalized = normalizeNavLayout(draft.value, store.catalog)
    const isDefault = layoutEquals(normalized, store.catalog.defaultLayout)
    await store.save(isDefault ? null : normalized)
    draft.value = store.layout ? cloneLayout(store.layout) : normalized
    appStore.showSuccess(t('admin.settings.navLayout.saved'))
  } catch (err) {
    appStore.showError((err as { message?: string })?.message || t('admin.settings.navLayout.saveFailed'))
  } finally {
    saving.value = false
  }
}

const MoveButtons = defineComponent({
  props: { up: Boolean, down: Boolean },
  emits: ['up', 'down'],
  setup(props, { emit }) {
    const btn = (dir: 'up' | 'down', enabled: boolean) => h('button', {
      type: 'button',
      class: 'btn btn-ghost btn-sm px-1.5',
      disabled: !enabled,
      title: t(dir === 'up' ? 'admin.settings.navLayout.moveUp' : 'admin.settings.navLayout.moveDown'),
      onClick: () => emit(dir),
    }, [h(Icon, { name: dir === 'up' ? 'chevronUp' : 'chevronDown', size: 'sm' })])
    return () => h('div', { class: 'flex items-center gap-0.5' }, [btn('up', props.up), btn('down', props.down)])
  },
})

const ItemRow = defineComponent({
  props: { path: { type: String as PropType<string>, required: true } },
  setup(props) {
    return () => {
      const pinned = NAV_LAYOUT_PINNED.has(props.path)
      const visible = !isHidden(props.path)
      const leaf = leafByPath.value.get(props.path)
      return h('div', { class: 'flex min-w-0 flex-1 flex-wrap items-center gap-3' }, [
        h('label', { class: 'flex min-w-0 flex-1 cursor-pointer items-center gap-2' }, [
          h('input', {
            type: 'checkbox',
            class: 'h-4 w-4 rounded border-gray-300 text-primary-600',
            checked: visible,
            disabled: pinned,
            'data-testid': `nav-layout-visible-${props.path}`,
            onChange: (e: Event) => setVisible(props.path, (e.target as HTMLInputElement).checked),
          }),
          h('span', { class: ['truncate text-sm', visible ? 'text-gray-800 dark:text-gray-100' : 'text-gray-400 line-through'] }, leafLabel(props.path)),
          leaf?.defaultGroupLabel ? h('span', { class: 'text-xs text-gray-400' }, leaf.defaultGroupLabel) : null,
        ]),
        h('select', {
          class: 'input h-8 w-40 py-1 text-xs',
          value: locationOf(props.path),
          'aria-label': t('admin.settings.navLayout.location'),
          'data-testid': `nav-layout-location-${props.path}`,
          onChange: (e: Event) => relocate(props.path, (e.target as HTMLSelectElement).value),
        }, [
          h('option', { value: '' }, t('admin.settings.navLayout.topLevel')),
          ...groups.value.map(g => h('option', { value: g.id }, groupDisplayName(g))),
        ]),
      ])
    }
  },
})
</script>
