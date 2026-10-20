const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const { inspectFiles, collectFiles, verifyPublicFixtures, exportSource } = require('../scripts/prepare-delivery.cjs');
const root = path.resolve(__dirname, '..');

function workspace() {
  const parent = path.join(root, 'bin', 'verification');
  fs.mkdirSync(parent, { recursive: true });
  // Go's ./... must not discover the intentionally minimal source.go fixtures.
  return fs.mkdtempSync(path.join(parent, '_delivery-test-'));
}

test('交付清单包含全部 Go 测试及固定来源，不依赖本地任务或实验目录', () => {
  verifyPublicFixtures(root);
  const files = collectFiles(root).map(item => item.path);
  for (const name of fs.readdirSync(root).filter(name => name.endsWith('.go'))) assert.ok(files.includes(name), name);
  assert.ok(files.includes('.env.example'));
  assert.ok(files.includes('.gitattributes'));
  for (const name of ['integration/reportadapter/adapter.go', 'cmd/adapt-report/main.go',
    'integration/upstream/contracts/v1.schema.json', 'integration/upstream/tools/check_quality.py',
    'scripts/test-integration.ps1', 'scripts/docker-smoke.py']) assert.ok(files.includes(name), name);
  assert.equal(files.filter(name => name.startsWith('testdata/public-courses/')).length, 8);
  assert.ok(files.every(name => !/^(?:data|bin|evaluation|docs\/history)\//.test(name)));
});

test('交付检查拒绝本地配置、越界路径及疑似密钥，不打印密钥内容', () => {
  const temp = workspace();
  const credential = ['sk', 'A'.repeat(32)].join('-');
  fs.writeFileSync(path.join(temp, 'example.txt'), credential);
  for (const name of ['.env', '.env.local', 'data/state.json', '../outside.txt', 'C:/outside.txt']) {
    assert.throws(() => inspectFiles(temp, [name]), /Local-only|Unsafe/);
  }
  assert.throws(() => inspectFiles(temp, ['example.txt']), error => /Possible credential/.test(error.message) && !error.message.includes(credential));
  fs.writeFileSync(path.join(temp, '.env.example'), 'DASHSCOPE_API_KEY=filled-example\n');
  assert.throws(() => inspectFiles(temp, ['.env.example']), /must be empty/);
});

test('交付检查拒绝目录链接，避免带入清单之外的文件', () => {
  const temp = workspace(), target = path.join(temp, 'target');
  fs.mkdirSync(target);
  fs.writeFileSync(path.join(target, 'fixture.txt'), 'public fixture');
  fs.symlinkSync(target, path.join(temp, 'linked'), process.platform === 'win32' ? 'junction' : 'dir');
  assert.throws(() => inspectFiles(temp, ['linked/fixture.txt']), /Linked/);
});

test('导出两次生成独立目录且内容、清单哈希一致，不复制本地数据', () => {
  const temp = workspace();
  fs.writeFileSync(path.join(temp, 'source.go'), 'package example\n');
  fs.writeFileSync(path.join(temp, '.env'), 'private-local-config');
  const entries = inspectFiles(temp, ['source.go']);
  const first = exportSource(temp, entries), second = exportSource(temp, entries);
  assert.notEqual(first.source, second.source);
  for (const exported of [first, second]) {
    assert.deepEqual(fs.readdirSync(exported.source), ['source.go']);
    const manifest = JSON.parse(fs.readFileSync(exported.manifest));
    const bytes = fs.readFileSync(path.join(exported.source, 'source.go'));
    assert.deepEqual(manifest.files, entries);
    assert.equal(manifest.files[0].sha256, crypto.createHash('sha256').update(bytes).digest('hex'));
  }
});
