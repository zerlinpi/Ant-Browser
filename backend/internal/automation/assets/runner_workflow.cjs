'use strict';

const fs = require('node:fs');
const net = require('node:net');
const { setTimeout: delay } = require('node:timers/promises');
const actions = new Set(['navigate', 'click', 'input', 'wait', 'upload', 'javascript', 'screenshot', 'extract', 'close']);

function isPrivateIPv4(address) {
  const parts = address.split('.').map(part => Number(part));
  if (parts.length !== 4 || parts.some(part => !Number.isInteger(part) || part < 0 || part > 255)) return true;
  const [a, b] = parts;
  return a === 0 || a === 10 || a === 127 ||
    (a === 100 && b >= 64 && b <= 127) ||
    (a === 169 && b === 254) ||
    (a === 172 && b >= 16 && b <= 31) ||
    (a === 192 && (b === 0 || b === 168)) ||
    (a === 198 && (b === 18 || b === 19)) ||
    a >= 224;
}

function isLocalNetworkHostname(hostname) {
  const host = String(hostname || '').trim().toLowerCase().replace(/^\[|\]$/g, '');
  if (!host || host === 'localhost' || host.endsWith('.localhost') || host.endsWith('.local') ||
      host.endsWith('.internal') || host.endsWith('.home.arpa') || !host.includes('.')) return true;
  const family = net.isIP(host);
  if (family === 4) return isPrivateIPv4(host);
  if (family === 6) {
    return host === '::' || host === '::1' || host.startsWith('::') || host.startsWith('fc') ||
      host.startsWith('fd') || /^fe[89ab]/.test(host) || /^fe[c-f]/.test(host) || host.startsWith('ff');
  }
  return false;
}

function parseNavigationURL(value) {
  let url;
  try {
    url = new URL(value);
  } catch {
    throw new Error('invalid_navigation_url');
  }
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || value.includes('{{')) {
    throw new Error('invalid_navigation_url');
  }
  if (isLocalNetworkHostname(url.hostname)) throw new Error('local_network_access_denied');
  return url;
}

function shouldBlockBrowserRequest(value) {
  let url;
  try {
    url = new URL(value);
  } catch {
    return true;
  }
  if (url.protocol === 'file:' || url.protocol === 'ftp:') return true;
  return ['http:', 'https:'].includes(url.protocol) && isLocalNetworkHostname(url.hostname);
}

async function installNetworkGuard(connection) {
  if (connection && connection.engine === 'puppeteer') {
    const page = connection.page;
    if (!page) return async () => {};
    if (typeof page.setRequestInterception !== 'function' || typeof page.on !== 'function') {
      throw new Error('workflow_network_guard_unavailable');
    }
    const handler = async request => {
      try {
        if (shouldBlockBrowserRequest(request.url())) await request.abort('blockedbyclient');
        else await request.continue();
      } catch {
        // Navigation may finish while a cancellation or timeout is being handled.
      }
    };
    await page.setRequestInterception(true);
    page.on('request', handler);
    return async () => {
      if (typeof page.off === 'function') page.off('request', handler);
      await page.setRequestInterception(false).catch(() => {});
    };
  }
  const context = connection && connection.context;
  if (!context || typeof context.route !== 'function' || typeof context.unroute !== 'function') {
    throw new Error('workflow_network_guard_unavailable');
  }
  const handler = async route => {
    if (shouldBlockBrowserRequest(route.request().url())) {
      await route.abort('blockedbyclient');
      return;
    }
    await route.continue();
  };
  await context.route('**/*', handler);
  return async () => {
    try {
      await context.unroute('**/*', handler);
    } catch {
      // A close step may already have destroyed the persistent context.
    }
  };
}

async function terminateTimedOutConnection(connection) {
  if (connection && connection.engine === 'puppeteer' && connection.browser && typeof connection.browser.target === 'function') {
    try {
      const session = await connection.browser.target().createCDPSession();
      return await Promise.race([session.send('Browser.close').then(() => true), delay(2000, false)]);
    } catch {
      return false;
    }
  }
  if (!connection || !connection.browser || typeof connection.browser.newBrowserCDPSession !== 'function') return false;
  try {
    const close = (async () => {
      const session = await connection.browser.newBrowserCDPSession();
      await session.send('Browser.close');
      return true;
    })();
    return await Promise.race([close, delay(2000, false)]);
  } catch {
    return false;
  }
}

async function cdpEvaluate(session, expression, awaitPromise = false) {
  const response = await session.send('Runtime.evaluate', {
    expression,
    awaitPromise,
    returnByValue: true,
    userGesture: true,
  });
  if (response && response.exceptionDetails) throw new Error('cdp_evaluation_failed');
  return response && response.result ? response.result.value : undefined;
}

async function waitForCDP(session, expression, timeout) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    if (await cdpEvaluate(session, expression)) return;
    await delay(Math.min(50, Math.max(1, deadline - Date.now())));
  }
  throw new Error('cdp_wait_timeout');
}

async function getCDPSession(state, connection) {
  if (state.session) return state.session;
  if (!connection.page || !connection.context || typeof connection.context.newCDPSession !== 'function') {
    throw new Error('cdp_session_unavailable');
  }
  state.session = await connection.context.newCDPSession(connection.page);
  await state.session.send('Runtime.enable');
  await state.session.send('Page.enable');
  await state.session.send('DOM.enable');
  return state.session;
}

async function executeCDPStep(step, p, timeout, state, connection, api, helpers) {
  if (step.action === 'close') {
    const browserSession = await connection.browser.newBrowserCDPSession();
    await browserSession.send('Browser.close');
    return;
  }
  const session = await getCDPSession(state, connection);
  const selector = () => helpers.string(p.selector);
  switch (step.action) {
    case 'navigate': {
      const url = parseNavigationURL(helpers.string(p.url));
      const response = await session.send('Page.navigate', { url: url.href });
      if (response && response.errorText) throw new Error('cdp_navigation_failed');
      const waitUntil = p.waitUntil || 'domcontentloaded';
      if (waitUntil !== 'commit') {
        const readyState = waitUntil === 'load' || waitUntil === 'networkidle' ? 'complete' : 'interactive';
        await waitForCDP(session, `['${readyState}','complete'].includes(document.readyState)`, timeout);
        if (waitUntil === 'networkidle') await delay(500);
      }
      break;
    }
    case 'click': {
      const expression = `(() => { const el = document.querySelector(${JSON.stringify(selector())}); if (!el) throw new Error('missing'); el.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, view: window, button: ${p.button === 'right' ? 2 : p.button === 'middle' ? 1 : 0} })); return true; })()`;
      await cdpEvaluate(session, expression);
      break;
    }
    case 'input': {
      if (Object.hasOwn(p, 'value') === Object.hasOwn(p, 'valueRef')) throw new Error('invalid_input_value');
      const value = helpers.string(Object.hasOwn(p, 'valueRef') ? await helpers.reference(p.valueRef, 'value') : p.value);
      helpers.assertActive();
      const expression = `(() => { const el = document.querySelector(${JSON.stringify(selector())}); if (!el) throw new Error('missing'); const value = ${JSON.stringify(value)}; el.focus(); el.value = ${p.clear === false ? 'String(el.value || "") + value' : 'value'}; el.dispatchEvent(new Event('input', { bubbles: true })); el.dispatchEvent(new Event('change', { bubbles: true })); return true; })()`;
      await cdpEvaluate(session, expression);
      break;
    }
    case 'wait':
      if (Object.hasOwn(p, 'durationMs')) {
        if (!Number.isInteger(p.durationMs) || p.durationMs < 1 || p.durationMs > 300000) throw new Error('invalid_wait_duration');
        await delay(p.durationMs, undefined, { signal: helpers.signal });
      } else {
        const query = JSON.stringify(selector());
        const stateName = p.state || 'visible';
        const expressions = {
          attached: `Boolean(document.querySelector(${query}))`,
          detached: `!document.querySelector(${query})`,
          visible: `(() => { const el = document.querySelector(${query}); if (!el) return false; const s = getComputedStyle(el); const r = el.getBoundingClientRect(); return s.visibility !== 'hidden' && s.display !== 'none' && r.width > 0 && r.height > 0; })()`,
          hidden: `(() => { const el = document.querySelector(${query}); if (!el) return true; const s = getComputedStyle(el); const r = el.getBoundingClientRect(); return s.visibility === 'hidden' || s.display === 'none' || r.width === 0 || r.height === 0; })()`,
        };
        if (!expressions[stateName]) throw new Error('invalid_wait_state');
        await waitForCDP(session, expressions[stateName], timeout);
      }
      break;
    case 'upload': {
      const artifact = await helpers.reference(p.artifactRef, 'artifact');
      helpers.assertActive();
      const document = await session.send('DOM.getDocument', { depth: 0, pierce: true });
      const match = await session.send('DOM.querySelector', { nodeId: document.root.nodeId, selector: selector() });
      if (!match || !match.nodeId) throw new Error('cdp_selector_not_found');
      await session.send('DOM.setFileInputFiles', { nodeId: match.nodeId, files: [helpers.string(artifact)] });
      break;
    }
    case 'javascript': {
      const expression = `(async () => (new Function('args', ${JSON.stringify(helpers.string(p.script))}))(${JSON.stringify(p.arguments || [])}))()`;
      await cdpEvaluate(session, expression, true);
      break;
    }
    case 'screenshot': {
      const name = helpers.string(p.name);
      if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,119}$/.test(name) || name.includes('..')) throw new Error('invalid_artifact_name');
      const format = p.format || 'png';
      if (!['png', 'jpeg'].includes(format)) throw new Error('invalid_screenshot_format');
      const options = { format, captureBeyondViewport: p.fullPage === true, fromSurface: true };
      if (p.fullPage === true) {
        const metrics = await session.send('Page.getLayoutMetrics');
        const size = metrics.cssContentSize || metrics.contentSize;
        if (size) options.clip = { x: 0, y: 0, width: size.width, height: size.height, scale: 1 };
      }
      const capture = await session.send('Page.captureScreenshot', options);
      if (!capture || typeof capture.data !== 'string') throw new Error('cdp_screenshot_failed');
      fs.writeFileSync(api.artifact(`${name}.${format}`), Buffer.from(capture.data, 'base64'));
      break;
    }
    case 'extract': {
      const key = helpers.string(p.storeAs);
      const expression = p.multiple === true
        ? `Array.from(document.querySelectorAll(${JSON.stringify(selector())}), el => ${p.attribute ? `el.getAttribute(${JSON.stringify(helpers.string(p.attribute))})` : 'el.textContent'})`
        : `(() => { const el = document.querySelector(${JSON.stringify(selector())}); return el ? ${p.attribute ? `el.getAttribute(${JSON.stringify(helpers.string(p.attribute))})` : 'el.textContent'} : null; })()`;
      helpers.variables.set(key, await cdpEvaluate(session, expression));
      break;
    }
    default:
      throw new Error('unsupported_cdp_action');
  }
}

async function executePuppeteerStep(step, p, timeout, connection, api, helpers) {
  if (step.action === 'close') {
    const session = await connection.browser.target().createCDPSession();
    await session.send('Browser.close');
    return;
  }
  const page = connection.page;
  const selector = () => helpers.string(p.selector);
  const waitForTarget = async (state = 'attached') => {
    const options = { timeout };
    if (state === 'visible') options.visible = true;
    if (state === 'hidden' || state === 'detached') options.hidden = true;
    if (!['attached', 'detached', 'visible', 'hidden'].includes(state)) throw new Error('invalid_wait_state');
    return page.waitForSelector(selector(), options);
  };
  switch (step.action) {
    case 'navigate': {
      const url = parseNavigationURL(helpers.string(p.url));
      const requested = p.waitUntil || 'domcontentloaded';
      const waitUntil = requested === 'networkidle' ? 'networkidle0' : requested === 'commit' ? 'domcontentloaded' : requested;
      await page.goto(url.href, { timeout, waitUntil });
      break;
    }
    case 'click':
      await waitForTarget('visible');
      await page.click(selector(), { button: p.button || 'left' });
      break;
    case 'input': {
      if (Object.hasOwn(p, 'value') === Object.hasOwn(p, 'valueRef')) throw new Error('invalid_input_value');
      const value = helpers.string(Object.hasOwn(p, 'valueRef') ? await helpers.reference(p.valueRef, 'value') : p.value);
      helpers.assertActive();
      await waitForTarget('visible');
      if (p.clear !== false) {
        await page.$eval(selector(), element => {
          element.focus();
          element.value = '';
          element.dispatchEvent(new Event('input', { bubbles: true }));
        });
      }
      await page.type(selector(), value);
      break;
    }
    case 'wait':
      if (Object.hasOwn(p, 'durationMs')) {
        if (!Number.isInteger(p.durationMs) || p.durationMs < 1 || p.durationMs > 300000) throw new Error('invalid_wait_duration');
        await delay(p.durationMs, undefined, { signal: helpers.signal });
      } else await waitForTarget(p.state || 'visible');
      break;
    case 'upload': {
      const artifact = await helpers.reference(p.artifactRef, 'artifact');
      helpers.assertActive();
      const handle = await waitForTarget('attached');
      if (!handle || typeof handle.uploadFile !== 'function') throw new Error('puppeteer_upload_unavailable');
      await handle.uploadFile(helpers.string(artifact));
      break;
    }
    case 'javascript':
      await page.evaluate(({ source, args }) => (new Function('args', source))(args), { source: helpers.string(p.script), args: p.arguments || [] });
      break;
    case 'screenshot': {
      const name = helpers.string(p.name);
      if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,119}$/.test(name) || name.includes('..')) throw new Error('invalid_artifact_name');
      const type = p.format || 'png';
      if (!['png', 'jpeg'].includes(type)) throw new Error('invalid_screenshot_format');
      await page.screenshot({ path: api.artifact(`${name}.${type}`), type, fullPage: p.fullPage === true });
      break;
    }
    case 'extract': {
      const key = helpers.string(p.storeAs);
      const query = selector();
      const attribute = p.attribute ? helpers.string(p.attribute) : null;
      const value = p.multiple === true
        ? await page.$$eval(query, (elements, attr) => elements.map(element => attr ? element.getAttribute(attr) : element.textContent), attribute)
        : await page.evaluate(({ query, attr }) => {
            const element = document.querySelector(query);
            return element ? (attr ? element.getAttribute(attr) : element.textContent) : null;
          }, { query, attr: attribute });
      helpers.variables.set(key, value);
      break;
    }
    default:
      throw new Error('unsupported_puppeteer_action');
  }
}

function validateWorkflow(definition) {
  if (!definition || definition.schemaVersion !== 'ant-workflow/v1') throw new Error('unsupported_workflow_schema');
  // Never run a definition with a different engine than the selected runtime.
  if (!['playwright', 'puppeteer', 'cdp'].includes(definition.engine)) throw new Error('workflow_engine_unavailable');
  if (!Array.isArray(definition.steps) || definition.steps.length < 1 || definition.steps.length > 500) throw new Error('invalid_workflow_steps');
  const ids = new Set();
  for (const [index, step] of definition.steps.entries()) {
    if (!step || !/^[A-Za-z][A-Za-z0-9_-]{0,63}$/.test(step.id) || ids.has(step.id) || !actions.has(step.action)) throw new Error('invalid_workflow_step');
    ids.add(step.id);
    if (step.timeoutMs !== undefined && (!Number.isInteger(step.timeoutMs) || step.timeoutMs < 100 || step.timeoutMs > 600000)) throw new Error('invalid_workflow_timeout');
    if (step.action === 'close' && index !== definition.steps.length - 1) throw new Error('close_must_be_last');
    if (step.parameters != null && (typeof step.parameters !== 'object' || Array.isArray(step.parameters))) throw new Error('invalid_workflow_parameters');
  }
}

async function runWorkflow(definition, api) {
  validateWorkflow(definition);
  const variables = new Map();
  const steps = [];
  let connection;
  const cdpState = { session: null };
  let removeNetworkGuard;
  let retainNetworkGuard = false;
  let halted = false;
  const signal = api.signal;
  const assertActive = () => { if (halted || signal?.aborted) throw new Error('workflow_cancelled'); };
  const reference = async (ref, kind) => {
    if (typeof ref !== 'string') throw new Error('invalid_reference');
    if (kind === 'value' && ref.startsWith('variable://')) {
      const key = ref.slice('variable://'.length);
      if (!variables.has(key)) throw new Error('variable_not_found');
      return variables.get(key);
    }
    const resolver = kind === 'artifact' ? api.resolveArtifact : api.resolveValue;
    if (typeof resolver !== 'function') throw new Error('reference_resolver_unavailable');
    // Resolvers must authorize the reference against the execution workspace.
    return resolver(ref);
  };
  const string = (value) => {
    if (typeof value !== 'string') throw new Error('invalid_string_parameter');
    return value;
  };
  const execute = async (step) => {
    const p = step.parameters || {};
    const timeout = step.timeoutMs || 30000;
    assertActive();
    if (!connection) {
      connection = await api.useBrowser({ engine: definition.engine });
      if (definition.engine === 'puppeteer' && !connection.page && step.action !== 'close') {
        connection.page = await connection.browser.newPage();
      }
      removeNetworkGuard = await installNetworkGuard(connection);
    }
    assertActive();
    if (!connection.page && step.action !== 'close') connection.page = await connection.context.newPage();
    assertActive();
    if (definition.engine === 'cdp') {
      await executeCDPStep(step, p, timeout, cdpState, connection, api, { assertActive, reference, signal, string, variables });
      return;
    }
    if (definition.engine === 'puppeteer') {
      await executePuppeteerStep(step, p, timeout, connection, api, { assertActive, reference, signal, string, variables });
      return;
    }
    const page = connection.page;
    const locator = () => page.locator(string(p.selector));
    switch (step.action) {
      case 'navigate': {
        const rawURL = string(p.url);
        const url = parseNavigationURL(rawURL);
        await page.goto(url.href, { timeout, waitUntil: p.waitUntil || 'domcontentloaded' });
        break;
      }
      case 'click': await locator().click({ timeout, button: p.button || 'left' }); break;
      case 'input': {
        if (Object.hasOwn(p, 'value') === Object.hasOwn(p, 'valueRef')) throw new Error('invalid_input_value');
        const value = string(Object.hasOwn(p, 'valueRef') ? await reference(p.valueRef, 'value') : p.value);
        assertActive();
        if (p.clear === false) await locator().pressSequentially(value, { timeout });
        else await locator().fill(value, { timeout });
        break;
      }
      case 'wait':
        if (Object.hasOwn(p, 'durationMs')) {
          if (!Number.isInteger(p.durationMs) || p.durationMs < 1 || p.durationMs > 300000) throw new Error('invalid_wait_duration');
          await delay(p.durationMs, undefined, { signal });
        } else await locator().waitFor({ timeout, state: p.state || 'visible' });
        break;
      case 'upload': {
        const artifact = await reference(p.artifactRef, 'artifact');
        assertActive();
        await locator().setInputFiles(artifact, { timeout });
        break;
      }
      case 'javascript':
        // Source is evaluated only inside the browser page, never in Node.
        await page.evaluate(({ source, args }) => (new Function('args', source))(args), { source: string(p.script), args: p.arguments || [] });
        break;
      case 'screenshot': {
        const name = string(p.name);
        if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,119}$/.test(name) || name.includes('..')) throw new Error('invalid_artifact_name');
        const type = p.format || 'png';
        if (!['png', 'jpeg'].includes(type)) throw new Error('invalid_screenshot_format');
        await page.screenshot({ path: api.artifact(`${name}.${type}`), type, fullPage: p.fullPage === true, timeout });
        break;
      }
      case 'extract': {
        const key = string(p.storeAs);
        const target = locator();
        const value = p.multiple === true
          ? await target.evaluateAll((elements, attribute) => elements.map(el => attribute ? el.getAttribute(attribute) : el.textContent), p.attribute || null)
          : p.attribute ? await target.getAttribute(string(p.attribute), { timeout }) : await target.textContent({ timeout });
        variables.set(key, value);
        break;
      }
      case 'close': {
        const session = await connection.browser.newBrowserCDPSession();
        await session.send('Browser.close');
        break;
      }
    }
  };
  try {
    for (const step of definition.steps) {
      let timer;
      let onAbort;
      let terminal = false;
      try {
        assertActive();
        const guard = new Promise((_, reject) => {
          timer = setTimeout(() => { terminal = true; halted = true; reject(new Error('workflow_step_timeout')); }, step.timeoutMs || 30000);
          onAbort = () => { terminal = true; halted = true; reject(new Error('workflow_cancelled')); };
          signal?.addEventListener('abort', onAbort, { once: true });
        });
        await Promise.race([execute(step), guard]);
        steps.push({ id: step.id, status: 'succeeded' });
      } catch {
        // Browser errors may echo input values, URLs, script source or secrets.
        const code = signal?.aborted ? 'workflow_cancelled' : terminal ? 'workflow_step_timeout' : 'workflow_step_failed';
        steps.push({ id: step.id, status: 'failed', errorCode: code });
        // Never overlap another step with a timed-out or cancelled operation.
        if (terminal || signal?.aborted) {
          // Page JavaScript cannot be cancelled through Promise.race. Closing the
          // attached browser prevents timed-out code from continuing with local
          // network access after the temporary request guard is removed.
          retainNetworkGuard = !(await terminateTimedOutConnection(connection));
          return { ok: false, error: code, steps };
        }
        if (!step.continueOnError) return { ok: false, error: code, steps };
      } finally {
        clearTimeout(timer);
        if (onAbort) signal?.removeEventListener('abort', onAbort);
      }
    }
    return { ok: true, steps, hadErrors: steps.some(step => step.status === 'failed') };
  } finally {
    if (removeNetworkGuard && !retainNetworkGuard) await removeNetworkGuard();
  }
}

module.exports = { runWorkflow, validateWorkflow };
