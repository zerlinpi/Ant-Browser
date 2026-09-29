<template>
  <button
    ref="triggerRef"
    class="icon-button"
    type="button"
    :aria-label="label"
    :title="label"
    aria-haspopup="menu"
    :aria-expanded="open"
    :disabled="disabled"
    @click="toggle"
  >
    <Icon :icon="icon" />
  </button>
  <Teleport to="body">
    <div v-if="open" ref="panelRef" class="menu-panel menu-floating" role="menu" :aria-label="label" :style="panelStyle" @keydown="onKeydown">
      <button
        v-for="item in items"
        :key="item.key"
        class="menu-item"
        :class="{ danger: item.danger }"
        type="button"
        role="menuitem"
        :disabled="item.disabled"
        @click="choose(item)"
      >
        <Icon :icon="item.icon" />
        <span>{{ item.label }}<small v-if="item.hint">{{ item.hint }}</small></span>
      </button>
    </div>
  </Teleport>
</template>

<script lang="ts">
export interface ActionMenuItem {
  key: string;
  label: string;
  icon: string;
  danger?: boolean;
  disabled?: boolean;
  /** Short reason shown under the label, typically why the item is disabled. */
  hint?: string;
}
</script>

<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref } from "vue";
import { Icon } from "@iconify/vue";

withDefaults(defineProps<{ items: ActionMenuItem[]; label?: string; icon?: string; disabled?: boolean }>(), {
  label: "更多操作",
  icon: "lucide:ellipsis",
  disabled: false,
});
const emit = defineEmits<{ select: [key: string] }>();

const open = ref(false);
const triggerRef = ref<HTMLButtonElement>();
const panelRef = ref<HTMLDivElement>();
const panelStyle = ref<Record<string, string>>({ top: "-9999px", left: "-9999px" });

const enabledItems = () => Array.from(panelRef.value?.querySelectorAll<HTMLButtonElement>(".menu-item:not(:disabled)") ?? []);

const position = () => {
  const trigger = triggerRef.value;
  const panel = panelRef.value;
  if (!trigger || !panel) return;
  const rect = trigger.getBoundingClientRect();
  const { offsetHeight: height, offsetWidth: width } = panel;
  // Open upwards when the row sits near the bottom of the viewport.
  const top = window.innerHeight - rect.bottom < height + 12 && rect.top > height + 12 ? rect.top - height - 6 : rect.bottom + 6;
  const left = Math.max(8, Math.min(rect.right - width, window.innerWidth - width - 8));
  panelStyle.value = { top: `${top}px`, left: `${left}px` };
};

const onPointerDown = (event: MouseEvent) => {
  const target = event.target as Node;
  if (panelRef.value?.contains(target) || triggerRef.value?.contains(target)) return;
  close(false);
};
const onViewportChange = () => close(false);

const listen = (enabled: boolean) => {
  if (enabled) {
    document.addEventListener("mousedown", onPointerDown, true);
    window.addEventListener("scroll", onViewportChange, true);
    window.addEventListener("resize", onViewportChange);
  } else {
    document.removeEventListener("mousedown", onPointerDown, true);
    window.removeEventListener("scroll", onViewportChange, true);
    window.removeEventListener("resize", onViewportChange);
  }
};

const close = (restoreFocus = true) => {
  if (!open.value) return;
  open.value = false;
  listen(false);
  if (restoreFocus) triggerRef.value?.focus();
};

const toggle = async () => {
  if (open.value) {
    close();
    return;
  }
  panelStyle.value = { top: "-9999px", left: "-9999px" };
  open.value = true;
  await nextTick();
  position();
  listen(true);
  enabledItems()[0]?.focus();
};

const choose = (item: ActionMenuItem) => {
  if (item.disabled) return;
  close();
  emit("select", item.key);
};

const onKeydown = (event: KeyboardEvent) => {
  if (event.key === "Escape" || event.key === "Tab") {
    if (event.key === "Escape") event.preventDefault();
    close(event.key === "Escape");
    return;
  }
  if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
  event.preventDefault();
  const items = enabledItems();
  if (!items.length) return;
  const index = items.indexOf(document.activeElement as HTMLButtonElement);
  const next = event.key === "ArrowDown" ? (index + 1) % items.length : (index - 1 + items.length) % items.length;
  items[next].focus();
};

onBeforeUnmount(() => listen(false));
</script>
