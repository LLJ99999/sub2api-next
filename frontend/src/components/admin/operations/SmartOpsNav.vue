<template>
  <nav v-if="items.length > 1" :aria-label="groupLabel" class="mb-5 flex w-fit flex-wrap gap-1 rounded-xl border border-gray-200/70 bg-white/80 p-1 shadow-sm dark:border-dark-700 dark:bg-dark-900">
    <RouterLink v-for="item in items" :key="item.path" :to="item.path" v-slot="{ isActive }" class="rounded-lg px-4 py-2 text-sm transition-colors" :class="route.path === item.path ? 'bg-gray-900 font-medium text-white dark:bg-gray-100 dark:text-gray-900' : 'text-gray-500 hover:bg-gray-100 dark:hover:bg-dark-800'"><span :aria-current="isActive ? 'page' : undefined">{{ item.label }}</span></RouterLink>
  </nav>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { useAdminNavLayoutStore } from '@/stores/adminNavLayout'

// In-page tab strip: shows the other pages of the sidebar group this page sits
// in (as arranged in 系统设置 → 侧边栏布局). Ungrouped pages show no strip.
const route = useRoute()
const navLayoutStore = useAdminNavLayoutStore()
const group = computed(() => navLayoutStore.resolved.find(item => item.children?.some(child => child.path === route.path)))
const items = computed(() => group.value?.children ?? [])
const groupLabel = computed(() => group.value?.label ?? '')
</script>
