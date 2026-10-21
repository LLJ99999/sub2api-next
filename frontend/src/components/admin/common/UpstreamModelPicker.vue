<template>
  <div class="relative inline-block" data-testid="upstream-model-picker">
    <button
      type="button"
      class="link-button whitespace-nowrap text-xs"
      data-testid="upstream-model-picker-open"
      :disabled="!canFetch || loading"
      :title="canFetch ? '' : t('upstreamModelPicker.needSource')"
      @click="openAndFetch"
    >
      {{ loading ? t('upstreamModelPicker.loading') : t('upstreamModelPicker.fetch') }}
    </button>
    <div
      v-if="open"
      class="absolute right-0 top-full z-50 mt-1 w-72 rounded-lg border border-gray-200 bg-white shadow-lg dark:border-dark-600 dark:bg-dark-700"
      data-testid="upstream-model-picker-panel"
    >
      <div class="border-b border-gray-200 p-2 dark:border-dark-600">
        <input v-model="query" type="text" class="input w-full text-sm" :placeholder="t('upstreamModelPicker.search')" data-testid="upstream-model-picker-search" />
      </div>
      <div class="max-h-60 overflow-auto py-1">
        <p v-if="!filtered.length" class="px-3 py-2 text-xs text-gray-500">{{ t('upstreamModelPicker.empty') }}</p>
        <label
          v-for="model in filtered"
          :key="model"
          class="flex cursor-pointer items-center gap-2 px-3 py-1.5 text-sm hover:bg-gray-100 dark:hover:bg-dark-600"
          data-testid="upstream-model-picker-option"
        >
          <input
            :type="multiple ? 'checkbox' : 'radio'"
            :checked="picked.includes(model)"
            name="upstream-model-picker"
            @change="toggle(model)"
          />
          <span class="truncate">{{ model }}</span>
        </label>
      </div>
      <div class="flex items-center justify-between border-t border-gray-200 p-2 text-xs dark:border-dark-600">
        <button v-if="multiple" type="button" class="link-button" @click="picked = [...filtered]">{{ t('upstreamModelPicker.selectAll') }}</button>
        <span v-else />
        <div class="flex gap-2">
          <button type="button" class="link-button" @click="open = false">{{ t('common.cancel') }}</button>
          <button type="button" class="btn btn-primary btn-sm" data-testid="upstream-model-picker-apply" :disabled="!picked.length" @click="apply">{{ t('upstreamModelPicker.apply') }}</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { accountsAPI } from '@/api/admin/accounts'
import type { SyncUpstreamModelsResult, SyncUpstreamPreviewParams } from '@/api/admin/accounts'

/**
 * 从上游拉取模型: fetches the live model list of an account (by id), a group
 * (union of its accounts) or draft credentials, and lets the admin pick one or
 * many. Manual typing in the host field stays possible; this only fills it.
 */
const props = withDefaults(defineProps<{
  accountId?: number | null
  groupId?: number | null
  credentials?: SyncUpstreamPreviewParams | null
  multiple?: boolean
  /** Currently entered models, pre-checked when the list opens. */
  current?: string[]
}>(), { accountId: null, groupId: null, credentials: null, multiple: false, current: () => [] })

const emit = defineEmits<{ pick: [models: string[]] }>()
const { t } = useI18n()
const appStore = useAppStore()

const open = ref(false)
const loading = ref(false)
const query = ref('')
const models = ref<string[]>([])
const picked = ref<string[]>([])

const canFetch = computed(() => Boolean((props.accountId && props.accountId > 0) || (props.groupId && props.groupId > 0) || props.credentials?.api_key))
const filtered = computed(() => {
  const q = query.value.trim().toLowerCase()
  return q ? models.value.filter((m) => m.toLowerCase().includes(q)) : models.value
})

async function fetchModels(): Promise<SyncUpstreamModelsResult> {
  if (props.accountId && props.accountId > 0) return accountsAPI.syncUpstreamModels(props.accountId)
  if (props.groupId && props.groupId > 0) return accountsAPI.syncGroupUpstreamModels(props.groupId)
  return accountsAPI.syncUpstreamModelsPreview(props.credentials as SyncUpstreamPreviewParams)
}

async function openAndFetch() {
  if (!canFetch.value || loading.value) return
  loading.value = true
  try {
    const result = await fetchModels()
    models.value = [...new Set((result?.models ?? []).map((m) => m.trim()).filter(Boolean))]
    if (!models.value.length) {
      appStore.showInfo(t('upstreamModelPicker.empty'))
      return
    }
    picked.value = props.current.filter((m) => models.value.includes(m))
    query.value = ''
    open.value = true
  } catch (err: unknown) {
    const message = (err as { message?: string })?.message
    appStore.showError(message || t('upstreamModelPicker.failed'))
  } finally {
    loading.value = false
  }
}

function toggle(model: string) {
  if (!props.multiple) {
    picked.value = [model]
    return
  }
  picked.value = picked.value.includes(model) ? picked.value.filter((m) => m !== model) : [...picked.value, model]
}

function apply() {
  emit('pick', [...picked.value])
  open.value = false
}

defineExpose({ openAndFetch })
</script>
