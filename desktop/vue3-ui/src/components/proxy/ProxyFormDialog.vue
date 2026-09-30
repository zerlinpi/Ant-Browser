<template>
  <ModalDialog :open="open" :title="isEdit ? '编辑代理' : '添加代理'" :description="isEdit ? '保存后，后续启动与检查按新配置执行。' : '节点绑定的连接栈负责启动、检查、预热和下载。'" @close="close">
    <div v-if="error" class="alert alert-danger proxy-form-alert" role="alert"><Icon icon="lucide:circle-alert" /><span>{{ error }}</span></div>
    <div v-if="refreshWarning" class="alert alert-warning proxy-form-alert" role="status"><Icon icon="lucide:triangle-alert" /><span>{{ refreshWarning }}</span></div>
    <div class="form-grid">
      <label class="field field-span"><span>名称</span><input v-model="form.name" maxlength="120" placeholder="例如：US-LA Residential 01" :disabled="busy" /></label>
      <label class="field">
        <span>连接栈</span>
        <select v-model="form.connectorType" :disabled="busy">
          <option value="xray">xray 组合栈（Xray + sing-box）</option>
          <option value="mihomo">mihomo 独立栈</option>
        </select>
      </label>
      <label class="field">
        <span>协议</span>
        <select v-model="form.protocol" :disabled="busy">
          <option value="" disabled>请选择协议</option>
          <optgroup v-for="group in groups" :key="group.label" :label="group.label">
            <option v-for="item in group.protocols" :key="item" :value="item">{{ protocolLabel(item) }}</option>
          </optgroup>
        </select>
      </label>
      <template v-if="!isDirect">
        <label class="field"><span>服务器</span><input v-model="form.host" maxlength="255" placeholder="proxy.example.com" autocomplete="off" :disabled="busy" /></label>
        <label class="field"><span>端口</span><input v-model.number="form.port" type="number" min="1" max="65535" placeholder="1080" :disabled="busy" /></label>
        <label class="field"><span>用户名（可选）</span><input v-model="form.username" autocomplete="off" :disabled="busy" /></label>
        <label class="field">
          <span>{{ baseline?.hasCredentials ? "新密码（留空不变）" : "密码（可选）" }}</span>
          <input v-model="form.password" type="password" autocomplete="new-password" :disabled="busy || form.clearSecret" />
        </label>
      </template>
      <label v-if="isEdit" class="field">
        <span>状态</span>
        <select v-model="form.status" :disabled="busy"><option value="active">启用</option><option value="disabled">停用</option></select>
      </label>
      <label v-if="baseline?.hasCredentials && !isDirect" class="checkbox-field proxy-form-check"><input v-model="form.clearSecret" type="checkbox" :disabled="busy" />清除已保存的密码</label>
    </div>
    <p v-if="stackHint" class="form-note proxy-form-warning"><Icon icon="lucide:triangle-alert" /><span>{{ stackHint }}</span></p>
    <p class="form-note"><Icon icon="lucide:cpu" /><span>{{ executorNote }}</span></p>
    <template #footer>
      <button class="button button-secondary" type="button" :disabled="saving" @click="close">取消</button>
      <button class="button button-primary" type="button" :disabled="busy" @click="submit">
        <Icon v-if="busy" icon="lucide:loader-circle" class="spin" />{{ refreshing ? "加载中…" : saving ? "保存中…" : isEdit ? "保存" : "添加并检查" }}
      </button>
    </template>
  </ModalDialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import { Icon } from "@iconify/vue";
import { api, ApiError, describeError } from "@/api/client";
import ModalDialog from "@/components/ModalDialog.vue";
import { useSessionStore } from "@/stores/session";
import type { ProxyConnectorType, ProxyNode } from "@/types";
import { executorLabel, normalizeStack, protocolGroups, protocolLabel, resolveKernel, stackLabel, supportsProtocol } from "./protocols";

interface ProxyForm {
  name: string;
  connectorType: ProxyConnectorType;
  protocol: string;
  host: string;
  /** v-model.number yields "" for an empty input. */
  port: number | "";
  username: string;
  password: string;
  status: "active" | "disabled";
  clearSecret: boolean;
}

const props = defineProps<{ open: boolean; proxy?: ProxyNode | null }>();
const emit = defineEmits<{ close: []; saved: [proxy: ProxyNode] }>();

const session = useSessionStore();
/** The server copy the form was filled from; its version is sent with PATCH. */
const baseline = ref<ProxyNode | null>(null);
const saving = ref(false);
const refreshing = ref(false);
const error = ref("");
const refreshWarning = ref("");
const stackHint = ref("");
const form = reactive<ProxyForm>({ name: "", connectorType: "xray", protocol: "", host: "", port: "", username: "", password: "", status: "active", clearSecret: false });
let openToken = 0;

const isEdit = computed(() => Boolean(props.proxy));
const busy = computed(() => saving.value || refreshing.value);
const isDirect = computed(() => form.protocol === "direct");
const groups = computed(() => protocolGroups[form.connectorType]);
// Mirrors the server: a username or any stored/new secret makes the proxy authenticated.
const authenticated = computed(() => Boolean(form.username.trim() || form.password || (baseline.value?.hasCredentials && !form.clearSecret)));
const executorNote = computed(() => {
  const stack = form.connectorType;
  if (!form.protocol) return "请选择该连接栈支持的协议。";
  if (isDirect.value) return "直连不经过代理，也无需连接器进程。";
  const kernel = resolveKernel(stack, form.protocol, authenticated.value);
  if (!kernel) return `${stackLabel(stack)}不支持「${protocolLabel(form.protocol)}」。`;
  if (kernel === "direct") return `未认证的 ${protocolLabel(form.protocol)} 代理由浏览器原生连接，无需连接器进程；填写认证信息后由 ${executorLabel(stack, stack)} 桥接。`;
  return `由 ${executorLabel(stack, kernel)} 执行，不会自动改用另一套连接栈。`;
});

const fill = (proxy: ProxyNode | null) => {
  const next: ProxyForm = proxy
    ? {
        name: proxy.name,
        connectorType: normalizeStack(proxy.connectorType),
        protocol: proxy.protocol,
        host: proxy.host ?? "",
        port: proxy.port || "",
        username: proxy.username ?? "",
        password: "",
        // The worker toggles active/unhealthy; only "disabled" is an operator choice.
        status: proxy.status === "disabled" ? "disabled" : "active",
        clearSecret: false,
      }
    : { name: "", connectorType: "xray", protocol: "", host: "", port: "", username: "", password: "", status: "active", clearSecret: false };
  Object.assign(form, next);
  baseline.value = proxy;
};

const fetchProxy = (id: string) => api.get<ProxyNode>(`${session.workspaceBase}/proxies/${encodeURIComponent(id)}`);

watch(() => props.open, async (open) => {
  if (!open) return;
  const token = ++openToken;
  error.value = "";
  refreshWarning.value = "";
  stackHint.value = "";
  fill(props.proxy ?? null);
  if (!props.proxy || !session.workspaceBase) return;
  // Completed health checks bump the version, so edit the current server copy.
  refreshing.value = true;
  try {
    const fresh = await fetchProxy(props.proxy.id);
    if (token === openToken) fill(fresh);
  } catch (err) {
    if (token === openToken) refreshWarning.value = `未能获取最新配置，当前显示列表数据：${describeError(err, "请稍后重试")}`;
  } finally {
    if (token === openToken) refreshing.value = false;
  }
}, { immediate: true });

// Changing the stack never switches it back or picks another protocol silently:
// an unsupported protocol is cleared and must be chosen again.
watch(() => form.connectorType, (stack) => {
  if (!form.protocol || supportsProtocol(stack, form.protocol)) return;
  stackHint.value = `${stackLabel(stack)}不支持「${protocolLabel(form.protocol)}」，请重新选择协议。`;
  form.protocol = "";
});
watch(() => form.protocol, (protocol) => { if (protocol) stackHint.value = ""; });

const validate = () => {
  const name = form.name.trim();
  if (!name) return "请输入代理名称";
  if ([...name].length > 120) return "名称不能超过 120 个字符";
  if (!form.protocol) return "请选择协议";
  if (!supportsProtocol(form.connectorType, form.protocol)) return `${stackLabel(form.connectorType)}不支持该协议`;
  if (isDirect.value) return "";
  const host = form.host.trim();
  if (!host) return "请输入服务器地址";
  if (host.length > 255) return "服务器地址不能超过 255 个字符";
  const port = Number(form.port);
  if (!Number.isInteger(port) || port < 1 || port > 65535) return "端口需在 1–65535 之间";
  return "";
};

/** Fields an operator edits; the worker only changes status (active/unhealthy) and version. */
const sameConfig = (a: ProxyNode, b: ProxyNode) =>
  a.name === b.name && a.protocol === b.protocol && a.host === b.host && a.port === b.port
  && (a.username ?? "") === (b.username ?? "") && normalizeStack(a.connectorType) === normalizeStack(b.connectorType)
  && a.hasCredentials === b.hasCredentials && (a.status === "disabled") === (b.status === "disabled");

const close = () => {
  if (!saving.value) emit("close");
};

const submit = async () => {
  if (busy.value) return;
  const problem = validate();
  if (problem) {
    error.value = problem;
    return;
  }
  const base = session.workspaceBase;
  if (!base) {
    error.value = "请先选择工作空间";
    return;
  }
  const current = baseline.value;
  const direct = isDirect.value;
  const fields = {
    name: form.name.trim(),
    protocol: form.protocol,
    host: direct ? "" : form.host.trim(),
    port: direct ? 0 : Number(form.port),
    username: direct ? "" : form.username.trim(),
    connectorType: form.connectorType,
  };
  const password = !direct && form.password ? form.password : "";
  saving.value = true;
  error.value = "";
  try {
    if (current) {
      // PATCH replaces every field (an empty status would re-enable the proxy),
      // so unchanged values are resent. Omitting `secret` keeps the stored one;
      // a direct proxy drops stored credentials instead of keeping them hidden.
      const secret = form.clearSecret || (direct && current.hasCredentials) ? { clear: true } : password ? { password } : undefined;
      const patch = (version: number) => api.patch<ProxyNode>(`${base}/proxies/${encodeURIComponent(current.id)}`, {
        ...fields,
        status: form.status,
        version,
        ...(secret ? { secret } : {}),
      });
      let updated: ProxyNode;
      try {
        updated = await patch(current.version);
      } catch (err) {
        if (!(err instanceof ApiError && err.code === "version_conflict")) throw err;
        const fresh = await fetchProxy(current.id);
        if (!sameConfig(current, fresh)) {
          // Someone else changed the configuration: keep the form, adopt their version.
          baseline.value = fresh;
          error.value = "代理已被他人修改；再次保存将以当前表单覆盖。";
          return;
        }
        // Only a health check touched the proxy (status/version): save on top of it.
        updated = await patch(fresh.version);
      }
      emit("saved", updated);
    } else {
      const created = await api.post<ProxyNode>(`${base}/proxies`, { ...fields, ...(password ? { secret: { password } } : {}) });
      emit("saved", created);
    }
  } catch (err) {
    error.value = `${current ? "代理更新失败" : "代理创建失败"}：${describeError(err, "请稍后重试")}`;
  } finally {
    saving.value = false;
  }
};
</script>

<style scoped>
.proxy-form-alert{margin-bottom:15px}
.proxy-form-check{align-self:end;min-height:39px}
.proxy-form-warning{background:#fff7e8;color:#8a5a00}
.form-note+.form-note{margin-top:8px}
</style>
