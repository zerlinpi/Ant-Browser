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
          <h2>{{ mode === "login" ? "欢迎回来" : "创建团队账号" }}</h2>
          <p>{{ mode === "login" ? "登录后继续管理跨境业务环境。" : "从 Free 版开始创建你的第一个工作空间。" }}</p>
        </header>

        <div v-if="errorMessage" class="alert alert-danger"><Icon icon="lucide:circle-alert" />{{ errorMessage }}</div>

        <label v-if="mode === 'register'" class="field">
          <span>姓名</span>
          <input v-model.trim="displayName" autocomplete="name" placeholder="团队联系人" required />
        </label>
        <label class="field">
          <span>邮箱</span>
          <input v-model.trim="email" type="email" autocomplete="email" placeholder="name@company.com" required />
        </label>
        <label class="field">
          <span>密码</span>
          <input v-model="password" type="password" :autocomplete="mode === 'login' ? 'current-password' : 'new-password'" minlength="8" placeholder="至少 8 位" required />
        </label>

        <details class="advanced-settings">
          <summary>连接设置</summary>
          <label class="field">
            <span>Cloud API 地址</span>
            <input v-model.trim="apiURL" type="url" spellcheck="false" required />
          </label>
        </details>

        <button class="button button-primary button-block" :disabled="submitting" type="submit">
          <Icon v-if="submitting" icon="lucide:loader-circle" class="spin" />
          {{ submitting ? "正在连接…" : mode === "login" ? "登录" : "注册并继续" }}
        </button>
        <button class="login-switch" type="button" @click="mode = mode === 'login' ? 'register' : 'login'; errorMessage = ''">
          {{ mode === "login" ? "没有账号？创建团队账号" : "已有账号？返回登录" }}
        </button>
      </form>
    </section>
  </main>
</template>

<script setup lang="ts">
import { ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import { Icon } from "@iconify/vue";
import { ApiError } from "@/api/client";
import { useSessionStore } from "@/stores/session";
import logoURL from "../../../../frontend/src/resources/images/logo.png";

const route = useRoute();
const router = useRouter();
const session = useSessionStore();
const mode = ref<"login" | "register">("login");
const displayName = ref("");
const email = ref("");
const password = ref("");
const apiURL = ref(session.apiBaseURL);
const submitting = ref(false);
const errorMessage = ref("");

const submit = async () => {
  submitting.value = true;
  errorMessage.value = "";
  try {
    if (mode.value === "login") await session.login(email.value, password.value, apiURL.value);
    else await session.register(email.value, password.value, displayName.value, apiURL.value);
    await router.replace(typeof route.query.next === "string" ? route.query.next : "/");
  } catch (error) {
    errorMessage.value = error instanceof ApiError ? error.message : "无法连接 Cloud API，请检查地址与网络。";
  } finally {
    submitting.value = false;
  }
};
</script>
