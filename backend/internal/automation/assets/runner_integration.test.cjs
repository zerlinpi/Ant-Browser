'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const http = require('node:http');
const { execFile } = require('node:child_process');
const { promisify } = require('node:util');
const execute = promisify(execFile);

// Exercise the real subprocess entry point, not just the interpreter's injected
// API. This catches lost engine metadata and accidental eager dependency loads.
for (const engine of ['playwright', 'puppeteer']) {
  test(`runner connects and disconnects ${engine} without closing the managed browser`, async t => {
    const root = fs.mkdtempSync(path.join(os.tmpdir(), 'ant-runner-test-'));
    t.after(() => fs.rmSync(root, { recursive: true, force: true }));
    const moduleDir = name => {
      const dir = path.join(root, 'node_modules', name);
      fs.mkdirSync(dir, { recursive: true });
      return dir;
    };
    fs.writeFileSync(path.join(moduleDir('playwright-core'), 'index.js'), `
      const fs = require('node:fs');
      const page = { goto: async () => {} };
      const context = { pages: () => [page], route: async () => {}, unroute: async () => {} };
      exports.chromium = { connectOverCDP: async () => ({
        contexts: () => [context],
        close: async () => fs.writeFileSync(${JSON.stringify(path.join(root, 'disconnected'))}, 'playwright')
      }) };
    `);
    if (engine === 'puppeteer') {
      fs.writeFileSync(path.join(moduleDir('puppeteer-core'), 'index.js'), `
        const fs = require('node:fs');
        const page = { goto: async () => {}, setRequestInterception: async () => {}, on: () => {}, off: () => {} };
        exports.connect = async () => ({
          browserContexts: () => [{}], pages: async () => [page],
          close: async () => { throw new Error('must not close managed browser'); },
          disconnect: () => fs.writeFileSync(${JSON.stringify(path.join(root, 'disconnected'))}, 'puppeteer')
        });
      `);
    }
    const server = http.createServer((req, res) => {
      assert.equal(req.url, '/api/launch');
      res.setHeader('Content-Type', 'application/json');
      res.end(JSON.stringify({ ok: true, cdpUrl: 'http://127.0.0.1:9222' }));
    });
    await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
    t.after(() => new Promise(resolve => server.close(resolve)));
    const payload = path.join(root, 'payload.json');
    fs.writeFileSync(payload, JSON.stringify({
      runtimeDir: root, taskType: 'workflow', artifactDir: path.join(root, 'artifacts'),
      launchBaseUrl: `http://127.0.0.1:${server.address().port}`,
      workflow: { schemaVersion: 'ant-workflow/v1', engine, steps: [
        { id: 'open', action: 'navigate', parameters: { url: 'https://example.test/' } },
      ] },
    }));
    const { stdout } = await execute(process.execPath, [path.join(__dirname, 'runner.cjs'), payload], { timeout: 10000 });
    const result = JSON.parse(stdout);
    assert.equal(result.ok, true, JSON.stringify(result));
    assert.equal(fs.readFileSync(path.join(root, 'disconnected'), 'utf8'), engine);
  });
}
