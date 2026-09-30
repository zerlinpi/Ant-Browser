<template>
  <span class="risk-badge" :class="`risk-${normalized}`">{{ riskLabel(normalized) }}</span>
</template>

<script setup lang="ts">
import { computed } from "vue";
import { riskLabel } from "@/components/account/accountMeta";

const props = defineProps<{ level?: string | null }>();
const normalized = computed(() => (props.level || "unknown").toLowerCase());
</script>

<style scoped>
/* styles.css colours medium/high/critical; low reads as healthy, info is informational, unknown stays neutral. */
.risk-low {
  background: #eaf9f1;
  color: var(--green);
}
.risk-info {
  background: #eef4ff;
  color: var(--blue);
}
</style>
