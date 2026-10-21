<template>
  <BaseDialog :show="show" :title="title" width="normal" @close="emit('close')">
    <div class="space-y-4" data-testid="upstream-probe-bulk-dialog">
      <p class="text-sm text-gray-600 dark:text-gray-300">
        {{ t('admin.accounts.upstreamProbeBulk.summary', { count: accountIds.length }) }}
      </p>
      <p class="text-xs text-gray-500">{{ t('admin.accounts.upstreamProbeBulk.dedupHint') }}</p>
      <div v-if="kind === 'quality'" class="grid gap-3 sm:grid-cols-3">
        <label class="sm:col-span-2">
          <span class="input-label">{{ t('admin.accounts.upstreamProbeBulk.model') }}</span>
          <input v-model.trim="model" class="input w-full" maxlength="128" data-testid="upstream-probe-bulk-model" :placeholder="t('admin.accounts.upstreamProbeBulk.modelPlaceholder')" />
        </label>
        <label>
          <span class="input-label">{{ t('admin.accounts.upstreamProbeBulk.interval') }}</span>
          <input v-model.number="intervalMinutes" type="number" min="5" max="1440" class="input w-full" />
        </label>
      </div>
      <div v-if="results.length" class="max-h-72 overflow-auto rounded border border-gray-200 dark:border-dark-600">
        <table class="w-full text-sm">
          <thead class="bg-gray-50 text-left text-xs text-gray-500 dark:bg-dark-800">
            <tr>
              <th class="px-3 py-2">{{ t('admin.accounts.upstreamProbeBulk.account') }}</th>
              <th class="px-3 py-2">{{ t('admin.accounts.upstreamProbeBulk.status') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in results" :key="row.account_id" class="border-t border-gray-100 dark:border-dark-700" :data-testid="`upstream-probe-bulk-row-${row.account_id}`">
              <td class="px-3 py-2">{{ accountLabel(row.account_id) }}</td>
              <td class="px-3 py-2" :class="statusClass(row.status)">
                {{ t(`admin.accounts.upstreamProbeBulk.statuses.${row.status}`) }}
                <span v-if="row.duplicate_of" class="text-xs text-gray-500">({{ t('admin.accounts.upstreamProbeBulk.duplicateOf', { name: accountLabel(row.duplicate_of) }) }})</span>
                <span v-if="row.message" class="block text-xs text-gray-500">{{ row.message }}</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
    <template #footer>
      <div class="flex justify-end gap-2">
        <button type="button" class="btn btn-secondary" @click="emit('close')">{{ t('common.close') }}</button>
        <button type="button" class="btn btn-warning" :disabled="running" data-testid="upstream-probe-bulk-disable" @click="run(false)">{{ t('admin.accounts.upstreamProbeBulk.disable') }}</button>
        <button type="button" class="btn btn-primary" :disabled="running" data-testid="upstream-probe-bulk-enable" @click="run(true)">{{ t('admin.accounts.upstreamProbeBulk.enable') }}</button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { useAppStore } from '@/stores/app'
import { accountsAPI, type UpstreamProbeBatchResult } from '@/api/admin/accounts'
import { batchSetUpstreamQualityAccounts } from '@/api/admin/upstreamOps'

/** Bulk 上游变动监控 / 上游质量探测 for the selected accounts, with a status per row. */
const props = defineProps<{
  show: boolean
  kind: 'billing' | 'quality'
  accountIds: number[]
  accountNames?: Record<number, string>
}>()
const emit = defineEmits<{ close: []; done: [results: UpstreamProbeBatchResult[]] }>()
const { t } = useI18n()
const appStore = useAppStore()

const model = ref('')
const intervalMinutes = ref(60)
const running = ref(false)
const results = ref<UpstreamProbeBatchResult[]>([])

watch(() => props.show, (visible) => {
  if (visible) results.value = []
})

const title = computed(() => props.kind === 'billing' ? t('admin.accounts.upstreamProbeBulk.billingTitle') : t('admin.accounts.upstreamProbeBulk.qualityTitle'))

function accountLabel(id: number) {
  const name = props.accountNames?.[id]
  return name ? `${name} (#${id})` : `#${id}`
}

function statusClass(status: string) {
  if (status === 'failed') return 'text-red-600 dark:text-red-400'
  if (status === 'skipped_duplicate') return 'text-amber-600 dark:text-amber-400'
  return 'text-green-600 dark:text-green-400'
}

async function run(enabled: boolean) {
  if (running.value || !props.accountIds.length) return
  running.value = true
  try {
    results.value = props.kind === 'billing'
      ? await accountsAPI.batchSetUpstreamBillingProbeEnabled(props.accountIds, enabled)
      : await batchSetUpstreamQualityAccounts({ account_ids: props.accountIds, enabled, model: model.value || undefined, interval_minutes: intervalMinutes.value || undefined })
    const failed = results.value.filter((row) => row.status === 'failed').length
    if (failed) appStore.showWarning(t('admin.accounts.upstreamProbeBulk.partial', { failed, total: results.value.length }))
    else appStore.showSuccess(t('admin.accounts.upstreamProbeBulk.done', { total: results.value.length }))
    emit('done', results.value)
  } catch (err: unknown) {
    appStore.showError((err as { message?: string })?.message || t('admin.accounts.upstreamProbeBulk.failed'))
  } finally {
    running.value = false
  }
}
</script>
