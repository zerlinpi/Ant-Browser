'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { runWorkflow } = require('./runner_workflow.cjs');

function fixture(overrides = {}) {
  const calls = [];
  let routeHandler;
  let routeRemoved = false;
  const locator = {
    click: async () => calls.push('click'), fill: async value => calls.push(['fill', value]),
    pressSequentially: async value => calls.push(['append', value]),
    waitFor: async () => calls.push('wait'), setInputFiles: async value => calls.push(['upload', value]),
    textContent: async () => 'extracted', getAttribute: async () => 'attribute',
    evaluateAll: async () => ['one', 'two'],
  };
  const page = { locator: () => locator, goto: async url => calls.push(['navigate', url]),
    evaluate: async () => calls.push('javascript'), screenshot: async options => calls.push(['screenshot', options.path]) };
  const context = {
    newPage: async () => page,
    route: async (_pattern, handler) => { routeHandler = handler; },
    unroute: async (_pattern, handler) => { assert.equal(handler, routeHandler); routeRemoved = true; },
  };
  const api = {
    useBrowser: async () => ({ page, context, browser: { newBrowserCDPSession: async () => ({ send: async method => calls.push(method) }) } }),
    artifact: name => `/artifacts/${name}`, ...overrides,
  };
  return { api, calls, page, locator, routeHandler: () => routeHandler, routeRemoved: () => routeRemoved };
}
const definition = steps => ({ schemaVersion: 'ant-workflow/v1', engine: 'playwright', steps });
const step = (id, action, parameters = {}, extra = {}) => ({ id, action, parameters, ...extra });

test('executes all actions sequentially and keeps extracted values in execution scope', async () => {
  const f = fixture({ resolveArtifact: async ref => { assert.equal(ref, 'artifact://file'); return '/authorized/file'; } });
  const result = await runWorkflow(definition([
    step('open', 'navigate', { url: 'https://example.test' }),
    step('click', 'click', { selector: '#button' }),
    step('extract', 'extract', { selector: '#result', storeAs: 'value' }),
    step('input', 'input', { selector: '#input', valueRef: 'variable://value' }),
    step('wait', 'wait', { selector: '#ready' }),
    step('upload', 'upload', { selector: '#upload', artifactRef: 'artifact://file' }),
    step('script', 'javascript', { script: 'return args', arguments: [1] }),
    step('capture', 'screenshot', { name: 'capture' }),
    step('close', 'close'),
  ]), f.api);
  assert.equal(result.ok, true);
  assert.equal(result.steps.length, 9);
  assert.deepEqual(f.calls, [['navigate', 'https://example.test/'], 'click', ['fill', 'extracted'], 'wait', ['upload', '/authorized/file'], 'javascript', ['screenshot', '/artifacts/capture.png'], 'Browser.close']);
  assert.equal(JSON.stringify(result).includes('extracted'), false);
  assert.equal(f.routeRemoved(), true);
});

test('rejects unsupported engines and invalid step graph before launching', async () => {
  let launched = 0;
  const api = { useBrowser: async () => { launched++; } };
  for (const engine of ['unknown']) {
    await assert.rejects(runWorkflow({ ...definition([step('close', 'close')]), engine }, api), /engine_unavailable/);
  }
  await assert.rejects(runWorkflow(definition([step('close', 'close'), step('later', 'wait', { durationMs: 1 })]), api), /close_must_be_last/);
  assert.equal(launched, 0);
});

test('executes the Puppeteer engine through its page and CDP session APIs', async () => {
  const calls = [];
  let requestHandler;
  let requestedEngine;
  const uploadHandle = { uploadFile: async value => calls.push(['upload', value]) };
  const page = {
    setRequestInterception: async enabled => calls.push(['interception', enabled]),
    on: (event, handler) => { if (event === 'request') requestHandler = handler; },
    off: (event, handler) => { assert.equal(event, 'request'); assert.equal(handler, requestHandler); },
    goto: async (url, options) => calls.push(['navigate', url, options.waitUntil]),
    waitForSelector: async (selector, options) => { calls.push(['wait', selector, options]); return selector === '#upload' ? uploadHandle : {}; },
    click: async (selector, options) => calls.push(['click', selector, options.button]),
    $eval: async selector => calls.push(['clear', selector]),
    type: async (selector, value) => calls.push(['type', selector, value]),
    evaluate: async (_fn, value) => {
      if (value && value.query) return 'puppeteer-value';
      calls.push('javascript');
      return null;
    },
    $$eval: async () => ['one', 'two'],
    screenshot: async options => calls.push(['screenshot', options.path]),
  };
  const browser = {
    target: () => ({ createCDPSession: async () => ({ send: async method => calls.push(method) }) }),
  };
  const api = {
    useBrowser: async options => { requestedEngine = options.engine; return { engine: 'puppeteer', page, browser, context: {} }; },
    resolveArtifact: async () => '/authorized/file',
    artifact: name => `/artifacts/${name}`,
  };
  const result = await runWorkflow({ ...definition([
    step('open', 'navigate', { url: 'https://example.test/', waitUntil: 'networkidle' }),
    step('click', 'click', { selector: '#button' }),
    step('input', 'input', { selector: '#input', value: 'safe' }),
    step('wait', 'wait', { selector: '#ready', state: 'visible' }),
    step('upload', 'upload', { selector: '#upload', artifactRef: 'artifact://file' }),
    step('script', 'javascript', { script: 'return args[0]', arguments: [1] }),
    step('extract', 'extract', { selector: '#result', storeAs: 'value' }),
    step('capture', 'screenshot', { name: 'capture' }),
    step('close', 'close'),
  ]), engine: 'puppeteer' }, api);
  assert.equal(result.ok, true);
  assert.equal(requestedEngine, 'puppeteer');
  assert.equal(typeof requestHandler, 'function');
  assert.equal(calls.some(call => Array.isArray(call) && call[0] === 'navigate' && call[2] === 'networkidle0'), true);
  assert.equal(calls.some(call => Array.isArray(call) && call[0] === 'upload' && call[1] === '/authorized/file'), true);
  assert.equal(calls.includes('Browser.close'), true);
  assert.equal(JSON.stringify(result).includes('puppeteer-value'), false);
  assert.deepEqual(calls.at(-1), ['interception', false]);
});

test('executes the CDP engine through raw protocol commands', async t => {
  const f = fixture();
  const protocol = [];
  const session = {
    send: async (method, parameters = {}) => {
      protocol.push([method, parameters]);
      if (method === 'Runtime.evaluate') {
        if (String(parameters.expression).includes('textContent')) return { result: { value: 'cdp-value' } };
        return { result: { value: true } };
      }
      if (method === 'DOM.getDocument') return { root: { nodeId: 1 } };
      if (method === 'DOM.querySelector') return { nodeId: 2 };
      if (method === 'Page.captureScreenshot') return { data: Buffer.from('image').toString('base64') };
      return {};
    },
  };
  f.api.useBrowser = async () => ({
    page: f.page,
    context: {
      route: async (_pattern, handler) => { await f.api.captureRoute(handler); },
      unroute: async () => {},
      newCDPSession: async () => session,
    },
    browser: { newBrowserCDPSession: async () => ({ send: async method => f.calls.push(method) }) },
  });
  let capturedRoute;
  f.api.captureRoute = async handler => { capturedRoute = handler; };
  const artifactRoot = fs.mkdtempSync(path.join(os.tmpdir(), 'ant-cdp-workflow-'));
  t.after(() => fs.rmSync(artifactRoot, { recursive: true, force: true }));
  f.api.artifact = name => path.join(artifactRoot, name);
  f.api.resolveArtifact = async () => path.join(artifactRoot, 'upload.txt');
  const result = await runWorkflow({ ...definition([
    step('open', 'navigate', { url: 'https://example.test/', waitUntil: 'commit' }),
    step('click', 'click', { selector: '#button' }),
    step('input', 'input', { selector: '#input', value: 'safe' }),
    step('wait', 'wait', { selector: '#ready', state: 'attached' }),
    step('upload', 'upload', { selector: '#upload', artifactRef: 'artifact://file' }),
    step('script', 'javascript', { script: 'return args[0]', arguments: [1] }),
    step('extract', 'extract', { selector: '#result', storeAs: 'value' }),
    step('capture', 'screenshot', { name: 'capture' }),
    step('close', 'close'),
  ]), engine: 'cdp' }, f.api);
  assert.equal(result.ok, true);
  assert.equal(typeof capturedRoute, 'function');
  const methods = protocol.map(([method]) => method);
  for (const method of ['Runtime.enable', 'Page.enable', 'DOM.enable', 'Page.navigate', 'Runtime.evaluate', 'DOM.setFileInputFiles', 'Page.captureScreenshot']) {
    assert.equal(methods.includes(method), true, method);
  }
  assert.equal(fs.readFileSync(path.join(artifactRoot, 'capture.png'), 'utf8'), 'image');
  assert.deepEqual(f.calls, ['Browser.close']);
  assert.equal(JSON.stringify(result).includes('cdp-value'), false);
});

test('redacts errors and honors continueOnError without exposing secrets', async () => {
  const f = fixture();
  f.locator.click = async () => { throw new Error('password=PRIVATE'); };
  const result = await runWorkflow(definition([
    step('fail', 'click', { selector: '#button' }, { continueOnError: true }),
    step('next', 'input', { selector: '#input', value: 'safe' }),
  ]), f.api);
  assert.equal(result.ok, true);
  assert.equal(result.hadErrors, true);
  assert.equal(JSON.stringify(result).includes('PRIVATE'), false);
  assert.deepEqual(f.calls, [['fill', 'safe']]);
});

test('missing secret resolver, unsafe URL and path fail without performing action', async () => {
  for (const action of [step('secret', 'input', { selector: '#password', valueRef: 'account-secret://id' }),
    step('url', 'navigate', { url: 'file:///etc/passwd' }), step('path', 'screenshot', { name: '../escape' })]) {
    const f = fixture();
    const result = await runWorkflow(definition([action]), f.api);
    assert.equal(result.ok, false);
    assert.deepEqual(f.calls, []);
  }
});

test('blocks local network navigation and page-initiated requests', async () => {
  for (const url of ['http://127.0.0.1:19876/api/launch', 'http://localhost/api', 'http://10.0.0.1/', 'http://[::1]/']) {
    const f = fixture();
    const result = await runWorkflow(definition([step('url', 'navigate', { url })]), f.api);
    assert.equal(result.ok, false);
    assert.deepEqual(f.calls, []);
  }

  const f = fixture();
  const result = await runWorkflow(definition([step('open', 'navigate', { url: 'https://example.test/' })]), f.api);
  assert.equal(result.ok, true);
  const route = f.routeHandler();
  assert.equal(typeof route, 'function');
  assert.equal(f.routeRemoved(), true);
  for (const [url, expected] of [['http://127.0.0.1/private', 'abort'], ['http://192.168.1.1/', 'abort'], ['https://example.test/api', 'continue']]) {
    let action = '';
    await route({
      request: () => ({ url: () => url }),
      abort: async () => { action = 'abort'; },
      continue: async () => { action = 'continue'; },
    });
    assert.equal(action, expected, url);
  }
});

test('fails closed when browser context cannot install a network guard', async () => {
  const f = fixture({ useBrowser: async () => ({ page: fixture().page, browser: {} }) });
  const result = await runWorkflow(definition([step('open', 'navigate', { url: 'https://example.test/' })]), f.api);
  assert.equal(result.ok, false);
  assert.deepEqual(f.calls, []);
});

test('timeout is terminal even with continueOnError and suppresses delayed input', async () => {
  let resolve;
  const f = fixture({ resolveValue: () => new Promise(done => { resolve = done; }) });
  const result = await runWorkflow(definition([
    step('slow', 'input', { selector: '#input', valueRef: 'account-secret://id' }, { timeoutMs: 100, continueOnError: true }),
    step('later', 'click', { selector: '#button' }),
  ]), f.api);
  assert.equal(result.error, 'workflow_step_timeout');
  resolve('PRIVATE');
  await new Promise(done => setImmediate(done));
  assert.deepEqual(f.calls, ['Browser.close']);
  assert.equal(f.routeRemoved(), true);
});

test('timeout closes the browser so page JavaScript cannot continue after guard removal', async () => {
  const f = fixture();
  f.page.evaluate = () => new Promise(() => {});
  const result = await runWorkflow(definition([
    step('script', 'javascript', { script: 'fetch("http://127.0.0.1/private")' }, { timeoutMs: 100 }),
  ]), f.api);
  assert.equal(result.error, 'workflow_step_timeout');
  assert.deepEqual(f.calls, ['Browser.close']);
  assert.equal(f.routeRemoved(), true);
});

test('cancellation prevents later steps', async () => {
  const controller = new AbortController();
  const f = fixture({ signal: controller.signal });
  controller.abort();
  const result = await runWorkflow(definition([step('first', 'click', { selector: '#button' })]), f.api);
  assert.equal(result.error, 'workflow_cancelled');
  assert.deepEqual(f.calls, []);
});
