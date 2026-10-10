import { test } from 'node:test';
import * as assert from 'node:assert/strict';
import * as os from 'node:os';
import * as path from 'node:path';
import { binaryName, expandPath, resolveServer } from '../server';

const ext = path.join('/ext');

test('binary name per platform', () => {
  assert.equal(binaryName('darwin'), 'bem-ls');
  assert.equal(binaryName('win32'), 'bem-ls.exe');
});

test('a configured path wins and is expanded', () => {
  const r = resolveServer({
    configured: '${workspaceFolder}/tools/bem-ls',
    workspaceFolder: '/ws',
    extensionPath: ext,
    exists: (p) => p === '/ws/tools/bem-ls' || p === path.join(ext, 'bin', 'bem-ls'),
    platform: 'linux',
  });
  assert.deepEqual(r, { command: '/ws/tools/bem-ls', source: 'setting' });
  assert.equal(expandPath('~/x'), path.join(os.homedir(), 'x'));
});

test('a configured path that does not exist is an error', () => {
  assert.throws(
    () => resolveServer({ configured: '/nope', extensionPath: ext, exists: () => false, platform: 'linux' }),
    /does not exist/,
  );
});

test('the bundled binary comes before PATH', () => {
  const bundled = path.join(ext, 'bin', 'bem-ls');
  const r = resolveServer({ extensionPath: ext, envPath: '/usr/bin', exists: (p) => p === bundled || p === '/usr/bin/bem-ls', platform: 'linux' });
  assert.deepEqual(r, { command: bundled, source: 'bundled' });
});

test('PATH is searched with the platform separator', () => {
  const r = resolveServer({
    extensionPath: ext,
    envPath: 'C:\\a;C:\\tools',
    exists: (p) => p === path.join('C:\\tools', 'bem-ls.exe'),
    platform: 'win32',
  });
  assert.equal(r?.source, 'path');
  assert.equal(resolveServer({ extensionPath: ext, envPath: '', exists: () => false, platform: 'linux' }), undefined);
});

test('package.json settings match the schema', async () => {
  const { settingsFromSchema } = await import(path.join(__dirname, '..', '..', 'scripts', 'sync-settings.mjs'));
  const fs = await import('node:fs');
  const schema = JSON.parse(fs.readFileSync(path.join(__dirname, '..', '..', '..', '..', 'schema', 'bem.config.schema.json'), 'utf8'));
  const pkg = JSON.parse(fs.readFileSync(path.join(__dirname, '..', '..', 'package.json'), 'utf8'));
  assert.deepEqual(pkg.contributes.configuration.properties, settingsFromSchema(schema), 'run npm run sync-settings');
});
