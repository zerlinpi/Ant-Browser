<template>
  <div class="page-stack fingerprint-editor-page">
    <PageHeader eyebrow="FINGERPRINT / TEMPLATE" :title="isNew ? '新建指纹模板' : form.name || '编辑指纹模板'" description="逐项核对系统、地区、硬件与高熵浏览器信号；保存后由桌面运行时按模板执行。">
      <RouterLink class="button button-secondary" :to="{ name: 'fingerprints' }"><Icon icon="lucide:arrow-left" />返回</RouterLink>
      <button class="button button-primary" type="button" :disabled="saving || loading" @click="save"><Icon :class="{ spin: saving }" :icon="saving ? 'lucide:loader-circle' : 'lucide:save'" />{{ saving ? '保存中…' : isNew ? '创建模板' : '保存修改' }}</button>
    </PageHeader>

    <div v-if="errorMessage" class="alert alert-danger" role="alert"><Icon icon="lucide:circle-alert" />{{ errorMessage }}</div>
    <div v-if="successMessage" class="editor-success" role="status"><Icon icon="lucide:circle-check" />{{ successMessage }}</div>
    <article v-if="loading" class="panel editor-loading"><Icon class="spin" icon="lucide:loader-circle" />正在加载指纹配置…</article>

    <section v-else class="fingerprint-editor-layout">
      <main class="editor-form-stack">
        <article v-if="isNew && presets.length" class="panel preset-chooser">
          <div><span class="section-icon"><Icon icon="lucide:sparkles" /></span><div><strong>从场景预设开始</strong><p>载入完整设备参数后仍可逐项调整。</p></div></div>
          <select v-model="selectedPresetKey" aria-label="选择场景预设"><option value="">不使用预设</option><option v-for="preset in presets" :key="preset.key" :value="preset.key">{{ preset.name }}</option></select>
          <button class="button button-secondary" type="button" :disabled="!selectedPresetKey" @click="applySelectedPreset">载入预设</button>
        </article>

        <article class="panel editor-section">
          <header class="editor-section-heading"><span class="section-icon"><Icon icon="lucide:id-card" /></span><div><h2>身份基础</h2><p>种子、浏览器版本、操作系统和地区必须形成稳定组合。</p></div></header>
          <div class="editor-fields three-columns">
            <label class="field field-span"><span>模板名称</span><input v-model.trim="form.name" maxlength="120" placeholder="例如：Amazon US · Windows 11" /></label>
            <label class="field"><span>生成模式</span><select v-model="form.mode"><option value="seeded">随机种子</option><option value="fixed">固定种子</option><option value="custom">自定义</option></select><small>{{ modeDescription }}</small></label>
            <label class="field seed-field"><span>指纹种子</span><div><input v-model.trim="form.seed" inputmode="numeric" :placeholder="isNew && form.mode !== 'fixed' ? '保存时安全生成' : '请输入正整数'" /><button class="icon-button" type="button" title="生成新种子" aria-label="生成新种子" @click="generateSeed"><Icon icon="lucide:dices" /></button></div><small>同一种子可复现同一套稳定身份。</small></label>
            <label class="field"><span>Chromium 主版本</span><input v-model.number="form.browserMajor" type="number" min="120" max="250" /><small>必须与运行设备上的 Chromium 主版本一致。</small></label>
            <label class="field"><span>操作系统</span><select v-model="form.platform"><option value="windows">Windows</option><option value="macos">macOS</option><option value="linux">Linux</option></select></label>
            <label class="field"><span>系统版本</span><input v-model.trim="form.configuration.platformVersion" maxlength="64" placeholder="例如 10.0.0" /></label>
            <label class="field"><span>语言地区</span><input v-model.trim="form.locale" maxlength="32" placeholder="en-US" /></label>
            <label class="field"><span>IANA 时区</span><input v-model.trim="form.timezone" maxlength="80" placeholder="America/Los_Angeles" /></label>
          </div>
        </article>

        <article class="panel editor-section">
          <header class="editor-section-heading"><span class="section-icon violet"><Icon icon="lucide:monitor-smartphone" /></span><div><h2>显示与设备</h2><p>窗口、屏幕、像素比和硬件能力需符合真实设备常见组合。</p></div></header>
          <div class="editor-fields four-columns">
            <label class="field"><span>窗口宽度</span><input v-model.number="form.configuration.windowWidth" type="number" min="800" max="7680" /></label>
            <label class="field"><span>窗口高度</span><input v-model.number="form.configuration.windowHeight" type="number" min="600" max="4320" /></label>
            <label class="field"><span>屏幕宽度</span><input v-model.number="form.configuration.screenWidth" type="number" min="800" max="15360" /></label>
            <label class="field"><span>屏幕高度</span><input v-model.number="form.configuration.screenHeight" type="number" min="600" max="8640" /></label>
            <label class="field"><span>设备像素比</span><input v-model.number="form.configuration.deviceScaleFactor" type="number" min="0.5" max="4" step="0.25" /></label>
            <label class="field"><span>CPU 逻辑核心</span><input v-model.number="form.configuration.hardwareConcurrency" type="number" min="1" max="128" /></label>
            <label class="field"><span>设备内存</span><select v-model.number="form.configuration.deviceMemory"><option :value="0">由浏览器决定</option><option :value="1">1 GiB</option><option :value="2">2 GiB</option><option :value="4">4 GiB</option><option :value="8">8 GiB</option></select></label>
            <label class="field"><span>色深</span><select v-model.number="form.configuration.colorDepth"><option :value="0">由浏览器决定</option><option :value="16">16 bit</option><option :value="24">24 bit</option><option :value="30">30 bit</option><option :value="32">32 bit</option></select></label>
            <div class="field"><span>最大触控点</span><input v-model.number="form.configuration.maxTouchPoints" type="number" min="0" max="20" :disabled="!touchPointsEnabled" /><label class="field-option"><input v-model="touchPointsEnabled" type="checkbox" />覆盖浏览器值</label></div>
          </div>
        </article>

        <article class="panel editor-section">
          <header class="editor-section-heading"><span class="section-icon green"><Icon icon="lucide:shield-check" /></span><div><h2>网络与高熵信号</h2><p>限制 WebRTC 泄漏，并以确定性扰动隔离常见采集表面。</p></div></header>
          <div class="editor-fields two-columns compact-fields">
            <label class="field"><span>WebRTC 地址策略</span><select v-model="form.configuration.webrtcPolicy"><option value="default">浏览器默认</option><option value="default_public_and_private_interfaces">公网与私网接口</option><option value="default_public_interface_only">仅默认公网接口</option><option value="disable_non_proxied_udp">禁用非代理 UDP</option></select></label>
            <label class="field"><span>Do Not Track</span><select v-model="form.configuration.doNotTrack"><option value="">保留浏览器实现</option><option value="unspecified">未指定</option><option value="1">开启（1）</option><option value="0">关闭（0）</option></select></label>
          </div>
          <div class="noise-grid">
            <label class="check-tile" :class="{ active: form.configuration.canvasNoise }"><input v-model="form.configuration.canvasNoise" type="checkbox" /><span><Icon icon="lucide:image" /><strong>Canvas 扰动</strong><small>稳定改变像素读回</small></span></label>
            <label class="check-tile" :class="{ active: form.configuration.audioNoise }"><input v-model="form.configuration.audioNoise" type="checkbox" /><span><Icon icon="lucide:audio-waveform" /><strong>Audio 扰动</strong><small>稳定改变音频采样</small></span></label>
            <label class="check-tile" :class="{ active: form.configuration.clientRectsNoise }"><input v-model="form.configuration.clientRectsNoise" type="checkbox" /><span><Icon icon="lucide:scan" /><strong>Client Rects 扰动</strong><small>稳定改变几何测量</small></span></label>
          </div>
          <div class="editor-fields two-columns signal-fields">
            <label class="field"><span>WebGL 厂商</span><input v-model.trim="form.configuration.webglVendor" maxlength="160" placeholder="Google Inc. (Intel)" /></label>
            <label class="field"><span>WebGL 渲染器</span><input v-model.trim="form.configuration.webglRenderer" maxlength="256" placeholder="ANGLE (Intel, Intel Iris Xe Graphics, D3D11)" /></label>
          </div>
        </article>

        <article class="panel editor-section">
          <header class="editor-section-heading"><span class="section-icon orange"><Icon icon="lucide:settings-2" /></span><div><h2>字体、媒体与电池</h2><p>显式维护页面可观察的设备清单；关闭开关即可保留主机实现。</p></div></header>
          <div class="editor-fields">
            <label class="field"><span>可用字体</span><textarea v-model="fontsText" rows="4" maxlength="12928" placeholder="每行一个字体，例如：&#10;Arial&#10;Segoe UI&#10;Times New Roman"></textarea><small>{{ parsedFonts.length }}/128 项；重复名称会自动合并。</small></label>
          </div>
          <div class="surface-block">
            <label class="surface-toggle"><input v-model="mediaEnabled" type="checkbox" /><span><strong>固定媒体设备清单</strong><small>以种子派生稳定的设备标识，不保存真实硬件 ID。</small></span></label>
            <div v-if="mediaEnabled" class="editor-fields three-columns surface-fields">
              <label class="field"><span>音频输入</span><input v-model.number="media.audioInputs" type="number" min="0" max="16" /></label>
              <label class="field"><span>视频输入</span><input v-model.number="media.videoInputs" type="number" min="0" max="16" /></label>
              <label class="field"><span>音频输出</span><input v-model.number="media.audioOutputs" type="number" min="0" max="16" /></label>
            </div>
          </div>
          <div class="surface-block">
            <label class="surface-toggle"><input v-model="batteryEnabled" type="checkbox" /><span><strong>固定电池状态</strong><small>覆盖 navigator.getBattery 返回的页面可见数据。</small></span></label>
            <div v-if="batteryEnabled" class="editor-fields four-columns surface-fields">
              <label class="field"><span>电量（0–1）</span><input v-model.number="battery.level" type="number" min="0" max="1" step="0.01" /></label>
              <label class="field"><span>充电秒数</span><input v-model.number="battery.chargingTimeSeconds" type="number" min="0" max="31536000" /></label>
              <label class="field"><span>放电秒数</span><input v-model.number="battery.dischargingTimeSeconds" type="number" min="0" max="31536000" /></label>
              <label class="switch-field"><input v-model="battery.charging" type="checkbox" /><span><strong>正在充电</strong><small>{{ battery.charging ? '是' : '否' }}</small></span></label>
            </div>
          </div>
        </article>
      </main>

      <aside class="editor-sidebar">
        <article class="panel identity-preview">
          <header><span class="identity-mark"><Icon icon="lucide:fingerprint" /></span><div><p class="page-eyebrow">运行身份预览</p><h2>{{ form.name || '未命名模板' }}</h2></div></header>
          <dl>
            <div><dt>浏览器</dt><dd>Chrome {{ form.browserMajor || '—' }}</dd></div>
            <div><dt>系统</dt><dd>{{ platformLabel(form.platform) }} {{ form.configuration.platformVersion || '' }}</dd></div>
            <div><dt>地区</dt><dd>{{ form.locale || '—' }}</dd></div>
            <div><dt>时区</dt><dd>{{ form.timezone || '—' }}</dd></div>
            <div><dt>视口</dt><dd>{{ form.configuration.windowWidth || '—' }} × {{ form.configuration.windowHeight || '—' }}</dd></div>
            <div><dt>屏幕</dt><dd>{{ form.configuration.screenWidth || '—' }} × {{ form.configuration.screenHeight || '—' }} @ {{ form.configuration.deviceScaleFactor || 1 }}x</dd></div>
            <div><dt>设备</dt><dd>{{ form.configuration.hardwareConcurrency || '—' }} 核 · {{ form.configuration.deviceMemory || '—' }} GiB</dd></div>
          </dl>
          <div class="preview-signals"><span v-for="signal in activeSignals" :key="signal">{{ signal }}</span><small v-if="!activeSignals.length">仅基础启动参数</small></div>
        </article>

        <article class="panel consistency-card">
          <span class="consistency-icon"><Icon icon="lucide:badge-check" /></span>
          <div><strong>一致性约束</strong><p>Cloud 会校验宿主系统和实际 Chromium 主版本。配置不匹配时启动会失败，而不会降级或伪装为另一套运行环境。</p></div>
        </article>

        <article v-if="template?.runtimeArgs.length" class="panel runtime-preview">
          <header><h2>当前运行参数</h2><span>v{{ template.version }}</span></header>
          <code v-for="argument in template.runtimeArgs" :key="argument">{{ argument }}</code>
          <p>高级页面信号由签名稳定的 MV3 运行时扩展注入。</p>
        </article>
      </aside>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, reactive, ref, watch } from "vue";
import { onBeforeRouteLeave, RouterLink, useRoute, useRouter } from "vue-router";
import { Icon } from "@iconify/vue";
import { api } from "@/api/client";
import PageHeader from "@/components/PageHeader.vue";
import { useSessionStore } from "@/stores/session";
import type { FingerprintBattery, FingerprintConfiguration, FingerprintCreateInput, FingerprintMediaDevices, FingerprintPlatform, FingerprintPreset, FingerprintTemplate } from "@/types";

interface EditorForm extends Omit<FingerprintCreateInput, "seed"> { seed: string }

const route = useRoute();
const router = useRouter();
const session = useSessionStore();
const template = ref<FingerprintTemplate>();
const presets = ref<FingerprintPreset[]>([]);
const selectedPresetKey = ref("");
const loading = ref(false);
const saving = ref(false);
const hydrating = ref(true);
const dirty = ref(false);
const errorMessage = ref("");
const successMessage = ref("");
const fontsText = ref("");
const touchPointsEnabled = ref(true);
const mediaEnabled = ref(true);
const batteryEnabled = ref(true);
const media = reactive<FingerprintMediaDevices>({ audioInputs: 1, videoInputs: 1, audioOutputs: 1 });
const battery = reactive<FingerprintBattery>({ charging: true, level: 0.82, chargingTimeSeconds: 900, dischargingTimeSeconds: 7200 });

const emptyConfiguration = (): FingerprintConfiguration => ({
  platformVersion: "", windowWidth: 0, windowHeight: 0,
  hardwareConcurrency: 0, deviceMemory: 0, colorDepth: 0,
  doNotTrack: "", screenWidth: 0, screenHeight: 0, deviceScaleFactor: 0,
  webrtcPolicy: "", canvasNoise: false, audioNoise: false, clientRectsNoise: false,
  fonts: [], webglVendor: "", webglRenderer: "",
});
const defaultConfiguration = (): FingerprintConfiguration => ({
  ...emptyConfiguration(),
  platformVersion: "10.0.0", windowWidth: 1440, windowHeight: 900,
  hardwareConcurrency: 8, deviceMemory: 8, colorDepth: 24, maxTouchPoints: 0,
  doNotTrack: "unspecified", screenWidth: 1920, screenHeight: 1080, deviceScaleFactor: 1,
  webrtcPolicy: "disable_non_proxied_udp", canvasNoise: true, audioNoise: true, clientRectsNoise: true,
  fonts: ["Arial", "Segoe UI", "Times New Roman", "Verdana"],
  webglVendor: "Google Inc. (Intel)", webglRenderer: "ANGLE (Intel, Intel Iris Xe Graphics, D3D11)",
  mediaDevices: { audioInputs: 1, videoInputs: 1, audioOutputs: 1 },
  battery: { charging: true, level: 0.82, chargingTimeSeconds: 900, dischargingTimeSeconds: 7200 },
});

const newForm = (): EditorForm => ({
  name: "", mode: "seeded", browserMajor: 144, platform: "windows", seed: "",
  locale: "en-US", timezone: "America/Los_Angeles", configuration: defaultConfiguration(),
});
const form = reactive<EditorForm>(newForm());

const isNew = computed(() => route.name === "fingerprint-new");
const parsedFonts = computed(() => {
  const seen = new Set<string>();
  return fontsText.value.split(/[\r\n,]+/).map((item) => item.trim()).filter((item) => {
    const key = item.toLocaleLowerCase();
    if (!item || seen.has(key)) return false;
    seen.add(key);
    return true;
  });
});
const modeDescription = computed(() => ({ seeded: "未填写种子时由服务端安全生成。", fixed: "必须指定正整数种子。", custom: "用于经过人工核对的定制组合。" })[form.mode]);
const activeSignals = computed(() => [
  form.configuration.canvasNoise && "Canvas", form.configuration.audioNoise && "Audio",
  form.configuration.clientRectsNoise && "Client Rects", form.configuration.webglVendor && "WebGL",
  parsedFonts.value.length && "Fonts", mediaEnabled.value && "Media", batteryEnabled.value && "Battery",
].filter(Boolean).map(String));

const cloneConfiguration = (value: FingerprintConfiguration): FingerprintConfiguration => JSON.parse(JSON.stringify(value)) as FingerprintConfiguration;
const readableError = (error: unknown) => error instanceof Error ? error.message : "操作失败，请稍后重试。";
const platformLabel = (value: FingerprintPlatform) => ({ windows: "Windows", macos: "macOS", linux: "Linux" })[value];
const positiveSeed = (value: string) => /^\d+$/.test(value) && BigInt(value) > 0n && BigInt(value) <= 9223372036854775807n;
const finiteNumber = (value: unknown) => Number.isFinite(Number(value));
const integerInRange = (value: unknown, minimum: number, maximum: number) => Number.isInteger(Number(value)) && Number(value) >= minimum && Number(value) <= maximum;

const setForm = (input: FingerprintCreateInput, seed = "") => {
  const configuration = { ...emptyConfiguration(), ...cloneConfiguration(input.configuration) };
  Object.assign(form, { name: input.name, mode: input.mode, browserMajor: input.browserMajor, platform: input.platform, seed, locale: input.locale, timezone: input.timezone, configuration });
  fontsText.value = (input.configuration.fonts || []).join("\n");
  touchPointsEnabled.value = input.configuration.maxTouchPoints !== undefined;
  mediaEnabled.value = Boolean(input.configuration.mediaDevices);
  Object.assign(media, input.configuration.mediaDevices || { audioInputs: 1, videoInputs: 1, audioOutputs: 1 });
  batteryEnabled.value = Boolean(input.configuration.battery);
  Object.assign(battery, input.configuration.battery || { charging: true, level: 0.82, chargingTimeSeconds: 900, dischargingTimeSeconds: 7200 });
};

const applySelectedPreset = () => {
  const preset = presets.value.find((item) => item.key === selectedPresetKey.value);
  if (!preset) return;
  setForm(preset.input, preset.input.seed || "");
  successMessage.value = `已载入 ${preset.name}，请核对后保存。`;
};

const hydrate = async () => {
  hydrating.value = true;
  loading.value = true;
  errorMessage.value = "";
  successMessage.value = "";
  try {
    presets.value = await api.get<FingerprintPreset[]>(`${session.workspaceBase}/fingerprint-presets`);
    if (isNew.value) {
      template.value = undefined;
      setForm(newForm());
      selectedPresetKey.value = String(route.query.preset || "");
      if (selectedPresetKey.value) applySelectedPreset();
    } else {
      const templateID = String(route.params.templateId || "");
      const current = await api.get<FingerprintTemplate>(`${session.workspaceBase}/fingerprint-templates/${encodeURIComponent(templateID)}`);
      template.value = current;
      setForm(current, current.seed);
    }
    await nextTick();
    dirty.value = false;
  } catch (error) {
    errorMessage.value = readableError(error);
  } finally { loading.value = false; hydrating.value = false; }
};

const generateSeed = () => {
  const words = crypto.getRandomValues(new Uint32Array(2));
  const value = (BigInt(words[0] & 0x7fffffff) << 32n) | BigInt(words[1]);
  form.seed = String(value || 1n);
};

const validate = () => {
  if (!form.name.trim() || Array.from(form.name.trim()).length > 120) return "请输入不超过 120 个字符的模板名称。";
  if (!integerInRange(form.browserMajor, 120, 250)) return "Chromium 主版本必须在 120 到 250 之间。";
  if (!/^[A-Za-z]{2,3}(?:-[A-Za-z0-9]{2,8})*$/.test(form.locale.trim())) return "请输入有效的 BCP 47 语言标签，例如 en-US。";
  try { new Intl.DateTimeFormat("en", { timeZone: form.timezone.trim() }).format(); } catch { return "请输入有效的 IANA 时区，例如 America/Los_Angeles。"; }
  if (form.seed && !positiveSeed(form.seed)) return "指纹种子必须是 1 到 9223372036854775807 之间的整数。";
  if ((form.mode === "fixed" || !isNew.value) && !positiveSeed(form.seed)) return "当前模式需要有效的正整数种子。";
  const config = form.configuration;
  if (config.platformVersion && !/^[A-Za-z0-9._-]{1,64}$/.test(config.platformVersion)) return "系统版本只能包含字母、数字、点、下划线和连字符。";
  const windowWidth = Number(config.windowWidth || 0);
  const windowHeight = Number(config.windowHeight || 0);
  if ((windowWidth === 0) !== (windowHeight === 0)) return "窗口宽度和高度必须同时填写或同时留空。";
  if (windowWidth !== 0 && (!integerInRange(windowWidth, 800, 7680) || !integerInRange(windowHeight, 600, 4320))) return "窗口尺寸超出支持范围（800×600 至 7680×4320）。";
  const screenWidth = Number(config.screenWidth || 0);
  const screenHeight = Number(config.screenHeight || 0);
  if ((screenWidth === 0) !== (screenHeight === 0)) return "屏幕宽度和高度必须同时填写或同时留空。";
  if (screenWidth !== 0 && (!integerInRange(screenWidth, 800, 15360) || !integerInRange(screenHeight, 600, 8640))) return "屏幕尺寸超出支持范围（800×600 至 15360×8640）。";
  const scale = Number(config.deviceScaleFactor || 0);
  if (scale !== 0 && (!finiteNumber(scale) || scale < 0.5 || scale > 4)) return "设备像素比必须在 0.5 到 4 之间。";
  const cores = Number(config.hardwareConcurrency || 0);
  if (cores !== 0 && !integerInRange(cores, 1, 128)) return "CPU 逻辑核心数必须在 1 到 128 之间。";
  if (![0, 1, 2, 4, 8].includes(Number(config.deviceMemory))) return "设备内存必须为 1、2、4 或 8 GiB。";
  if (![0, 16, 24, 30, 32].includes(Number(config.colorDepth))) return "色深不在支持范围内。";
  if (touchPointsEnabled.value && !integerInRange(config.maxTouchPoints, 0, 20)) return "最大触控点必须在 0 到 20 之间。";
  if (Boolean(config.webglVendor?.trim()) !== Boolean(config.webglRenderer?.trim())) return "WebGL 厂商和渲染器必须同时填写或同时留空。";
  if (parsedFonts.value.length > 128 || parsedFonts.value.some((font) => Array.from(font).length > 100 || /[\u0000-\u001f\u007f]/.test(font))) return "字体最多 128 项，单项不超过 100 个字符且不能包含控制字符。";
  if (mediaEnabled.value) {
    if (![media.audioInputs, media.videoInputs, media.audioOutputs].every((value) => integerInRange(value, 0, 16)) || media.audioInputs + media.videoInputs + media.audioOutputs > 32) return "媒体设备每类最多 16 个，总数最多 32 个。";
  }
  if (batteryEnabled.value) {
    if (!finiteNumber(battery.level) || battery.level < 0 || battery.level > 1 || !integerInRange(battery.chargingTimeSeconds, 0, 31536000) || !integerInRange(battery.dischargingTimeSeconds, 0, 31536000)) return "电池电量或时间超出支持范围。";
  }
  return "";
};

const buildConfiguration = (): FingerprintConfiguration => ({
  platformVersion: form.configuration.platformVersion?.trim() || undefined,
  windowWidth: Number(form.configuration.windowWidth), windowHeight: Number(form.configuration.windowHeight),
  hardwareConcurrency: Number(form.configuration.hardwareConcurrency), deviceMemory: Number(form.configuration.deviceMemory),
  colorDepth: Number(form.configuration.colorDepth), maxTouchPoints: touchPointsEnabled.value ? Number(form.configuration.maxTouchPoints) : undefined,
  doNotTrack: form.configuration.doNotTrack || "",
  screenWidth: Number(form.configuration.screenWidth), screenHeight: Number(form.configuration.screenHeight),
  deviceScaleFactor: Number(form.configuration.deviceScaleFactor), webrtcPolicy: form.configuration.webrtcPolicy || "default",
  canvasNoise: Boolean(form.configuration.canvasNoise), audioNoise: Boolean(form.configuration.audioNoise), clientRectsNoise: Boolean(form.configuration.clientRectsNoise),
  fonts: parsedFonts.value, webglVendor: form.configuration.webglVendor?.trim() || undefined, webglRenderer: form.configuration.webglRenderer?.trim() || undefined,
  mediaDevices: mediaEnabled.value ? { ...media } : undefined,
  battery: batteryEnabled.value ? { ...battery } : undefined,
});

const createInput = (): FingerprintCreateInput => ({
  name: form.name.trim(), mode: form.mode, browserMajor: Number(form.browserMajor), platform: form.platform,
  ...(form.seed ? { seed: form.seed } : {}), locale: form.locale.trim(), timezone: form.timezone.trim(), configuration: buildConfiguration(),
});

const save = async () => {
  if (saving.value) return;
  const validation = validate();
  if (validation) { errorMessage.value = validation; successMessage.value = ""; return; }
  saving.value = true;
  errorMessage.value = "";
  successMessage.value = "";
  try {
    const input = createInput();
    let saved: FingerprintTemplate;
    if (isNew.value) {
      saved = await api.post<FingerprintTemplate>(`${session.workspaceBase}/fingerprint-templates`, input);
    } else {
      if (!template.value) return;
      saved = await api.patch<FingerprintTemplate>(`${session.workspaceBase}/fingerprint-templates/${encodeURIComponent(template.value.id)}`, { ...input, seed: form.seed, version: template.value.version });
    }
    template.value = saved;
    hydrating.value = true;
    setForm(saved, saved.seed);
    await nextTick();
    dirty.value = false;
    hydrating.value = false;
    if (isNew.value) await router.replace({ name: "fingerprint-edit", params: { templateId: saved.id } });
    successMessage.value = `指纹模板已保存为版本 v${saved.version}。`;
  } catch (error) {
    errorMessage.value = readableError(error);
  } finally { saving.value = false; }
};

watch([form, fontsText, touchPointsEnabled, mediaEnabled, batteryEnabled, media, battery], () => { if (!hydrating.value) { dirty.value = true; successMessage.value = ""; } }, { deep: true });
watch(() => route.params.templateId, (next, previous) => { if (next !== previous && String(next || "") !== template.value?.id) void hydrate(); });
onMounted(() => void hydrate());
onBeforeRouteLeave(() => !dirty.value || window.confirm("当前指纹模板有尚未保存的修改，确定离开吗？"));
</script>

<style scoped>
.fingerprint-editor-page{max-width:1580px}.editor-success{display:flex;align-items:center;gap:8px;padding:11px 13px;border:1px solid #ccebdc;border-radius:9px;background:#effaf4;color:#117b4b}.editor-loading{min-height:300px;display:flex;align-items:center;justify-content:center;gap:8px;color:var(--muted)}.fingerprint-editor-layout{display:grid;grid-template-columns:minmax(620px,1fr) 310px;gap:16px;align-items:start}.editor-form-stack{display:grid;gap:16px}.preset-chooser{display:grid;grid-template-columns:minmax(250px,1fr) minmax(190px,280px) auto;align-items:center;gap:12px;padding:14px 16px}.preset-chooser>div{display:flex;align-items:center;gap:11px}.preset-chooser p{margin:3px 0 0;color:var(--muted);font-size:12px}.preset-chooser select{height:38px;padding:0 10px;border:1px solid var(--line);border-radius:8px;background:#fff}.editor-section{overflow:visible}.editor-section-heading{display:flex;align-items:center;gap:11px;padding:16px 18px;border-bottom:1px solid var(--line)}.editor-section-heading h2{margin:0;font-size:16px}.editor-section-heading p{margin:4px 0 0;color:var(--muted);font-size:12px}.section-icon{width:38px;height:38px;flex:0 0 auto;display:flex;align-items:center;justify-content:center;border-radius:10px;background:#edf4ff;color:var(--blue)}.section-icon.violet{background:#f3eeff;color:var(--violet)}.section-icon.green{background:#eaf9f1;color:var(--green)}.section-icon.orange{background:#fff5e7;color:var(--orange)}.editor-fields{display:grid;gap:16px;padding:18px}.editor-fields.two-columns{grid-template-columns:repeat(2,minmax(0,1fr))}.editor-fields.three-columns{grid-template-columns:repeat(3,minmax(0,1fr))}.editor-fields.four-columns{grid-template-columns:repeat(4,minmax(0,1fr))}.editor-fields textarea{padding:10px 11px;resize:vertical;line-height:1.55}.seed-field>div{display:grid;grid-template-columns:minmax(0,1fr) 38px;gap:6px}.seed-field .icon-button{width:38px;height:39px;border:1px solid var(--line)}.compact-fields{padding-bottom:10px}.signal-fields{padding-top:10px}.noise-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:10px;padding:4px 18px 8px}.check-tile{position:relative;display:block;cursor:pointer}.check-tile>input{position:absolute;opacity:0;pointer-events:none}.check-tile>span{min-height:78px;display:grid;grid-template-columns:27px minmax(0,1fr);align-content:center;column-gap:8px;padding:11px;border:1px solid var(--line);border-radius:9px;color:#5c6c84}.check-tile>span>svg{grid-row:1/3;width:20px;height:20px;align-self:center}.check-tile strong{font-size:12px}.check-tile small{margin-top:3px;color:var(--muted);font-size:11px}.check-tile.active>span{border-color:#b6cef8;background:#f4f8ff;color:#2e68c8;box-shadow:0 0 0 2px rgba(52,120,246,.06)}.surface-block{margin:0 18px 18px;border:1px solid var(--line);border-radius:10px;overflow:hidden}.surface-toggle{display:flex;align-items:center;gap:10px;padding:13px 14px;background:#f8fafc;cursor:pointer}.surface-toggle input,.switch-field input{width:16px;height:16px;accent-color:var(--blue)}.surface-toggle span{display:grid;gap:3px}.surface-toggle small{color:var(--muted);font-size:11px}.surface-fields{border-top:1px solid var(--line)}.switch-field{min-height:67px;display:flex;align-items:center;gap:10px;padding:12px;border:1px solid var(--line);border-radius:8px}.switch-field span{display:grid;gap:3px}.switch-field small{color:var(--muted);font-size:11px}.editor-sidebar{position:sticky;top:0;display:grid;gap:14px}.identity-preview{padding:18px}.identity-preview>header{display:flex;align-items:center;gap:11px;padding-bottom:16px;border-bottom:1px solid var(--line)}.identity-preview h2{max-width:205px;margin:0;overflow:hidden;font-size:16px;text-overflow:ellipsis;white-space:nowrap}.identity-preview .page-eyebrow{margin-bottom:4px}.identity-mark{width:44px;height:44px;display:flex;align-items:center;justify-content:center;border-radius:12px;background:var(--accent);color:#fff}.identity-mark svg{width:22px;height:22px}.identity-preview dl{margin:10px 0 0}.identity-preview dl>div{display:grid;grid-template-columns:70px minmax(0,1fr);gap:10px;padding:9px 0;border-bottom:1px solid #edf1f6}.identity-preview dt{color:var(--muted);font-size:12px}.identity-preview dd{margin:0;overflow:hidden;font-size:12px;font-weight:650;text-align:right;text-overflow:ellipsis;white-space:nowrap}.preview-signals{display:flex;flex-wrap:wrap;gap:5px;margin-top:14px}.preview-signals span{padding:3px 7px;border-radius:999px;background:#edf4ff;color:#326bbd;font-size:10px;font-weight:700}.preview-signals small{color:var(--muted)}.consistency-card{display:flex;align-items:flex-start;gap:11px;padding:15px}.consistency-icon{width:34px;height:34px;flex:0 0 auto;display:flex;align-items:center;justify-content:center;border-radius:9px;background:#eaf9f1;color:var(--green)}.consistency-card p{margin:5px 0 0;color:var(--muted);font-size:12px;line-height:1.55}.runtime-preview{display:grid;gap:7px;padding:15px}.runtime-preview header{display:flex;align-items:center;justify-content:space-between;margin-bottom:3px}.runtime-preview h2{margin:0;font-size:14px}.runtime-preview header span{color:var(--muted);font-size:11px}.runtime-preview code{overflow:hidden;padding:6px 7px;border-radius:6px;background:#f2f5f8;color:#506079;font-size:10px;text-overflow:ellipsis;white-space:nowrap}.runtime-preview p{margin:4px 0 0;color:var(--muted);font-size:11px;line-height:1.5}@media(max-width:1250px){.fingerprint-editor-layout{grid-template-columns:minmax(580px,1fr) 280px}.editor-fields.four-columns{grid-template-columns:repeat(2,minmax(0,1fr))}.preset-chooser{grid-template-columns:minmax(220px,1fr) 210px auto}}@media(max-width:1040px){.fingerprint-editor-layout{grid-template-columns:1fr}.editor-sidebar{position:static;grid-template-columns:repeat(2,minmax(0,1fr))}.runtime-preview{grid-column:1/-1}.preset-chooser{grid-template-columns:1fr auto}.preset-chooser select{grid-column:1/-1;grid-row:2}.editor-fields.three-columns{grid-template-columns:repeat(2,minmax(0,1fr))}}
</style>
