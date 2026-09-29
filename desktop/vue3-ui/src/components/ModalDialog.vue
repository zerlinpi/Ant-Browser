<template>
  <Teleport to="body">
    <div v-if="open" class="modal-backdrop" role="presentation" @mousedown.self="$emit('close')">
      <section class="modal-panel" :class="{ 'modal-panel-wide': wide }" role="dialog" aria-modal="true" :aria-labelledby="titleId">
        <header class="modal-header">
          <div>
            <h2 :id="titleId">{{ title }}</h2>
            <p v-if="description">{{ description }}</p>
          </div>
          <button class="icon-button" type="button" aria-label="关闭" @click="$emit('close')">
            <Icon icon="lucide:x" />
          </button>
        </header>
        <div class="modal-body"><slot /></div>
        <footer v-if="$slots.footer" class="modal-footer"><slot name="footer" /></footer>
      </section>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted } from "vue";
import { Icon } from "@iconify/vue";

const props = defineProps<{ open: boolean; title: string; description?: string; wide?: boolean }>();
const emit = defineEmits<{ close: [] }>();
const titleId = computed(() => `modal-${props.title.replace(/\s+/g, "-").toLowerCase()}`);
const onKeydown = (event: KeyboardEvent) => { if (event.key === "Escape" && props.open) emit("close"); };
onMounted(() => document.addEventListener("keydown", onKeydown));
onBeforeUnmount(() => document.removeEventListener("keydown", onKeydown));
</script>
