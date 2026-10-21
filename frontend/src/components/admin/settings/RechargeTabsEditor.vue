<template>
  <div class="card" data-testid="recharge-tabs-editor">
    <div class="border-b border-gray-100 px-4 py-3 dark:border-dark-700">
      <div class="flex items-center justify-between gap-3">
        <div class="min-w-0">
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('admin.settings.payment.rechargeTabs.label') }}
          </h2>
          <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.settings.payment.rechargeTabs.hint') }}
          </p>
        </div>
        <button
          type="button"
          class="btn btn-primary btn-sm shrink-0"
          :disabled="tabs.length >= MAX_RECHARGE_TABS"
          data-testid="recharge-tabs-add"
          @click="addTab"
        >
          <Icon name="plus" size="sm" class="mr-1" />
          {{ t('admin.settings.payment.rechargeTabs.add') }}
        </button>
      </div>
    </div>

    <div class="space-y-3 p-4">
      <p v-if="!tabs.length" class="text-sm text-gray-500 dark:text-gray-400" data-testid="recharge-tabs-default">
        {{ t('admin.settings.payment.rechargeTabs.defaultHint') }}
      </p>

      <div
        v-for="(tab, index) in tabs"
        :key="tab.id"
        class="rounded-lg border border-gray-200 p-3 dark:border-dark-600"
        :data-testid="`recharge-tab-row-${index}`"
      >
        <div class="flex flex-wrap items-center gap-2">
          <input
            :value="tab.name"
            class="input min-w-[10rem] flex-1"
            :maxlength="MAX_RECHARGE_TAB_NAME"
            :placeholder="t('admin.settings.payment.rechargeTabs.namePlaceholder')"
            :data-testid="`recharge-tab-name-${index}`"
            @input="patch(index, { name: ($event.target as HTMLInputElement).value })"
          />
          <label class="flex items-center gap-1 text-sm text-gray-600 dark:text-gray-300">
            <input
              type="checkbox"
              :checked="tab.enabled"
              :data-testid="`recharge-tab-enabled-${index}`"
              @change="patch(index, { enabled: ($event.target as HTMLInputElement).checked })"
            />
            {{ t('admin.settings.payment.rechargeTabs.enabled') }}
          </label>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="index === 0" :title="t('admin.settings.payment.rechargeTabs.moveUp')" :data-testid="`recharge-tab-up-${index}`" @click="move(index, -1)">↑</button>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="index === tabs.length - 1" :title="t('admin.settings.payment.rechargeTabs.moveDown')" :data-testid="`recharge-tab-down-${index}`" @click="move(index, 1)">↓</button>
          <button type="button" class="btn btn-danger btn-sm" :data-testid="`recharge-tab-delete-${index}`" @click="remove(index)">
            {{ t('common.delete') }}
          </button>
        </div>

        <div class="mt-2">
          <span class="text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('admin.settings.payment.rechargeTabs.methods') }}</span>
          <div class="mt-1 flex flex-wrap gap-2">
            <label
              v-for="option in optionsFor(tab)"
              :key="option.key"
              class="flex items-center gap-1 rounded border border-gray-200 px-2 py-1 text-xs dark:border-dark-600"
              :class="option.known ? '' : 'border-dashed text-gray-500'"
            >
              <input
                type="checkbox"
                :checked="tab.methods.includes(option.key)"
                :data-testid="`recharge-tab-${index}-method-${option.key}`"
                @change="toggleMethod(index, option.key, ($event.target as HTMLInputElement).checked)"
              />
              {{ option.label }}
              <span class="text-gray-400">({{ option.key }})</span>
            </label>
          </div>
          <div class="mt-2 flex items-center gap-2">
            <input
              v-model="customMethodDraft[tab.id]"
              class="input input-sm w-48"
              :placeholder="t('admin.settings.payment.rechargeTabs.customMethodPlaceholder')"
              :data-testid="`recharge-tab-custom-${index}`"
              @keydown.enter.prevent="addCustomMethod(index)"
            />
            <button type="button" class="btn btn-secondary btn-sm" @click="addCustomMethod(index)">
              {{ t('admin.settings.payment.rechargeTabs.addMethod') }}
            </button>
          </div>
        </div>
      </div>

      <p v-if="errorKey" class="text-xs text-red-600 dark:text-red-400" data-testid="recharge-tabs-error">
        {{ t(`admin.settings.payment.rechargeTabs.errors.${errorKey}`, { max: MAX_RECHARGE_TABS, maxName: MAX_RECHARGE_TAB_NAME }) }}
      </p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, reactive } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { ProviderInstance, RechargeTab } from '@/types/payment'
import { normalizeVisibleMethod } from '@/components/payment/paymentFlow'
import {
  MAX_RECHARGE_TABS,
  MAX_RECHARGE_TAB_NAME,
  nextRechargeTabId,
  validateRechargeTabsDraft,
} from '@/utils/rechargeTabs'

/**
 * 充值页多 tab 配置：每个 tab 自定义名称、排序（列表顺序）、启用开关，并绑定一个或多个
 * 支付方式（用户可见 key，含 EasyPay customMethods 的 type，例如 binance）。
 * 空列表 = 默认单个「充值」tab 显示全部方式。
 */
const props = defineProps<{
  modelValue: RechargeTab[]
  providers: ProviderInstance[]
}>()
const emit = defineEmits<{ 'update:modelValue': [tabs: RechargeTab[]] }>()
const { t, te } = useI18n()

const tabs = computed(() => props.modelValue ?? [])
const errorKey = computed(() => validateRechargeTabsDraft(tabs.value))
const customMethodDraft = reactive<Record<string, string>>({})

interface MethodOption { key: string; label: string; known: boolean }

function userFacingKey(providerKey: string, type: string): string {
  if (providerKey === 'stripe') return 'stripe'
  if (providerKey === 'airwallex') return 'airwallex'
  return normalizeVisibleMethod(type) || type.trim().toLowerCase()
}

function customMethodNames(provider: ProviderInstance): Record<string, string> {
  const names: Record<string, string> = {}
  const raw = provider.config?.customMethods
  if (!raw) return names
  try {
    const parsed = JSON.parse(raw)
    if (Array.isArray(parsed)) {
      parsed.forEach((item) => {
        const type = String(item?.type ?? '').trim()
        if (type) names[type] = String(item?.displayName ?? '').trim()
      })
    }
  } catch {
    // ignore malformed customMethods; the provider dialog validates it
  }
  return names
}

function methodLabel(key: string): string {
  const path = `payment.methods.${key}`
  return te(path) ? t(path) : key
}

/** Every user-facing method key the configured provider instances can expose. */
const knownOptions = computed<MethodOption[]>(() => {
  const map = new Map<string, MethodOption>()
  props.providers.forEach((provider) => {
    const names = customMethodNames(provider)
    ;(provider.supported_types || []).forEach((type) => {
      const key = userFacingKey(provider.provider_key, type)
      if (!key || map.has(key)) return
      map.set(key, { key, label: names[type] || methodLabel(key), known: true })
    })
  })
  return [...map.values()]
})

function optionsFor(tab: RechargeTab): MethodOption[] {
  const known = knownOptions.value
  const extra = tab.methods
    .filter((key) => !known.some((option) => option.key === key))
    .map((key) => ({ key, label: methodLabel(key), known: false }))
  return [...known, ...extra]
}

function commit(next: RechargeTab[]) {
  emit('update:modelValue', next.map((tab, index) => ({ ...tab, sort_order: index + 1 })))
}

function patch(index: number, change: Partial<RechargeTab>) {
  commit(tabs.value.map((tab, i) => (i === index ? { ...tab, ...change } : tab)))
}

function addTab() {
  if (tabs.value.length >= MAX_RECHARGE_TABS) return
  // The first tab starts as 「充值」 with every known method, i.e. today's default page.
  const first = tabs.value.length === 0
  commit([
    ...tabs.value,
    {
      id: first ? 'recharge' : nextRechargeTabId(tabs.value),
      name: first ? t('payment.tabTopUp') : '',
      sort_order: tabs.value.length + 1,
      enabled: true,
      methods: first ? knownOptions.value.map((option) => option.key) : [],
    },
  ])
}

function remove(index: number) {
  commit(tabs.value.filter((_, i) => i !== index))
}

function move(index: number, delta: number) {
  const target = index + delta
  if (target < 0 || target >= tabs.value.length) return
  const next = [...tabs.value]
  ;[next[index], next[target]] = [next[target], next[index]]
  commit(next)
}

function toggleMethod(index: number, key: string, checked: boolean) {
  const tab = tabs.value[index]
  const methods = checked ? [...new Set([...tab.methods, key])] : tab.methods.filter((m) => m !== key)
  patch(index, { methods })
}

function addCustomMethod(index: number) {
  const tab = tabs.value[index]
  const key = (customMethodDraft[tab.id] || '').trim().toLowerCase()
  if (!/^[a-z0-9_.-]{1,64}$/.test(key)) return
  customMethodDraft[tab.id] = ''
  toggleMethod(index, key, true)
}
</script>
