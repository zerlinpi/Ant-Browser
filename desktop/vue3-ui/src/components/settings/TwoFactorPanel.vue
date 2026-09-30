<template>
  <section class="panel settings-panel">
    <header class="panel-header">
      <div><h2>两步验证</h2><p>登录时除密码外，还需输入身份验证器应用生成的 6 位验证码。</p></div>
      <StatusBadge v-if="status" :status="status.enabled ? 'active' : 'inactive'" :label="status.enabled ? '已开启' : '未开启'" />
    </header>

    <div v-if="error" class="alert alert-danger mfa-alert" role="alert">
      <Icon icon="lucide:circle-alert" /><span>{{ error }}</span>
      <button class="button button-secondary small" type="button" :disabled="loading" @click="load">重试</button>
    </div>
    <div v-if="success" class="alert alert-success mfa-alert" role="status"><Icon icon="lucide:circle-check" /><span>{{ success }}</span></div>

    <div class="settings-form">
      <p v-if="!status && loading" class="panel-empty"><Icon icon="lucide:loader-circle" class="spin" /> 正在加载两步验证状态…</p>
      <template v-else-if="status && !status.enabled">
        <div class="settings-row">
          <div>
            <strong>身份验证器应用</strong>
            <p>支持 Google Authenticator、Microsoft Authenticator、1Password 等 TOTP 应用。</p>
          </div>
          <button class="button button-primary" type="button" :disabled="!status.available" @click="openEnroll">
            <Icon icon="lucide:shield-plus" />开启两步验证
          </button>
        </div>
        <p v-if="!status.available" class="form-note"><Icon icon="lucide:info" /><span>服务端尚未配置两步验证所需的加密密钥，暂时无法开启。</span></p>
      </template>
      <template v-else-if="status">
        <div class="settings-row">
          <div>
            <strong>身份验证器应用</strong>
            <p>{{ status.enabledAt ? `已于 ${formatFullDateTime(status.enabledAt)} 开启` : "已开启" }}，每次登录都需要输入验证码。</p>
          </div>
          <button class="button button-danger" type="button" @click="openDisable"><Icon icon="lucide:shield-off" />关闭两步验证</button>
        </div>
        <div class="settings-row">
          <div>
            <strong>恢复码</strong>
            <p>剩余 {{ status.recoveryCodesRemaining }} 个可用。手机丢失时可用恢复码登录，每个只能使用一次。</p>
          </div>
          <button class="button button-secondary" type="button" @click="openRegenerate"><Icon icon="lucide:refresh-cw" />重新生成恢复码</button>
        </div>
        <div v-if="status.recoveryCodesRemaining <= 3" class="alert" :class="status.recoveryCodesRemaining ? 'alert-warning' : 'alert-danger'" role="status">
          <Icon icon="lucide:triangle-alert" />
          <span>{{ status.recoveryCodesRemaining ? "恢复码即将用完，请重新生成一组并妥善保存。" : "恢复码已经用完。请立即重新生成，以免丢失手机后无法登录。" }}</span>
        </div>
      </template>
    </div>

    <ModalDialog :open="enroll.open" title="开启两步验证" :description="enrollDescription" :wide="enroll.step === 'scan'" @close="closeEnroll">
      <div v-if="enroll.error" class="alert alert-danger dialog-alert" role="alert"><Icon icon="lucide:circle-alert" /><span>{{ enroll.error }}</span></div>
      <form v-if="enroll.step === 'password'" id="mfa-enroll-password" class="dialog-form" @submit.prevent="startSetup">
        <label class="field">
          <span>当前密码</span>
          <input ref="enrollPasswordInput" v-model="enroll.password" type="password" autocomplete="current-password" required />
          <small>开启前需要再次确认密码。</small>
        </label>
      </form>
      <form v-else-if="setup" id="mfa-enroll-confirm" class="enroll-scan" @submit.prevent="confirmSetup">
        <div class="enroll-qr">
          <svg v-if="qr" class="qr-code" :viewBox="`0 0 ${qr.size} ${qr.size}`" role="img" aria-label="两步验证绑定二维码" shape-rendering="crispEdges">
            <rect :width="qr.size" :height="qr.size" fill="#fff" />
            <path :d="qr.path" fill="#111827" />
          </svg>
          <p v-else class="form-note"><Icon icon="lucide:info" /><span>无法生成二维码，请使用下方密钥手动添加。</span></p>
        </div>
        <div class="enroll-steps">
          <p class="enroll-step"><strong>1.</strong> 用身份验证器应用扫描二维码。</p>
          <div class="field">
            <span>无法扫码时，手动输入密钥</span>
            <div class="secret-row">
              <code class="code-block secret-value">{{ groupedSecret }}</code>
              <button class="button button-secondary small" type="button" @click="copySecret">
                <Icon :icon="secretCopied ? 'lucide:check' : 'lucide:copy'" />{{ secretCopied ? "已复制" : "复制" }}
              </button>
            </div>
            <small>账号：{{ setup.accountName }} · 基于时间 · {{ setup.digits }} 位 · 每 {{ setup.period }} 秒更新</small>
          </div>
          <label class="field">
            <span><strong>2.</strong> 输入应用中显示的 6 位验证码</span>
            <input ref="enrollCodeInput" v-model="enroll.code" class="mono" inputmode="numeric" autocomplete="one-time-code" maxlength="8" placeholder="6 位数字" required />
          </label>
        </div>
      </form>
      <template #footer>
        <button class="button button-secondary" type="button" :disabled="enroll.busy" @click="closeEnroll">取消</button>
        <button v-if="enroll.step === 'password'" class="button button-primary" type="submit" form="mfa-enroll-password" :disabled="enroll.busy || !enroll.password">
          <Icon v-if="enroll.busy" icon="lucide:loader-circle" class="spin" />{{ enroll.busy ? "处理中…" : "下一步" }}
        </button>
        <button v-else class="button button-primary" type="submit" form="mfa-enroll-confirm" :disabled="enroll.busy">
          <Icon v-if="enroll.busy" icon="lucide:loader-circle" class="spin" />{{ enroll.busy ? "验证中…" : "验证并开启" }}
        </button>
      </template>
    </ModalDialog>

    <ModalDialog :open="proof.open" :title="proofTitle" :description="proofDescription" @close="closeProof">
      <div v-if="proof.error" class="alert alert-danger dialog-alert" role="alert"><Icon icon="lucide:circle-alert" /><span>{{ proof.error }}</span></div>
      <form id="mfa-proof-form" class="dialog-form" @submit.prevent="submitProof">
        <label v-if="proof.action === 'disable'" class="field">
          <span>当前密码</span>
          <input ref="proofPasswordInput" v-model="proof.password" type="password" autocomplete="current-password" required />
        </label>
        <label v-if="!proof.useRecovery" class="field">
          <span>身份验证器验证码</span>
          <input ref="proofCodeInput" v-model="proof.code" class="mono" inputmode="numeric" autocomplete="one-time-code" maxlength="8" placeholder="6 位数字" required />
        </label>
        <label v-else class="field">
          <span>恢复码</span>
          <input v-model.trim="proof.recoveryCode" class="mono" autocomplete="off" spellcheck="false" maxlength="24" placeholder="xxxx-xxxx-xxxx-xxxx" required />
          <small>使用后该恢复码失效。</small>
        </label>
        <button class="login-switch proof-switch" type="button" :disabled="proof.busy" @click="proof.useRecovery = !proof.useRecovery">
          {{ proof.useRecovery ? "改用身份验证器验证码" : "无法使用身份验证器？改用恢复码" }}
        </button>
      </form>
      <template #footer>
        <button class="button button-secondary" type="button" :disabled="proof.busy" @click="closeProof">取消</button>
        <button class="button" :class="proof.action === 'disable' ? 'button-danger' : 'button-primary'" type="submit" form="mfa-proof-form" :disabled="proof.busy">
          <Icon v-if="proof.busy" icon="lucide:loader-circle" class="spin" />{{ proof.busy ? "处理中…" : proof.action === "disable" ? "关闭两步验证" : "重新生成" }}
        </button>
      </template>
    </ModalDialog>

    <RecoveryCodesDialog :open="recoveryCodes.length > 0" :codes="recoveryCodes" :account="session.user?.email" @close="recoveryCodes = []" />
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, reactive, ref } from "vue";
import { Icon } from "@iconify/vue";
import { api, ApiError, describeError } from "@/api/client";
import ModalDialog from "@/components/ModalDialog.vue";
import StatusBadge from "@/components/StatusBadge.vue";
import RecoveryCodesDialog from "@/components/settings/RecoveryCodesDialog.vue";
import { useSessionStore } from "@/stores/session";
import type { MFAStatus, SecondFactorProof, TOTPSetup } from "@/types";
import { copyToClipboard, formatFullDateTime } from "@/utils/format";
import { encodeQr, qrSvgPath } from "@/utils/qrcode";

const QR_BORDER = 4;

const session = useSessionStore();
const status = ref<MFAStatus | null>(null);
const loading = ref(false);
const error = ref("");
const success = ref("");
const recoveryCodes = ref<string[]>([]);

const load = async () => {
  loading.value = true;
  error.value = "";
  try {
    status.value = await api.get<MFAStatus>("/api/v1/me/mfa");
  } catch (err) {
    error.value = `两步验证状态加载失败：${describeError(err, "请稍后重试")}`;
  } finally {
    loading.value = false;
  }
};

// ---- Enrollment: password, then scan and confirm one code ----

const enroll = reactive({ open: false, step: "password" as "password" | "scan", password: "", code: "", busy: false, error: "" });
const setup = ref<TOTPSetup | null>(null);
const secretCopied = ref(false);
const enrollPasswordInput = ref<HTMLInputElement | null>(null);
const enrollCodeInput = ref<HTMLInputElement | null>(null);

const enrollDescription = computed(() => (enroll.step === "password" ? "第 1 步，共 2 步：确认身份" : "第 2 步，共 2 步：绑定身份验证器"));
const groupedSecret = computed(() => setup.value?.secret.match(/.{1,4}/g)?.join(" ") ?? "");
const qr = computed(() => {
  if (!setup.value) return null;
  try {
    const code = encodeQr(setup.value.otpauthUri, "M");
    return { size: code.size + QR_BORDER * 2, path: qrSvgPath(code, QR_BORDER) };
  } catch {
    return null;
  }
});

const openEnroll = async () => {
  success.value = "";
  Object.assign(enroll, { open: true, step: "password", password: "", code: "", busy: false, error: "" });
  setup.value = null;
  secretCopied.value = false;
  await nextTick();
  enrollPasswordInput.value?.focus();
};

const closeEnroll = () => {
  if (enroll.busy) return;
  enroll.open = false;
  enroll.password = "";
  enroll.code = "";
  // An unconfirmed setup changes nothing; the next attempt replaces it.
  setup.value = null;
};

const startSetup = async () => {
  if (enroll.busy || !enroll.password) return;
  enroll.busy = true;
  enroll.error = "";
  try {
    setup.value = await api.post<TOTPSetup>("/api/v1/me/mfa/totp/setup", { password: enroll.password });
    enroll.password = "";
    enroll.step = "scan";
    await nextTick();
    enrollCodeInput.value?.focus();
  } catch (err) {
    enroll.error = describeError(err, "无法开始绑定，请稍后重试");
    if (err instanceof ApiError && err.code === "mfa_already_enabled") void load();
  } finally {
    enroll.busy = false;
  }
};

const confirmSetup = async () => {
  const code = enroll.code.replace(/[\s-]/g, "");
  if (enroll.busy) return;
  if (!/^\d{6}$/.test(code)) {
    enroll.error = "请输入 6 位数字验证码";
    return;
  }
  enroll.busy = true;
  enroll.error = "";
  try {
    const result = await api.post<{ recoveryCodes: string[] }>("/api/v1/me/mfa/totp/confirm", { code });
    enroll.busy = false;
    closeEnroll();
    recoveryCodes.value = result.recoveryCodes ?? [];
    success.value = "两步验证已开启，下次登录时需要输入验证码";
    await load();
  } catch (err) {
    enroll.code = "";
    if (err instanceof ApiError && err.code === "mfa_setup_required") {
      // Another setup replaced this one (for example in a second window).
      setup.value = null;
      enroll.step = "password";
    }
    enroll.error = describeError(err, "验证失败，请稍后重试");
  } finally {
    enroll.busy = false;
  }
};

const copySecret = async () => {
  if (setup.value && (await copyToClipboard(setup.value.secret))) secretCopied.value = true;
};

// ---- Recovery code regeneration and removal: both prove the second factor ----

const proof = reactive({
  open: false, action: "regenerate" as "regenerate" | "disable", password: "", code: "", recoveryCode: "",
  useRecovery: false, busy: false, error: "",
});
const proofPasswordInput = ref<HTMLInputElement | null>(null);
const proofCodeInput = ref<HTMLInputElement | null>(null);
const proofTitle = computed(() => (proof.action === "disable" ? "关闭两步验证" : "重新生成恢复码"));
const proofDescription = computed(() => (proof.action === "disable"
  ? "关闭后登录只需要密码，账号安全性会降低。"
  : "生成新的一组恢复码，现有恢复码全部失效。"));

const openProof = async (action: "regenerate" | "disable") => {
  success.value = "";
  Object.assign(proof, { open: true, action, password: "", code: "", recoveryCode: "", useRecovery: false, busy: false, error: "" });
  await nextTick();
  (action === "disable" ? proofPasswordInput.value : proofCodeInput.value)?.focus();
};
const openRegenerate = () => openProof("regenerate");
const openDisable = () => openProof("disable");

const closeProof = () => {
  if (proof.busy) return;
  proof.open = false;
  proof.password = "";
  proof.code = "";
  proof.recoveryCode = "";
};

const submitProof = async () => {
  if (proof.busy) return;
  const factor: SecondFactorProof = proof.useRecovery
    ? { recoveryCode: proof.recoveryCode.trim() }
    : { code: proof.code.replace(/[\s-]/g, "") };
  if (proof.useRecovery ? !factor.recoveryCode : !/^\d{6}$/.test(factor.code ?? "")) {
    proof.error = proof.useRecovery ? "请输入恢复码" : "请输入 6 位数字验证码";
    return;
  }
  if (proof.action === "disable" && !proof.password) {
    proof.error = "请输入当前密码";
    return;
  }
  proof.busy = true;
  proof.error = "";
  try {
    if (proof.action === "disable") {
      await api.delete("/api/v1/me/mfa", { password: proof.password, ...factor });
      success.value = "两步验证已关闭";
      proof.busy = false;
      closeProof();
    } else {
      const result = await api.post<{ recoveryCodes: string[] }>("/api/v1/me/mfa/recovery-codes", factor);
      proof.busy = false;
      closeProof();
      recoveryCodes.value = result.recoveryCodes ?? [];
      success.value = "已生成新的恢复码，旧恢复码已失效";
    }
    await load();
  } catch (err) {
    proof.code = "";
    proof.error = describeError(err, "操作失败，请稍后重试");
    if (err instanceof ApiError && err.code === "mfa_not_enabled") void load();
  } finally {
    proof.busy = false;
  }
};

onMounted(() => void load());
</script>

<style scoped>
.mfa-alert {
  margin: 14px 20px 0;
}

.mfa-alert .button {
  margin-left: auto;
}

.dialog-alert {
  margin-bottom: 14px;
}

.dialog-form {
  display: grid;
  gap: 14px;
}

.enroll-scan {
  display: grid;
  grid-template-columns: 196px minmax(0, 1fr);
  gap: 20px;
  align-items: start;
}

.qr-code {
  display: block;
  width: 196px;
  height: 196px;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: #fff;
}

.enroll-steps {
  display: grid;
  gap: 14px;
}

.enroll-step {
  margin: 0;
  color: #3d4b61;
}

.secret-value {
  letter-spacing: 0.08em;
  user-select: all;
}

.proof-switch {
  justify-self: start;
}
</style>
