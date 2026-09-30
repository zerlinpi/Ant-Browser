<template>
  <main class="login-page">
    <section class="login-brand-panel">
      <div class="login-brand">
        <img :src="logoURL" alt="" />
        <span>Ant Browser</span>
      </div>
      <div class="login-copy">
        <p class="login-kicker">CROSS-BORDER OPERATIONS</p>
        <h1>让每个账号，都在可信环境中运行。</h1>
        <p>统一管理浏览器环境、账号资产、代理网络与自动化流程，让跨境团队协作更稳定、更清晰。</p>
      </div>
      <div class="login-security"><Icon icon="lucide:shield-check" /> 设备隔离 · 云端同步 · 权限审计</div>
    </section>

    <section class="login-form-panel">
      <form class="login-card" @submit.prevent="submit">
        <header>
          <p class="page-eyebrow">ANT BROWSER CLOUD</p>
          <h2>{{ heading }}</h2>
          <p>{{ subheading }}</p>
        </header>

        <div v-if="errorMessage" class="alert alert-danger" role="alert"><Icon icon="lucide:circle-alert" /><span>{{ errorMessage }}</span></div>

        <template v-if="step === 'credentials'">
          <label v-if="mode === 'register'" class="field">
            <span>姓名</span>
            <input v-model.trim="displayName" autocomplete="name" maxlength="100" placeholder="团队联系人" required />
          </label>
          <label class="field">
            <span>邮箱</span>
            <input v-model.trim="email" type="email" autocomplete="email" placeholder="name@company.com" required />
          </label>
          <label class="field">
            <span>密码</span>
            <input
              v-model="password"
              type="password"
              :autocomplete="mode === 'login' ? 'current-password' : 'new-password'"
              :minlength="mode === 'register' ? 12 : undefined"
              :placeholder="mode === 'register' ? '至少 12 位，包含字母和数字' : '请输入密码'"
              required
            />
          </label>

          <details class="advanced-settings">
            <summary>连接设置</summary>
            <label class="field">
              <span>Cloud API 地址</span>
              <input v-model.trim="apiURL" type="url" spellcheck="false" required />
            </label>
          </details>
        </template>

        <template v-else>
          <label v-if="!useRecovery" class="field">
            <span>验证码</span>
            <input
              ref="codeInput"
              v-model="mfaCode"
              class="mfa-code-input"
              inputmode="numeric"
              autocomplete="one-time-code"
              maxlength="8"
              placeholder="6 位数字"
              aria-describedby="mfa-code-hint"
              required
            />
            <small id="mfa-code-hint">验证码每 30 秒更新一次，输入 6 位数字后自动提交。</small>
          </label>
          <label v-else class="field">
            <span>恢复码</span>
            <input
              ref="recoveryInput"
              v-model.trim="recoveryCode"
              class="mono"
              autocomplete="off"
              spellcheck="false"
              maxlength="24"
              placeholder="xxxx-xxxx-xxxx-xxxx"
              required
            />
            <small>每个恢复码只能使用一次，用后请尽快重新生成一组。</small>
          </label>
        </template>

        <button class="button button-primary button-block" :disabled="submitting" type="submit">
          <Icon v-if="submitting" icon="lucide:loader-circle" class="spin" />
          {{ submitLabel }}
        </button>
        <template v-if="step === 'credentials'">
          <button class="login-switch" type="button" :disabled="submitting" @click="switchMode">
            {{ mode === "login" ? "没有账号？创建团队账号" : "已有账号？返回登录" }}
          </button>
        </template>
        <div v-else class="mfa-actions">
          <button class="login-switch" type="button" :disabled="submitting" @click="toggleRecovery">
            {{ useRecovery ? "改用身份验证器验证码" : "无法使用身份验证器？改用恢复码" }}
          </button>
          <button class="login-switch" type="button" :disabled="submitting" @click="backToCredentials()">返回重新登录</button>
        </div>
      </form>
    </section>
  </main>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { Icon } from "@iconify/vue";
import { ApiError, describeError } from "@/api/client";
import { useSessionStore } from "@/stores/session";
import type { MFAChallenge } from "@/types";
import logoURL from "../../../../frontend/src/resources/images/logo.png";

// Register-specific copy for authservice errors (ErrEmailExists, security.ErrWeakPassword).
const registerErrors: Record<string, string> = {
  resource_conflict: "该邮箱已注册，请直接登录",
  weak_password: "密码至少 12 位，且需同时包含字母和数字",
};

const route = useRoute();
const router = useRouter();
const session = useSessionStore();
const mode = ref<"login" | "register">("login");
const step = ref<"credentials" | "mfa">("credentials");
const displayName = ref("");
const email = ref("");
const password = ref("");
const apiURL = ref(session.apiBaseURL);
const submitting = ref(false);
const errorMessage = ref("");

const challenge = ref<MFAChallenge | null>(null);
const useRecovery = ref(false);
const mfaCode = ref("");
const recoveryCode = ref("");
const codeInput = ref<HTMLInputElement | null>(null);
const recoveryInput = ref<HTMLInputElement | null>(null);

const heading = computed(() => {
  if (step.value === "mfa") return "两步验证";
  return mode.value === "login" ? "欢迎回来" : "创建团队账号";
});
const subheading = computed(() => {
  if (step.value === "mfa") {
    return useRecovery.value ? "输入保存的恢复码完成登录。" : `打开身份验证器应用，输入 ${email.value} 当前的 6 位验证码。`;
  }
  return mode.value === "login" ? "登录后继续管理跨境业务环境。" : "从 Free 版开始创建你的第一个工作空间。";
});
const submitLabel = computed(() => {
  if (submitting.value) return step.value === "mfa" ? "正在验证…" : "正在连接…";
  if (step.value === "mfa") return "验证并登录";
  return mode.value === "login" ? "登录" : "注册并继续";
});

/** Honours ?next= only for in-app paths; "//host" and "/\host" would leave the app. */
const nextPath = () => {
  const next = route.query.next;
  return typeof next === "string" && next.startsWith("/") && !next.startsWith("//") && !next.startsWith("/\\") ? next : "/";
};

const switchMode = () => {
  mode.value = mode.value === "login" ? "register" : "login";
  errorMessage.value = "";
};

const focusSecondFactor = async () => {
  await nextTick();
  (useRecovery.value ? recoveryInput.value : codeInput.value)?.focus();
};

const enterSecondStep = async (pending: MFAChallenge) => {
  challenge.value = pending;
  // The password is no longer needed; an expired challenge requires signing in again.
  password.value = "";
  mfaCode.value = "";
  recoveryCode.value = "";
  useRecovery.value = false;
  step.value = "mfa";
  await focusSecondFactor();
};

const backToCredentials = (message = "") => {
  challenge.value = null;
  mfaCode.value = "";
  recoveryCode.value = "";
  useRecovery.value = false;
  step.value = "credentials";
  errorMessage.value = message;
};

const toggleRecovery = async () => {
  useRecovery.value = !useRecovery.value;
  errorMessage.value = "";
  await focusSecondFactor();
};

const verify = async () => {
  const token = challenge.value?.token;
  if (!token) {
    backToCredentials("登录验证已过期，请重新登录");
    return;
  }
  const code = mfaCode.value.replace(/[\s-]/g, "");
  const recovery = recoveryCode.value.trim();
  if (useRecovery.value ? !recovery : !/^\d{6}$/.test(code)) {
    errorMessage.value = useRecovery.value ? "请输入恢复码" : "请输入 6 位数字验证码";
    return;
  }
  submitting.value = true;
  errorMessage.value = "";
  try {
    await session.verifyMFA(token, useRecovery.value ? { recoveryCode: recovery } : { code });
    await router.replace(nextPath());
  } catch (error) {
    if (error instanceof ApiError && error.code === "mfa_challenge_invalid") {
      backToCredentials(describeError(error, "登录验证已过期，请重新登录"));
      return;
    }
    errorMessage.value = describeError(error, "验证失败，请稍后重试");
    if (!useRecovery.value) mfaCode.value = "";
    await focusSecondFactor();
  } finally {
    submitting.value = false;
  }
};

// Authenticator codes are submitted as soon as six digits are entered.
watch(mfaCode, (value) => {
  if (step.value === "mfa" && !useRecovery.value && !submitting.value && /^\d{6}$/.test(value.replace(/[\s-]/g, ""))) void verify();
});

const submit = async () => {
  if (submitting.value) return;
  if (step.value === "mfa") {
    await verify();
    return;
  }
  const registering = mode.value === "register";
  submitting.value = true;
  errorMessage.value = "";
  try {
    if (registering) {
      await session.register(email.value, password.value, displayName.value, apiURL.value);
    } else {
      const pending = await session.login(email.value, password.value, apiURL.value);
      if (pending) {
        await enterSecondStep(pending);
        return;
      }
    }
    await router.replace(nextPath());
  } catch (error) {
    // describeError localizes stable codes and appends the Retry-After wait for 429 responses.
    errorMessage.value = registering
      ? describeError(error, "注册失败，请稍后重试", registerErrors)
      : describeError(error, "登录失败，请稍后重试");
  } finally {
    submitting.value = false;
  }
};
</script>

<style scoped>
.mfa-code-input {
  font-family: "Cascadia Code", Consolas, monospace;
  font-size: 20px;
  letter-spacing: 0.3em;
  text-align: center;
}

.mfa-code-input::placeholder {
  font-family: inherit;
  font-size: 14px;
  letter-spacing: normal;
}

.mfa-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: 8px;
}
</style>
