<template>
  <span class="status-badge" :class="toneClass"><span class="status-dot" />{{ label }}</span>
</template>

<script setup lang="ts">
import { computed } from "vue";

const props = defineProps<{ status?: string; label?: string }>();
const normalized = computed(() => (props.status || "unknown").toLowerCase());
const toneClass = computed(() => {
  if (["online", "running", "active", "healthy", "success", "succeeded", "completed", "committed", "normal", "ready"].includes(normalized.value)) return "is-success";
  if (["queued", "pending", "waiting", "warning", "verifying", "leased", "starting", "stopping", "migrating", "syncing", "uploading", "open", "verification_required", "paused"].includes(normalized.value)) return "is-warning";
  if (["failed", "error", "frozen", "unhealthy", "revoked", "dead_letter", "conflict", "locked", "suspended", "risk", "unreachable"].includes(normalized.value)) return "is-danger";
  return "is-neutral";
});
const label = computed(() => props.label || props.status || "未知");
</script>
