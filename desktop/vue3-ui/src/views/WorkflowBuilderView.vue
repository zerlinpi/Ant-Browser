<template>
  <div class="page-stack workflow-builder-page">
    <PageHeader
      eyebrow="AUTOMATION / BUILDER"
      :title="isNew ? '新建自动化工作流' : workflow?.name || '工作流编排'"
      description="按执行顺序配置步骤，保存为不可变版本，再将确认过的版本发布给任务队列。"
    >
      <RouterLink class="button button-secondary" :to="{ name: 'automation' }"><Icon icon="lucide:arrow-left" />返回</RouterLink>
      <button class="button button-secondary" type="button" :disabled="saving || loading" @click="saveWorkflow"><Icon icon="lucide:save" />{{ saving ? '保存中…' : '保存版本' }}</button>
      <button class="button button-primary" type="button" :disabled="saving || publishing || loading" @click="publishWorkflow"><Icon icon="lucide:send" />{{ publishing ? '发布中…' : '保存并发布' }}</button>
    </PageHeader>

    <div v-if="errorMessage" class="alert alert-danger" role="alert"><Icon icon="lucide:circle-alert" />{{ errorMessage }}</div>
    <div v-if="successMessage" class="builder-success" role="status"><Icon icon="lucide:circle-check" />{{ successMessage }}</div>

    <article v-if="loading" class="panel builder-loading"><Icon class="spin" icon="lucide:loader-circle" />正在加载工作流版本…</article>

    <section v-else class="builder-layout">
      <aside class="panel builder-palette" aria-label="步骤组件">
        <header class="builder-panel-heading">
          <div><p class="page-eyebrow">步骤库</p><h2>添加动作</h2></div>
          <span>{{ steps.length }}/500</span>
        </header>
        <p class="builder-panel-copy">选择动作后会追加到流程末尾，可再调整顺序和参数。</p>
        <div class="action-palette">
          <button v-for="action in actionCatalog" :key="action.id" type="button" @click="addStep(action.id)">
            <span class="action-icon"><Icon :icon="action.icon" /></span>
            <span><strong>{{ action.label }}</strong><small>{{ action.hint }}</small></span>
            <Icon icon="lucide:plus" />
          </button>
        </div>
      </aside>

      <main class="panel builder-canvas">
        <header class="builder-panel-heading canvas-heading">
          <div><p class="page-eyebrow">执行序列</p><h2>工作流步骤</h2></div>
          <span v-if="workflow" class="tag">v{{ workflow.latestVersion }} · {{ workflow.status }}</span>
          <span v-else class="tag">尚未保存</span>
        </header>

        <div class="workflow-basics">
          <label class="field"><span>工作流名称</span><input v-model.trim="name" maxlength="120" :disabled="!isNew" placeholder="例如：Amazon 登录与订单采集" /><small v-if="!isNew">工作流名称在创建后保持稳定；修改步骤会生成新版本。</small></label>
          <label class="field"><span>执行引擎</span><select v-model="engine"><option value="playwright">Playwright</option><option value="puppeteer">Puppeteer</option><option value="cdp">Chrome DevTools Protocol</option></select><small>每个版本固定一个执行引擎。</small></label>
        </div>

        <div v-if="steps.length" class="step-sequence">
          <article v-for="(step, index) in steps" :key="step.id" class="step-card" :class="{ selected: selectedStepId === step.id }">
            <button class="step-select" type="button" :aria-pressed="selectedStepId === step.id" @click="selectedStepId = step.id">
              <span class="step-number">{{ index + 1 }}</span>
              <span class="step-action-icon"><Icon :icon="actionInfo(step.action).icon" /></span>
              <span class="step-copy"><strong>{{ actionInfo(step.action).label }}</strong><small>{{ stepSummary(step) }}</small></span>
            </button>
            <div class="step-actions" aria-label="步骤排序与删除">
              <button type="button" :disabled="index === 0" :aria-label="`上移步骤 ${index + 1}`" @click="moveStep(index, -1)"><Icon icon="lucide:arrow-up" /></button>
              <button type="button" :disabled="index === steps.length - 1" :aria-label="`下移步骤 ${index + 1}`" @click="moveStep(index, 1)"><Icon icon="lucide:arrow-down" /></button>
              <button class="danger" type="button" :aria-label="`删除步骤 ${index + 1}`" @click="removeStep(index)"><Icon icon="lucide:trash-2" /></button>
            </div>
          </article>
        </div>
        <div v-else class="builder-empty">
          <span><Icon icon="lucide:workflow" /></span>
          <h3>从一个动作开始</h3>
          <p>工作流至少需要一个步骤。常见流程从“打开网页”开始，以“关闭浏览器”结束。</p>
          <button class="button button-primary" type="button" @click="addStep('navigate')"><Icon icon="lucide:plus" />添加打开网页</button>
        </div>
      </main>

      <aside class="panel builder-inspector" aria-label="步骤配置">
        <template v-if="selectedStep">
          <header class="builder-panel-heading">
            <div><p class="page-eyebrow">步骤配置</p><h2>{{ actionInfo(selectedStep.action).label }}</h2></div>
            <code>{{ selectedStep.id }}</code>
          </header>
          <div class="inspector-form">
            <template v-if="selectedStep.action === 'navigate'">
              <label class="field"><span>目标网址</span><input :value="stringParam('url')" type="url" placeholder="https://seller.example.com" @input="setStringParam('url', $event)" /></label>
              <label class="field"><span>等待条件</span><select :value="stringParam('waitUntil') || 'load'" @change="setStringParam('waitUntil', $event)"><option value="load">页面加载完成</option><option value="domcontentloaded">DOM 已就绪</option><option value="networkidle">网络空闲</option></select></label>
            </template>

            <template v-else-if="selectedStep.action === 'click'">
              <label class="field"><span>元素选择器</span><input :value="stringParam('selector')" placeholder="button[type='submit']" @input="setStringParam('selector', $event)" /></label>
              <label class="field"><span>鼠标按键</span><select :value="stringParam('button') || 'left'" @change="setStringParam('button', $event)"><option value="left">左键</option><option value="right">右键</option><option value="middle">中键</option></select></label>
            </template>

            <template v-else-if="selectedStep.action === 'input'">
              <label class="field"><span>元素选择器</span><input :value="stringParam('selector')" placeholder="input[name='email']" @input="setStringParam('selector', $event)" /></label>
              <label class="field"><span>输入来源</span><select :value="inputUsesReference ? 'reference' : 'literal'" @change="setInputSource"><option value="literal">普通文本</option><option value="reference">安全引用</option></select></label>
              <label v-if="inputUsesReference" class="field"><span>值引用</span><input :value="stringParam('valueRef')" placeholder="account-secret://account-id/password" @input="setStringParam('valueRef', $event)" /><small>敏感值只允许 account-secret://、artifact:// 或 variable:// 引用。</small></label>
              <label v-else class="field"><span>输入文本</span><input :value="stringParam('value')" autocomplete="off" placeholder="可使用 {{ variable }}" @input="setStringParam('value', $event)" /></label>
              <label class="check-field"><input :checked="booleanParam('clear', true)" type="checkbox" @change="setBooleanParam('clear', $event)" /><span>输入前清空原内容</span></label>
            </template>

            <template v-else-if="selectedStep.action === 'wait'">
              <label class="field"><span>等待方式</span><select :value="waitUsesSelector ? 'selector' : 'duration'" @change="setWaitSource"><option value="duration">固定时长</option><option value="selector">等待元素</option></select></label>
              <label v-if="waitUsesSelector" class="field"><span>元素选择器</span><input :value="stringParam('selector')" placeholder="[data-ready='true']" @input="setStringParam('selector', $event)" /></label>
              <label v-if="waitUsesSelector" class="field"><span>元素状态</span><select :value="stringParam('state') || 'visible'" @change="setStringParam('state', $event)"><option value="visible">可见</option><option value="attached">已挂载</option><option value="hidden">已隐藏</option><option value="detached">已移除</option></select></label>
              <label v-else class="field"><span>等待毫秒</span><input :value="numberParam('durationMs', 1000)" type="number" min="1" max="300000" @input="setNumberParam('durationMs', $event)" /></label>
            </template>

            <template v-else-if="selectedStep.action === 'upload'">
              <label class="field"><span>文件输入选择器</span><input :value="stringParam('selector')" placeholder="input[type='file']" @input="setStringParam('selector', $event)" /></label>
              <label class="field"><span>文件引用</span><input :value="stringParam('artifactRef')" placeholder="artifact://upload/catalog.csv" @input="setStringParam('artifactRef', $event)" /></label>
            </template>

            <template v-else-if="selectedStep.action === 'javascript'">
              <label class="field"><span>JavaScript</span><textarea :value="stringParam('script')" rows="10" spellcheck="false" placeholder="return document.title;" @input="setStringParam('script', $event)" /></label>
              <p class="inspector-note"><Icon icon="lucide:shield-check" />脚本在当前页面上下文执行；不要把密码、Cookie 或令牌直接写入定义。</p>
            </template>

            <template v-else-if="selectedStep.action === 'screenshot'">
              <label class="field"><span>输出名称</span><input :value="stringParam('name')" maxlength="120" placeholder="order-summary" @input="setStringParam('name', $event)" /></label>
              <label class="field"><span>图片格式</span><select :value="stringParam('format') || 'png'" @change="setStringParam('format', $event)"><option value="png">PNG</option><option value="jpeg">JPEG</option></select></label>
              <label class="check-field"><input :checked="booleanParam('fullPage', true)" type="checkbox" @change="setBooleanParam('fullPage', $event)" /><span>截取完整页面</span></label>
            </template>

            <template v-else-if="selectedStep.action === 'extract'">
              <label class="field"><span>元素选择器</span><input :value="stringParam('selector')" placeholder=".order-total" @input="setStringParam('selector', $event)" /></label>
              <label class="field"><span>读取属性（可选）</span><input :value="stringParam('attribute')" placeholder="留空读取文本" @input="setOptionalStringParam('attribute', $event)" /></label>
              <label class="field"><span>结果变量</span><input :value="stringParam('storeAs')" maxlength="120" placeholder="orderTotal" @input="setStringParam('storeAs', $event)" /></label>
              <label class="check-field"><input :checked="booleanParam('multiple')" type="checkbox" @change="setBooleanParam('multiple', $event)" /><span>读取所有匹配元素</span></label>
            </template>

            <div v-else class="close-step-note"><Icon icon="lucide:circle-stop" /><p><strong>结束本次浏览器会话</strong><span>关闭动作不需要额外参数，通常放在流程末尾。</span></p></div>

            <div class="inspector-divider"></div>
            <label class="field"><span>步骤超时（毫秒）</span><input :value="selectedStep.timeoutMs || 30000" type="number" min="100" max="600000" @input="setStepTimeout" /></label>
            <label class="check-field"><input :checked="selectedStep.continueOnError" type="checkbox" @change="setContinueOnError" /><span>失败后继续执行下一步</span></label>
          </div>
        </template>
        <div v-else class="inspector-empty"><Icon icon="lucide:mouse-pointer-click" /><p>选择中间流程中的一个步骤，在这里配置参数。</p></div>
      </aside>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from "vue";
import { onBeforeRouteLeave, RouterLink, useRoute, useRouter } from "vue-router";
import { Icon } from "@iconify/vue";
import { api } from "@/api/client";
import PageHeader from "@/components/PageHeader.vue";
import { useSessionStore } from "@/stores/session";
import type { Workflow, WorkflowAction, WorkflowDefinition, WorkflowEngine, WorkflowStep, WorkflowVersion } from "@/types";

interface WorkflowMutationResult { workflow: Workflow; version: WorkflowVersion }
interface ActionInfo { id: WorkflowAction; label: string; hint: string; icon: string }

const actionCatalog: ActionInfo[] = [
  { id: "navigate", label: "打开网页", hint: "访问 HTTP(S) 地址", icon: "lucide:globe" },
  { id: "click", label: "点击元素", hint: "按选择器触发点击", icon: "lucide:mouse-pointer-click" },
  { id: "input", label: "输入内容", hint: "文本或安全引用", icon: "lucide:text-cursor-input" },
  { id: "wait", label: "等待", hint: "时长或元素状态", icon: "lucide:timer" },
  { id: "upload", label: "上传文件", hint: "使用受控文件引用", icon: "lucide:upload" },
  { id: "javascript", label: "执行脚本", hint: "运行页面 JavaScript", icon: "lucide:braces" },
  { id: "screenshot", label: "页面截图", hint: "保存 PNG 或 JPEG", icon: "lucide:camera" },
  { id: "extract", label: "采集数据", hint: "提取文本或属性", icon: "lucide:scan-search" },
  { id: "close", label: "关闭浏览器", hint: "结束当前会话", icon: "lucide:circle-stop" },
];

const route = useRoute();
const router = useRouter();
const session = useSessionStore();
const workflow = ref<Workflow>();
const name = ref("");
const engine = ref<WorkflowEngine>("playwright");
const steps = ref<WorkflowStep[]>([]);
const selectedStepId = ref("");
const loading = ref(false);
const saving = ref(false);
const publishing = ref(false);
const dirty = ref(false);
const hydrating = ref(true);
const errorMessage = ref("");
const successMessage = ref("");
let stepSequence = 0;

const isNew = computed(() => route.name === "automation-new");
const selectedStep = computed(() => steps.value.find((step) => step.id === selectedStepId.value));
const inputUsesReference = computed(() => selectedStep.value?.parameters.valueRef !== undefined);
const waitUsesSelector = computed(() => selectedStep.value?.parameters.selector !== undefined);

const actionInfo = (action: WorkflowAction) => actionCatalog.find((item) => item.id === action) || actionCatalog[0];
const eventValue = (event: Event) => (event.target as HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement).value;
const eventChecked = (event: Event) => (event.target as HTMLInputElement).checked;
const stringParam = (key: string) => String(selectedStep.value?.parameters[key] ?? "");
const numberParam = (key: string, fallback = 0) => Number(selectedStep.value?.parameters[key] ?? fallback);
const booleanParam = (key: string, fallback = false) => Boolean(selectedStep.value?.parameters[key] ?? fallback);

const defaultParameters = (action: WorkflowAction): Record<string, unknown> => {
  switch (action) {
    case "navigate": return { url: "", waitUntil: "load" };
    case "click": return { selector: "", button: "left" };
    case "input": return { selector: "", value: "", clear: true };
    case "wait": return { durationMs: 1000 };
    case "upload": return { selector: "", artifactRef: "artifact://" };
    case "javascript": return { script: "" };
    case "screenshot": return { name: "capture", format: "png", fullPage: true };
    case "extract": return { selector: "", storeAs: "result", multiple: false };
    case "close": return {};
  }
};

const nextStepID = (action: WorkflowAction) => {
  let candidate = "";
  do { stepSequence += 1; candidate = `${action}_${stepSequence}`; } while (steps.value.some((step) => step.id === candidate));
  return candidate;
};

const addStep = (action: WorkflowAction) => {
  const step: WorkflowStep = { id: nextStepID(action), action, timeoutMs: 30000, parameters: defaultParameters(action) };
  steps.value.push(step);
  selectedStepId.value = step.id;
};

const moveStep = (index: number, delta: number) => {
  const target = index + delta;
  if (target < 0 || target >= steps.value.length) return;
  const [step] = steps.value.splice(index, 1);
  steps.value.splice(target, 0, step);
};

const removeStep = (index: number) => {
  const [removed] = steps.value.splice(index, 1);
  if (removed?.id === selectedStepId.value) selectedStepId.value = steps.value[Math.min(index, steps.value.length - 1)]?.id || "";
};

const setStringParam = (key: string, event: Event) => { if (selectedStep.value) selectedStep.value.parameters[key] = eventValue(event); };
const setOptionalStringParam = (key: string, event: Event) => {
  if (!selectedStep.value) return;
  const value = eventValue(event).trim();
  if (value) selectedStep.value.parameters[key] = value;
  else delete selectedStep.value.parameters[key];
};
const setNumberParam = (key: string, event: Event) => { if (selectedStep.value) selectedStep.value.parameters[key] = Number(eventValue(event)); };
const setBooleanParam = (key: string, event: Event) => { if (selectedStep.value) selectedStep.value.parameters[key] = eventChecked(event); };
const setStepTimeout = (event: Event) => { if (selectedStep.value) selectedStep.value.timeoutMs = Number(eventValue(event)); };
const setContinueOnError = (event: Event) => { if (selectedStep.value) selectedStep.value.continueOnError = eventChecked(event); };

const setInputSource = (event: Event) => {
  if (!selectedStep.value) return;
  if (eventValue(event) === "reference") {
    delete selectedStep.value.parameters.value;
    selectedStep.value.parameters.valueRef = "account-secret://";
  } else {
    delete selectedStep.value.parameters.valueRef;
    selectedStep.value.parameters.value = "";
  }
};

const setWaitSource = (event: Event) => {
  if (!selectedStep.value) return;
  if (eventValue(event) === "selector") {
    delete selectedStep.value.parameters.durationMs;
    selectedStep.value.parameters.selector = "";
    selectedStep.value.parameters.state = "visible";
  } else {
    delete selectedStep.value.parameters.selector;
    delete selectedStep.value.parameters.state;
    selectedStep.value.parameters.durationMs = 1000;
  }
};

const stepSummary = (step: WorkflowStep) => {
  const text = (key: string) => String(step.parameters[key] ?? "").trim();
  switch (step.action) {
    case "navigate": return text("url") || "配置目标网址";
    case "click": return text("selector") || "配置元素选择器";
    case "input": return text("selector") || "配置输入框选择器";
    case "wait": return step.parameters.durationMs ? `等待 ${step.parameters.durationMs} ms` : text("selector") || "配置等待条件";
    case "upload": return text("artifactRef") || "配置文件引用";
    case "javascript": return text("script") ? "已配置页面脚本" : "配置页面脚本";
    case "screenshot": return text("name") || "配置输出名称";
    case "extract": return text("storeAs") ? `保存到 ${text("storeAs")}` : "配置采集结果";
    case "close": return "结束当前浏览器会话";
  }
};

const cloneSteps = (value: WorkflowStep[]) => value.map((step) => ({
  ...step,
  parameters: { ...(step.parameters || {}) },
}));

const hydrate = async () => {
  hydrating.value = true;
  loading.value = !isNew.value;
  errorMessage.value = "";
  successMessage.value = "";
  try {
    if (isNew.value) {
      workflow.value = undefined;
      name.value = "";
      engine.value = "playwright";
      steps.value = [];
      stepSequence = 0;
      addStep("navigate");
    } else {
      const workflowID = String(route.params.workflowId || "");
      const current = await api.get<Workflow>(`${session.workspaceBase}/workflows/${workflowID}`);
      const version = await api.get<WorkflowVersion>(`${session.workspaceBase}/workflows/${workflowID}/versions/${current.latestVersion}`);
      workflow.value = current;
      name.value = current.name;
      engine.value = version.definition.engine;
      steps.value = cloneSteps(version.definition.steps);
      stepSequence = steps.value.length;
      selectedStepId.value = steps.value[0]?.id || "";
    }
    await nextTick();
    dirty.value = false;
  } catch (error) {
    errorMessage.value = readableError(error);
  } finally {
    loading.value = false;
    hydrating.value = false;
  }
};

const validReference = (value: string) => /^(account-secret|artifact|variable):\/\/.+/.test(value);
const validate = () => {
  if (isNew.value && (!name.value.trim() || name.value.trim().length > 120)) return "请输入不超过 120 个字符的工作流名称。";
  if (!steps.value.length) return "工作流至少需要一个步骤。";
  for (let index = 0; index < steps.value.length; index += 1) {
    const step = steps.value[index];
    const label = `步骤 ${index + 1}（${actionInfo(step.action).label}）`;
    const text = (key: string) => String(step.parameters[key] ?? "").trim();
    if ((step.timeoutMs || 0) < 100 || (step.timeoutMs || 0) > 600000) return `${label}的超时必须在 100 到 600000 毫秒之间。`;
    if (step.action === "navigate" && !/^https?:\/\//i.test(text("url"))) return `${label}需要有效的 HTTP(S) 网址。`;
    if (["click", "input", "upload", "extract"].includes(step.action) && !text("selector")) return `${label}需要元素选择器。`;
    if (step.action === "input" && inputReferenceFor(step) && !validReference(text("valueRef"))) return `${label}需要有效的安全值引用。`;
    if (step.action === "wait" && step.parameters.durationMs !== undefined) {
      const duration = Number(step.parameters.durationMs);
      if (!Number.isInteger(duration) || duration < 1 || duration > 300000) return `${label}的等待时长必须在 1 到 300000 毫秒之间。`;
    }
    if (step.action === "wait" && step.parameters.selector !== undefined && !text("selector")) return `${label}需要等待元素选择器。`;
    if (step.action === "upload" && !validReference(text("artifactRef"))) return `${label}需要有效的文件引用。`;
    if (step.action === "javascript" && !text("script")) return `${label}需要 JavaScript 内容。`;
    if (step.action === "screenshot" && !text("name")) return `${label}需要输出名称。`;
    if (step.action === "extract" && !text("storeAs")) return `${label}需要结果变量名。`;
  }
  return "";
};

const inputReferenceFor = (step: WorkflowStep) => step.parameters.valueRef !== undefined;
const definition = (): WorkflowDefinition => ({ schemaVersion: "ant-workflow/v1", engine: engine.value, steps: cloneSteps(steps.value) });
const readableError = (error: unknown) => error instanceof Error ? error.message : "操作失败，请稍后重试。";

const saveWorkflow = async (): Promise<boolean> => {
  if (saving.value) return false;
  const validation = validate();
  if (validation) { errorMessage.value = validation; successMessage.value = ""; return false; }
  if (!isNew.value && !dirty.value) return true;
  saving.value = true;
  errorMessage.value = "";
  successMessage.value = "";
  try {
    let result: WorkflowMutationResult;
    if (isNew.value) {
      result = await api.post<WorkflowMutationResult>(`${session.workspaceBase}/workflows`, { name: name.value.trim(), definition: definition() });
    } else {
      if (!workflow.value) return false;
      result = await api.post<WorkflowMutationResult>(`${session.workspaceBase}/workflows/${workflow.value.id}/versions`, { expectedVersion: workflow.value.version, definition: definition() });
    }
    workflow.value = result.workflow;
    dirty.value = false;
    successMessage.value = `版本 v${result.version.version} 已安全保存。`;
    if (isNew.value) await router.replace({ name: "automation-edit", params: { workflowId: result.workflow.id } });
    return true;
  } catch (error) {
    errorMessage.value = readableError(error);
    return false;
  } finally { saving.value = false; }
};

const publishWorkflow = async () => {
  if (publishing.value) return;
  if (!await saveWorkflow() || !workflow.value) return;
  publishing.value = true;
  errorMessage.value = "";
  try {
    workflow.value = await api.post<Workflow>(`${session.workspaceBase}/workflows/${workflow.value.id}/publish`, { expectedVersion: workflow.value.version, workflowVersion: workflow.value.latestVersion });
    successMessage.value = `版本 v${workflow.value.latestVersion} 已发布，可用于任务和批量运行。`;
  } catch (error) {
    errorMessage.value = readableError(error);
  } finally { publishing.value = false; }
};

watch([name, engine, steps], () => { if (!hydrating.value) { dirty.value = true; successMessage.value = ""; } }, { deep: true });
watch(() => route.params.workflowId, () => void hydrate());
onMounted(() => void hydrate());
onBeforeRouteLeave(() => !dirty.value || window.confirm("当前工作流有尚未保存的修改，确定离开吗？"));
</script>

<style scoped>
.workflow-builder-page{max-width:1680px}.builder-success{display:flex;align-items:center;gap:8px;padding:11px 13px;border:1px solid #ccebdc;border-radius:9px;background:#effaf4;color:#117b4b}.builder-loading{min-height:260px;display:flex;align-items:center;justify-content:center;gap:9px;color:var(--muted)}.builder-layout{min-height:640px;display:grid;grid-template-columns:240px minmax(390px,1fr) 320px;gap:14px;align-items:start}.builder-palette,.builder-inspector{position:sticky;top:0;max-height:calc(100vh - 142px);overflow:auto}.builder-panel-heading{min-height:64px;display:flex;align-items:center;justify-content:space-between;gap:12px;padding:14px 16px;border-bottom:1px solid var(--line)}.builder-panel-heading h2{margin:0;font-size:15px}.builder-panel-heading .page-eyebrow{margin-bottom:4px}.builder-panel-heading>span{color:var(--muted);font-size:12px}.builder-panel-heading code{max-width:112px;overflow:hidden;text-overflow:ellipsis;color:var(--muted);font-size:11px}.builder-panel-copy{margin:0;padding:13px 16px;color:var(--muted);font-size:12px;line-height:1.55}.action-palette{display:grid;padding:0 8px 10px}.action-palette button{display:grid;grid-template-columns:34px minmax(0,1fr) 16px;align-items:center;gap:9px;padding:9px 8px;border-radius:9px;text-align:left}.action-palette button:hover{background:#f1f5fa}.action-palette button>svg{color:#98a4b7}.action-palette button span:nth-child(2){min-width:0;display:grid;gap:2px}.action-palette small{overflow:hidden;color:var(--muted);font-size:11px;text-overflow:ellipsis;white-space:nowrap}.action-icon{width:34px;height:34px;display:flex;align-items:center;justify-content:center;border-radius:9px;background:#edf4ff;color:var(--blue)}.builder-canvas{min-height:640px}.canvas-heading{padding-inline:20px}.workflow-basics{display:grid;grid-template-columns:minmax(0,1.4fr) minmax(190px,.6fr);gap:14px;padding:18px 20px;border-bottom:1px solid var(--line)}.workflow-basics input:disabled{background:#f4f6f9;color:#65738a}.step-sequence{display:grid;gap:10px;padding:20px}.step-card{position:relative;display:grid;grid-template-columns:minmax(0,1fr) auto;align-items:center;border:1px solid var(--line);border-radius:11px;background:#fff;transition:.15s}.step-card::after{position:absolute;bottom:-11px;left:33px;width:1px;height:10px;background:var(--line-strong);content:""}.step-card:last-child::after{display:none}.step-card.selected{border-color:#9fc0fb;box-shadow:0 0 0 3px rgba(52,120,246,.08)}.step-select{min-width:0;display:grid;grid-template-columns:28px 38px minmax(0,1fr);align-items:center;gap:10px;padding:13px;text-align:left}.step-number{width:24px;height:24px;display:flex;align-items:center;justify-content:center;border-radius:999px;background:#eef2f7;color:#68778e;font-size:11px;font-weight:750}.step-action-icon{width:38px;height:38px;display:flex;align-items:center;justify-content:center;border-radius:9px;background:#f2f5f9;color:#53647c}.step-copy{min-width:0;display:grid;gap:4px}.step-copy small{overflow:hidden;color:var(--muted);font-size:12px;text-overflow:ellipsis;white-space:nowrap}.step-actions{display:flex;gap:2px;padding-right:10px}.step-actions button{width:30px;height:30px;display:flex;align-items:center;justify-content:center;border-radius:7px;color:#748299}.step-actions button:hover:not(:disabled){background:#eef2f7;color:var(--text)}.step-actions button.danger:hover{background:#fff0f0;color:var(--red)}.step-actions button:disabled{opacity:.3;cursor:not-allowed}.builder-empty{min-height:470px;display:flex;flex-direction:column;align-items:center;justify-content:center;padding:35px;text-align:center}.builder-empty>span{width:48px;height:48px;display:flex;align-items:center;justify-content:center;border-radius:13px;background:#edf4ff;color:var(--blue)}.builder-empty h3{margin:13px 0 5px}.builder-empty p{max-width:390px;margin:0 0 16px;color:var(--muted);line-height:1.6}.inspector-form{display:grid;gap:15px;padding:16px}.inspector-form textarea{padding-block:10px;resize:vertical;font-family:"Cascadia Code",Consolas,monospace;line-height:1.5}.check-field{display:flex;align-items:center;gap:8px;color:#4f5e75;font-size:12px}.check-field input{width:16px;height:16px;accent-color:var(--blue)}.inspector-note,.close-step-note{display:flex;align-items:flex-start;gap:9px;padding:10px;border-radius:9px;background:#f5f8fd;color:#5f7089;font-size:12px;line-height:1.55}.inspector-note{margin:0}.close-step-note p{display:grid;gap:3px;margin:0}.close-step-note span{color:var(--muted)}.inspector-divider{height:1px;margin-block:2px;background:var(--line)}.inspector-empty{min-height:420px;display:flex;flex-direction:column;align-items:center;justify-content:center;padding:30px;text-align:center;color:var(--muted)}.inspector-empty>svg{width:28px;height:28px}.inspector-empty p{max-width:210px;line-height:1.6}.button svg{width:16px}.tag{text-transform:capitalize}@media(max-width:1320px){.builder-layout{grid-template-columns:210px minmax(360px,1fr) 290px}.workflow-basics{grid-template-columns:1fr}}@media(max-width:1080px){.builder-layout{grid-template-columns:220px minmax(0,1fr)}.builder-inspector{position:static;grid-column:1/-1;max-height:none}.builder-palette{top:0}.inspector-form{grid-template-columns:repeat(2,minmax(0,1fr))}.inspector-divider,.inspector-note,.close-step-note{grid-column:1/-1}} 
</style>
