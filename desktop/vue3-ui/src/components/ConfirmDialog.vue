<template>
  <ModalDialog :open="open" :title="title" @close="$emit('cancel')">
    <p class="confirm-message">{{ message }}</p>
    <div v-if="error" class="alert alert-danger" role="alert"><Icon icon="lucide:circle-alert" />{{ error }}</div>
    <template #footer>
      <button class="button button-secondary" type="button" :disabled="busy" @click="$emit('cancel')">取消</button>
      <button class="button" :class="danger ? 'button-danger' : 'button-primary'" type="button" :disabled="busy" @click="$emit('confirm')">
        <Icon v-if="busy" icon="lucide:loader-circle" class="spin" />{{ busy ? "处理中…" : confirmText }}
      </button>
    </template>
  </ModalDialog>
</template>

<script setup lang="ts">
import { Icon } from "@iconify/vue";
import ModalDialog from "@/components/ModalDialog.vue";

withDefaults(defineProps<{
  open: boolean;
  title: string;
  message: string;
  confirmText?: string;
  danger?: boolean;
  busy?: boolean;
  error?: string;
}>(), { confirmText: "确认", danger: false, busy: false, error: "" });
defineEmits<{ confirm: []; cancel: [] }>();
</script>
