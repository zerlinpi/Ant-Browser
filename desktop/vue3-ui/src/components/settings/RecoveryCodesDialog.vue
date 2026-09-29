<template>
  <ModalDialog :open="open" title="保存恢复码" description="手机丢失或无法使用身份验证器时，用恢复码登录。" @close="requestClose">
    <div class="alert alert-warning" role="alert">
      <Icon icon="lucide:triangle-alert" /><span>恢复码只显示这一次。每个恢复码只能使用一次，之前的恢复码已全部失效。</span>
    </div>
    <ol class="recovery-code-grid" aria-label="恢复码">
      <li v-for="code in codes" :key="code" class="mono">{{ code }}</li>
    </ol>
    <div class="recovery-actions">
      <button class="button button-secondary small" type="button" @click="copyAll">
        <Icon :icon="copied ? 'lucide:check' : 'lucide:copy'" />{{ copied ? "已复制" : "复制全部" }}
      </button>
      <button class="button button-secondary small" type="button" @click="download">
        <Icon :icon="downloaded ? 'lucide:check' : 'lucide:download'" />{{ downloaded ? "已下载" : "下载 .txt" }}
      </button>
    </div>
    <div v-if="copyError" class="alert alert-danger recovery-alert" role="alert"><Icon icon="lucide:circle-alert" /><span>{{ copyError }}</span></div>
    <div v-if="closeArmed" class="alert alert-danger recovery-alert" role="alert"><Icon icon="lucide:circle-alert" /><span>还没有复制或下载恢复码，关闭后将无法再次查看。</span></div>
    <template #footer>
      <button class="button" :class="closeArmed ? 'button-danger' : 'button-primary'" type="button" @click="requestClose">{{ closeArmed ? "仍然关闭" : "我已保存，关闭" }}</button>
    </template>
  </ModalDialog>
</template>

<script setup lang="ts">
import { ref, watch } from "vue";
import { Icon } from "@iconify/vue";
import ModalDialog from "@/components/ModalDialog.vue";
import { copyToClipboard } from "@/utils/format";

const props = defineProps<{ open: boolean; codes: string[]; account?: string }>();
const emit = defineEmits<{ close: [] }>();

const copied = ref(false);
const downloaded = ref(false);
const copyError = ref("");
const closeArmed = ref(false);

// A new set of codes starts with a clean save state.
watch(() => props.codes, () => {
  copied.value = false;
  downloaded.value = false;
  copyError.value = "";
  closeArmed.value = false;
});

const copyAll = async () => {
  copyError.value = "";
  if (await copyToClipboard(props.codes.join("\n"))) {
    copied.value = true;
    closeArmed.value = false;
  } else {
    copyError.value = "无法访问剪贴板，请手动抄写或下载恢复码";
  }
};

const download = () => {
  const lines = [
    "Ant Browser 两步验证恢复码",
    props.account ? `账号：${props.account}` : "",
    `生成时间：${new Date().toLocaleString("zh-CN")}`,
    "",
    ...props.codes,
    "",
    "每个恢复码只能使用一次。重新生成后，旧的恢复码全部失效。",
  ].filter((line, index) => line || index > 2);
  const url = URL.createObjectURL(new Blob([lines.join("\r\n")], { type: "text/plain;charset=utf-8" }));
  const link = document.createElement("a");
  link.href = url;
  link.download = "ant-browser-recovery-codes.txt";
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
  downloaded.value = true;
  closeArmed.value = false;
};

/** The first close before the codes were copied or downloaded only arms a warning. */
const requestClose = () => {
  if (!copied.value && !downloaded.value && !closeArmed.value) {
    closeArmed.value = true;
    return;
  }
  emit("close");
};
</script>

<style scoped>
.recovery-code-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px 16px;
  margin: 16px 0 0;
  padding: 14px 16px;
  list-style: none;
  background: #f2f5f8;
  border-radius: 8px;
  font-size: 14px;
  user-select: all;
}

.recovery-actions {
  display: flex;
  gap: 8px;
  margin-top: 12px;
}

.recovery-alert {
  margin-top: 12px;
}
</style>
