<template>
  <div class="empty-state">
    <span class="empty-state-icon"><Icon :icon="icon" /></span>
    <h3>{{ title }}</h3>
    <p>{{ description }}</p>
    <slot />
  </div>
</template>

<script setup lang="ts">
import { Icon } from "@iconify/vue";
withDefaults(defineProps<{ icon?: string; title: string; description: string }>(), { icon: "lucide:inbox" });
</script>
