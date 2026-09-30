<template>
  <ModalDialog :open="open && !!registration" :title="title" :description="summary" @close="requestClose">
    <template v-if="registration">
      <div class="alert alert-warning" role="alert"><Icon icon="lucide:triangle-alert" /><span>凭据只显示这一次，关闭后无法再次查看。遗失后只能重新轮换。</span></div>
      <div class="secret-list credential-list">
        <div>
          <span>设备 ID</span>
          <div class="secret-row">
            <code class="code-block">{{ registration.device.id }}</code>
            <button class="button button-secondary small" type="button" @click="copy('id', registration.device.id)">
              <Icon :icon="copied.id ? 'lucide:check' : 'lucide:copy'" />{{ copied.id ? "已复制" : "复制" }}
            </button>
          </div>
        </div>
        <div>
          <span>设备凭据</span>
          <div class="secret-row">
            <code class="code-block credential-value">{{ registration.credential }}</code>
            <button class="button button-secondary small" type="button" @click="copy('credential', registration.credential)">
              <Icon :icon="copied.credential ? 'lucide:check' : 'lucide:copy'" />{{ copied.credential ? "已复制" : "复制" }}
            </button>
          </div>
        </div>
      </div>
      <p class="form-note"><Icon icon="lucide:info" /><span>在桌面端代理中将该凭据配置为环境变量 <code>ANT_CLOUD_DEVICE_CREDENTIAL</code>，并填写上方设备 ID。</span></p>
      <div v-if="copyError" class="alert alert-danger credential-alert" role="alert"><Icon icon="lucide:circle-alert" /><span>{{ copyError }}</span></div>
      <div v-if="closeArmed" class="alert alert-danger credential-alert" role="alert"><Icon icon="lucide:circle-alert" /><span>尚未复制凭据，再次关闭后将无法找回。</span></div>
    </template>
    <template #footer>
      <button class="button" :class="closeArmed ? 'button-danger' : 'button-primary'" type="button" @click="requestClose">{{ closeArmed ? "仍然关闭" : "我已保存，关闭" }}</button>
    </template>
  </ModalDialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import { Icon } from "@iconify/vue";
import ModalDialog from "@/components/ModalDialog.vue";
import { devicePlatformLabel } from "@/components/device/deviceLabels";
import type { DeviceRegistration } from "@/types";
import { copyToClipboard } from "@/utils/format";

const props = defineProps<{ open: boolean; title: string; registration: DeviceRegistration | null; workspaceName?: string }>();
const emit = defineEmits<{ close: [] }>();

const copied = reactive({ id: false, credential: false });
const copyError = ref("");
const closeArmed = ref(false);

const summary = computed(() => {
  const device = props.registration?.device;
  if (!device) return "";
  return [device.name, devicePlatformLabel(device.platform), props.workspaceName].filter(Boolean).join(" · ");
});

// Every newly issued credential starts with a clean copy/close state.
watch(() => props.registration, () => {
  copied.id = false;
  copied.credential = false;
  copyError.value = "";
  closeArmed.value = false;
});

const copy = async (field: "id" | "credential", value: string) => {
  copyError.value = "";
  if (await copyToClipboard(value)) {
    copied[field] = true;
    if (field === "credential") closeArmed.value = false;
  } else {
    copyError.value = "无法访问剪贴板，请手动选中并复制";
  }
};

/** The first close without copying the credential only arms a warning. */
const requestClose = () => {
  if (!copied.credential && !closeArmed.value) {
    closeArmed.value = true;
    return;
  }
  emit("close");
};
</script>

<style scoped>
.credential-list{margin-top:16px}.credential-value{user-select:all}.credential-alert{margin-top:12px}.form-note code{font-family:"Cascadia Code",Consolas,monospace;font-weight:650;color:#3d4b61}
</style>
