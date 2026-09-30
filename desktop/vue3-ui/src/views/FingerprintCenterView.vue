<template>
  <div class="page-stack fingerprint-center-page">
    <PageHeader title="指纹中心" description="为跨境账号维护可复现、与运行时一致的浏览器设备身份">
      <button class="button button-secondary" type="button" :disabled="!presets.length" @click="openBatch()"><Icon icon="lucide:copy-plus" />批量生成</button>
      <RouterLink class="button button-primary" :to="{ name: 'fingerprint-new' }"><Icon icon="lucide:plus" />新建模板</RouterLink>
    </PageHeader>

    <div v-if="errorMessage" class="alert alert-danger" role="alert"><Icon icon="lucide:circle-alert" />{{ errorMessage }}</div>
    <div v-if="successMessage" class="fingerprint-success" role="status"><Icon icon="lucide:circle-check" />{{ successMessage }}</div>

    <section class="preset-section" aria-labelledby="preset-heading">
      <header class="section-heading">
        <div><p class="page-eyebrow">经过验证的起点</p><h2 id="preset-heading">场景预设</h2></div>
        <p>选择后进入独立编辑页，可在保存前核对每项设备信号。</p>
      </header>
      <div class="preset-grid">
        <article v-for="preset in presets" :key="preset.key" class="preset-card">
          <span class="preset-icon"><Icon :icon="presetIcon(preset.key)" /></span>
          <div class="preset-copy">
            <div><strong>{{ preset.name }}</strong><span>{{ platformLabel(preset.input.platform) }} · Chrome {{ preset.input.browserMajor }}</span></div>
            <p>{{ preset.description }}</p>
            <small><Icon icon="lucide:map-pin" />{{ preset.input.locale }} · {{ preset.input.timezone }}</small>
          </div>
          <div class="preset-actions">
            <button class="button button-quiet small" type="button" @click="openBatch(preset.key)">批量</button>
            <RouterLink class="button button-secondary small" :to="{ name: 'fingerprint-new', query: { preset: preset.key } }">使用预设</RouterLink>
          </div>
        </article>
        <article v-if="loading && !presets.length" v-for="index in 3" :key="index" class="preset-card preset-skeleton" aria-hidden="true"></article>
      </div>
    </section>

    <article class="panel">
      <div class="panel-header fingerprint-list-heading">
        <div><h2>指纹模板</h2><p>{{ templates.length }} 个模板可分配给浏览器实例</p></div>
        <span class="tag">配置版本受保护</span>
      </div>
      <div class="toolbar">
        <label class="search-field"><Icon icon="lucide:search" /><input v-model.trim="query" type="search" placeholder="搜索名称、地区或时区…" /></label>
        <select v-model="platformFilter" aria-label="筛选操作系统"><option value="">全部系统</option><option value="windows">Windows</option><option value="macos">macOS</option><option value="linux">Linux</option></select>
        <select v-model="modeFilter" aria-label="筛选生成模式"><option value="">全部模式</option><option value="seeded">随机种子</option><option value="fixed">固定种子</option><option value="custom">自定义</option></select>
        <span class="toolbar-spacer"></span>
        <button class="button button-quiet" type="button" :disabled="loading" @click="load"><Icon :class="{ spin: loading }" icon="lucide:refresh-cw" />刷新</button>
      </div>
      <div class="table-wrap">
        <table v-if="filtered.length" class="data-table fingerprint-table">
          <thead><tr><th>模板</th><th>系统 / 浏览器</th><th>地区身份</th><th>隔离信号</th><th>种子</th><th>更新于</th><th class="actions-cell">操作</th></tr></thead>
          <tbody>
            <tr v-for="item in filtered" :key="item.id">
              <td><div class="primary-cell"><span class="row-icon"><Icon icon="lucide:fingerprint" /></span><div><strong>{{ item.name }}</strong><small>{{ modeLabel(item.mode) }} · v{{ item.version }}</small></div></div></td>
              <td><strong>{{ platformLabel(item.platform) }}</strong><small class="cell-subtitle">Chromium {{ item.browserMajor }} · {{ item.configuration.platformVersion || '默认系统版本' }}</small></td>
              <td><strong>{{ item.locale }}</strong><small class="cell-subtitle">{{ item.timezone }}</small></td>
              <td><div class="signal-list"><span v-if="item.configuration.canvasNoise">Canvas</span><span v-if="item.configuration.audioNoise">Audio</span><span v-if="item.configuration.clientRectsNoise">Rects</span><span v-if="item.configuration.webglVendor">WebGL</span><span v-if="item.configuration.mediaDevices">Media</span><span v-if="item.configuration.battery">Battery</span><small v-if="!signalCount(item.configuration)">基础</small></div></td>
              <td><code class="seed-value" :title="item.seed">{{ compactSeed(item.seed) }}</code></td>
              <td>{{ formatDate(item.updatedAt) }}</td>
              <td class="actions-cell"><RouterLink class="button button-quiet small" :to="{ name: 'fingerprint-edit', params: { templateId: item.id } }"><Icon icon="lucide:pencil" />编辑</RouterLink><button class="icon-button fingerprint-delete" type="button" :disabled="deletingId === item.id" :aria-label="`删除 ${item.name}`" @click="remove(item)"><Icon :class="{ spin: deletingId === item.id }" :icon="deletingId === item.id ? 'lucide:loader-circle' : 'lucide:trash-2'" /></button></td>
            </tr>
          </tbody>
        </table>
        <EmptyState v-if="!filtered.length && !loading" icon="lucide:fingerprint" :title="templates.length ? '没有匹配的指纹模板' : '还没有指纹模板'" :description="templates.length ? '调整搜索条件或筛选项。' : '从场景预设开始，或创建一份经过完整核对的设备身份。'"><RouterLink class="button button-primary" :to="{ name: 'fingerprint-new' }">新建模板</RouterLink></EmptyState>
        <div v-if="loading && !templates.length" class="fingerprint-loading"><Icon class="spin" icon="lucide:loader-circle" />正在加载模板…</div>
      </div>
    </article>

    <ModalDialog :open="batchOpen" title="批量生成指纹" description="基于同一预设原子创建最多 100 份模板，每份使用独立种子。" @close="closeBatch">
      <div v-if="batchError" class="alert alert-danger batch-alert" role="alert"><Icon icon="lucide:circle-alert" />{{ batchError }}</div>
      <div class="form-grid">
        <label class="field field-span"><span>配置预设</span><select v-model="batch.presetKey"><option v-for="preset in presets" :key="preset.key" :value="preset.key">{{ preset.name }} · {{ preset.input.locale }} · {{ preset.input.timezone }}</option></select><small>高级设备参数将完整复制；生成后仍可逐份编辑。</small></label>
        <label class="field field-span"><span>名称前缀</span><input v-model.trim="batch.namePrefix" maxlength="110" placeholder="例如：Amazon US 店铺" /></label>
        <label class="field"><span>生成数量</span><input v-model.number="batch.count" type="number" min="1" max="100" /></label>
        <label class="field"><span>种子模式</span><select v-model="batch.mode"><option value="seeded">随机起始种子</option><option value="fixed">指定连续种子</option><option value="custom">自定义模板</option></select></label>
        <label v-if="batch.mode === 'fixed'" class="field field-span"><span>起始种子</span><input v-model.trim="batch.seedStart" inputmode="numeric" placeholder="正整数，例如 73000001" /><small>系统会从此值开始连续分配，确保批次内不重复。</small></label>
      </div>
      <div v-if="batchPreset" class="batch-summary"><span class="preset-icon"><Icon :icon="presetIcon(batchPreset.key)" /></span><div><strong>{{ batchPreview }}</strong><p>{{ platformLabel(batchPreset.input.platform) }} · Chromium {{ batchPreset.input.browserMajor }} · {{ batchPreset.input.locale }} · {{ batchPreset.input.timezone }}</p></div></div>
      <template #footer><button class="button button-secondary" type="button" @click="closeBatch">取消</button><button class="button button-primary" type="button" :disabled="batchSaving || !batchPreset || !batch.namePrefix || batch.count < 1 || batch.count > 100" @click="createBatch"><Icon :class="{ spin: batchSaving }" :icon="batchSaving ? 'lucide:loader-circle' : 'lucide:copy-plus'" />{{ batchSaving ? '生成中…' : `生成 ${batch.count} 份` }}</button></template>
    </ModalDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { Icon } from "@iconify/vue";
import { RouterLink } from "vue-router";
import { api, idempotencyKey } from "@/api/client";
import EmptyState from "@/components/EmptyState.vue";
import ModalDialog from "@/components/ModalDialog.vue";
import PageHeader from "@/components/PageHeader.vue";
import { useSessionStore } from "@/stores/session";
import type { FingerprintBatchCreateInput, FingerprintConfiguration, FingerprintMode, FingerprintPreset, FingerprintTemplate } from "@/types";

interface BatchForm { presetKey: string; namePrefix: string; count: number; mode: FingerprintMode; seedStart: string }

const session = useSessionStore();
const templates = ref<FingerprintTemplate[]>([]);
const presets = ref<FingerprintPreset[]>([]);
const query = ref("");
const platformFilter = ref("");
const modeFilter = ref("");
const loading = ref(false);
const batchOpen = ref(false);
const batchSaving = ref(false);
const deletingId = ref("");
const errorMessage = ref("");
const successMessage = ref("");
const batchError = ref("");
const batch = reactive<BatchForm>({ presetKey: "", namePrefix: "", count: 10, mode: "seeded", seedStart: "" });

const filtered = computed(() => {
  const needle = query.value.toLocaleLowerCase();
  return templates.value.filter((item) => {
    const search = `${item.name} ${item.platform} ${item.locale} ${item.timezone}`.toLocaleLowerCase();
    return (!needle || search.includes(needle)) && (!platformFilter.value || item.platform === platformFilter.value) && (!modeFilter.value || item.mode === modeFilter.value);
  });
});
const batchPreset = computed(() => presets.value.find((item) => item.key === batch.presetKey));
const batchPreview = computed(() => {
  if (!batch.namePrefix || batch.count < 1) return "填写前缀与数量后即可生成";
  const digits = Math.max(2, String(batch.count).length);
  return `${batch.namePrefix} ${String(1).padStart(digits, "0")} — ${batch.namePrefix} ${String(batch.count).padStart(digits, "0")}`;
});

const cloneConfiguration = (value: FingerprintConfiguration): FingerprintConfiguration => JSON.parse(JSON.stringify(value)) as FingerprintConfiguration;
const readableError = (error: unknown) => error instanceof Error ? error.message : "操作失败，请稍后重试。";
const formatDate = (value: string) => new Intl.DateTimeFormat("zh-CN", { dateStyle: "short", timeStyle: "short" }).format(new Date(value));
const compactSeed = (value: string) => value.length > 12 ? `${value.slice(0, 6)}…${value.slice(-4)}` : value;
const modeLabel = (value: FingerprintMode) => ({ seeded: "随机种子", fixed: "固定种子", custom: "自定义" })[value];
const platformLabel = (value: string) => ({ windows: "Windows", macos: "macOS", linux: "Linux" }[value] || value);
const presetIcon = (key: string) => key.includes("amazon") ? "lucide:package" : key.includes("tiktok") ? "lucide:music-2" : "lucide:messages-square";
const signalCount = (value: FingerprintConfiguration) => [value.canvasNoise, value.audioNoise, value.clientRectsNoise, value.webglVendor, value.mediaDevices, value.battery].filter(Boolean).length;

const load = async () => {
  loading.value = true;
  errorMessage.value = "";
  try {
    const [templateResult, presetResult] = await Promise.all([
      api.get<FingerprintTemplate[]>(`${session.workspaceBase}/fingerprint-templates`),
      api.get<FingerprintPreset[]>(`${session.workspaceBase}/fingerprint-presets`),
    ]);
    templates.value = templateResult;
    presets.value = presetResult;
    if (!batch.presetKey || !presets.value.some((item) => item.key === batch.presetKey)) batch.presetKey = presets.value[0]?.key || "";
  } catch (error) {
    errorMessage.value = readableError(error);
  } finally { loading.value = false; }
};

const openBatch = (presetKey?: string) => {
  const selected = presets.value.find((item) => item.key === presetKey) || presets.value[0];
  batch.presetKey = selected?.key || "";
  batch.namePrefix = selected ? `${selected.name} 账号` : "";
  batch.count = 10;
  batch.mode = "seeded";
  batch.seedStart = "";
  batchError.value = "";
  batchOpen.value = true;
};
const closeBatch = () => { if (!batchSaving.value) batchOpen.value = false; };

const createBatch = async () => {
  const preset = batchPreset.value;
  if (!preset) return;
  if (!batch.namePrefix.trim() || batch.namePrefix.trim().length > 110) { batchError.value = "请输入不超过 110 个字符的名称前缀。"; return; }
  if (!Number.isInteger(batch.count) || batch.count < 1 || batch.count > 100) { batchError.value = "单次生成数量必须在 1 到 100 之间。"; return; }
  if (batch.mode === "fixed" && (!/^\d+$/.test(batch.seedStart) || BigInt(batch.seedStart) <= 0n)) { batchError.value = "固定模式需要正整数起始种子。"; return; }
  batchSaving.value = true;
  batchError.value = "";
  try {
    const input: FingerprintBatchCreateInput = {
      namePrefix: batch.namePrefix.trim(), count: batch.count, mode: batch.mode,
      browserMajor: preset.input.browserMajor, platform: preset.input.platform,
      locale: preset.input.locale, timezone: preset.input.timezone,
      configuration: cloneConfiguration(preset.input.configuration),
      ...(batch.mode === "fixed" ? { seedStart: batch.seedStart } : {}),
    };
    const created = await api.post<FingerprintTemplate[]>(`${session.workspaceBase}/fingerprint-templates/batch`, input, idempotencyKey("fingerprint-batch"));
    batchOpen.value = false;
    successMessage.value = `已原子创建 ${created.length} 份指纹模板。`;
    await load();
  } catch (error) {
    batchError.value = readableError(error);
  } finally { batchSaving.value = false; }
};

const remove = async (item: FingerprintTemplate) => {
  if (!window.confirm(`确定删除“${item.name}”吗？已被浏览器实例使用的模板会被服务端保护。`)) return;
  deletingId.value = item.id;
  errorMessage.value = "";
  successMessage.value = "";
  try {
    await api.delete(`${session.workspaceBase}/fingerprint-templates/${encodeURIComponent(item.id)}?version=${item.version}`);
    templates.value = templates.value.filter((current) => current.id !== item.id);
    successMessage.value = `已删除指纹模板“${item.name}”。`;
  } catch (error) {
    errorMessage.value = readableError(error);
  } finally { deletingId.value = ""; }
};

onMounted(() => void load());
</script>

<style scoped>
.fingerprint-center-page{max-width:1540px}.fingerprint-success{display:flex;align-items:center;gap:8px;padding:11px 13px;border:1px solid #ccebdc;border-radius:9px;background:#effaf4;color:#117b4b}.preset-section{display:grid;gap:12px}.section-heading{display:flex;align-items:end;justify-content:space-between;gap:20px}.section-heading h2{margin:0;font-size:16px}.section-heading .page-eyebrow{margin-bottom:5px}.section-heading>p{margin:0;color:var(--muted);font-size:12px}.preset-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:12px}.preset-card{min-height:154px;display:grid;grid-template-columns:42px minmax(0,1fr);gap:12px;padding:16px;background:var(--surface);border:1px solid var(--line);border-radius:var(--radius)}.preset-card:hover{border-color:#c9d5e6;box-shadow:0 5px 18px rgba(24,39,75,.05)}.preset-icon{width:42px;height:42px;display:flex;align-items:center;justify-content:center;border-radius:11px;background:#edf4ff;color:var(--blue)}.preset-copy{min-width:0}.preset-copy>div{display:flex;align-items:center;justify-content:space-between;gap:8px}.preset-copy>div span{color:var(--muted);font-size:11px;white-space:nowrap}.preset-copy p{height:36px;margin:7px 0;color:var(--muted);font-size:12px;line-height:1.5}.preset-copy small{display:flex;align-items:center;gap:5px;color:#586a84}.preset-copy small svg{width:13px}.preset-actions{grid-column:2;display:flex;justify-content:flex-end;gap:6px}.preset-skeleton{grid-column:auto;animation:pulse 1.4s ease-in-out infinite;background:#eef2f7}@keyframes pulse{50%{opacity:.55}}.fingerprint-list-heading .tag{align-self:center}.fingerprint-table td{height:72px}.signal-list{max-width:210px;display:flex;flex-wrap:wrap;gap:4px}.signal-list span{padding:2px 6px;border-radius:5px;background:#edf4ff;color:#3469b5;font-size:10px;font-weight:650}.signal-list small{color:var(--muted)}.seed-value{display:inline-block;max-width:112px;padding:4px 7px;border-radius:6px;background:#f2f5f8;color:#4d5c72;font-size:11px}.fingerprint-delete:hover{background:#fff0f0;color:var(--red)}.fingerprint-loading{min-height:220px;display:flex;align-items:center;justify-content:center;gap:8px;color:var(--muted)}.batch-alert{margin-bottom:15px}.batch-summary{display:flex;align-items:center;gap:11px;margin-top:16px;padding:13px;border:1px solid #dce7f7;border-radius:10px;background:#f6f9fe}.batch-summary>div{min-width:0}.batch-summary strong{display:block;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.batch-summary p{margin:4px 0 0;color:var(--muted);font-size:12px}@media(max-width:1200px){.preset-grid{grid-template-columns:1fr}.preset-card{min-height:auto}.preset-copy p{height:auto}.signal-list{max-width:150px}}
</style>
