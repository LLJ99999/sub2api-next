<template>
  <HelpTooltip v-if="items.length > 0" class="!ml-0" width-class="w-80">
    <template #trigger>
      <span
        data-testid="account-cooldown-badge"
        class="inline-flex items-center gap-1 rounded bg-sky-100 px-1.5 py-0.5 text-xs font-medium text-sky-700 dark:bg-sky-900/30 dark:text-sky-300"
      >
        <Icon name="clock" size="xs" :stroke-width="2" />
        {{ t('admin.accounts.cooldownInfo.badge', { count: items.length }) }}
      </span>
    </template>
    <div class="space-y-2 text-left" data-testid="account-cooldown-details">
      <div class="font-semibold">{{ t('admin.accounts.cooldownInfo.title') }}</div>
      <div
        v-for="(item, index) in items"
        :key="`${item.kind}-${item.model || ''}-${index}`"
        class="border-t border-white/10 pt-1.5 first:border-t-0 first:pt-0"
      >
        <div class="flex flex-wrap items-center gap-x-2 gap-y-0.5">
          <span class="font-medium">{{ kindLabel(item) }}</span>
          <span class="opacity-80">{{ scopeLabel(item) }}</span>
          <span v-if="item.status_code" class="opacity-80">
            {{ t('admin.accounts.cooldownInfo.statusCode', { code: item.status_code }) }}
          </span>
        </div>
        <div v-if="item.keyword === breakerKeyword || item.breaker_level" class="opacity-80">
          {{ t('admin.accounts.cooldownInfo.breaker') }}
          <template v-if="item.breaker_level">
            · {{ t('admin.accounts.cooldownInfo.breakerLevel', { level: item.breaker_level }) }}
          </template>
        </div>
        <div v-if="item.message" class="whitespace-pre-wrap break-words opacity-90">{{ item.message }}</div>
        <div class="opacity-70">
          {{ t('admin.accounts.cooldownInfo.remaining', { time: remainingText(item) }) }}
        </div>
      </div>
      <button
        type="button"
        data-testid="account-cooldown-recover"
        class="mt-1 w-full rounded bg-emerald-600 px-2 py-1 text-xs font-medium text-white hover:bg-emerald-700 disabled:opacity-60"
        :disabled="recovering"
        @click.stop="emit('recover', account)"
      >
        {{ recovering ? t('admin.accounts.cooldownInfo.recovering') : t('admin.accounts.cooldownInfo.recover') }}
      </button>
    </div>
  </HelpTooltip>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import Icon from '@/components/icons/Icon.vue'
import type { Account, AccountCooldown } from '@/types'

const props = withDefaults(defineProps<{
  account: Account
  recovering?: boolean
}>(), { recovering: false })

const emit = defineEmits<{
  (e: 'recover', account: Account): void
}>()

const { t } = useI18n()

const breakerKeyword = 'upstream_relay_breaker'

// Only cooldowns that have not expired yet (the list may be a few seconds old).
const items = computed<AccountCooldown[]>(() => {
  const now = Date.now()
  return (props.account.cooldowns ?? []).filter((item) => {
    const until = new Date(item.until).getTime()
    return Number.isNaN(until) ? item.remaining_seconds > 0 : until > now
  })
})

const kindLabel = (item: AccountCooldown) => t(`admin.accounts.cooldownInfo.kinds.${item.kind}`)

const scopeLabel = (item: AccountCooldown) =>
  item.scope === 'model' && item.model
    ? t('admin.accounts.cooldownInfo.scopeModel', { model: item.model })
    : t('admin.accounts.cooldownInfo.scopeAccount')

const remainingText = (item: AccountCooldown) => {
  const until = new Date(item.until).getTime()
  const secs = Number.isNaN(until)
    ? Math.max(0, item.remaining_seconds)
    : Math.max(0, Math.ceil((until - Date.now()) / 1000))
  if (secs < 60) return `${secs}s`
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `${mins}m ${secs % 60}s`
  const hours = Math.floor(mins / 60)
  return `${hours}h ${mins % 60}m`
}
</script>
