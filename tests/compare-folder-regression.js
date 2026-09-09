const assert = require('assert');
const fs = require('fs');
const path = require('path');
const vm = require('vm');

const source = fs.readFileSync(path.join(__dirname, '..', 'web', 'pages', 'compare.js'), 'utf8');
const storage = {
  getItem() { return null; },
  setItem() {},
  removeItem() {}
};
const Kairo = {
  core: { el() {}, toast() {}, copyToClipboard() {} },
  api: {
    api() {},
    getPreference: async () => ({ exists: false }),
    putPreference: async () => {},
    preferenceSaver: () => () => {},
    browseButton() {}
  },
  pages: {},
  state: { routes: {}, routeNames: {} }
};
const context = {
  window: { Kairo, localStorage: storage },
  localStorage: storage,
  console,
  setTimeout,
  clearTimeout,
  URL,
  Date,
  Map,
  Set,
  Number,
  String,
  Object,
  Array,
  Math,
  JSON,
  Promise
};
vm.runInNewContext(source, context, { filename: 'web/pages/compare.js' });

const { rollupAllFolders, canSaveComparedFile } = Kairo.compareTest;
const dir = (rel, status = 'same', extra = {}) => ({
  rel_path: rel,
  left: { is_dir: true },
  status,
  ...extra
});
const file = (rel, status = 'same', extra = {}) => ({
  rel_path: rel,
  left: { is_dir: false },
  status,
  ...extra
});

// A directory returned at a scan boundary is not verified yet.
{
  const item = dir('empty');
  rollupAllFolders([item], new Set(['']));
  assert.strictEqual(item.status, 'pending');
  assert.strictEqual(item.pending, true);
}

// Once its lazy scan is loaded, an empty directory is a verified same result.
{
  const item = dir('empty');
  rollupAllFolders([item], new Set(['', 'empty']));
  assert.strictEqual(item.status, 'same');
  assert.strictEqual(item.pending, false);
  assert.strictEqual(item.incomplete, false);
}

// Loaded nested empty directories roll up to their parent as same.
{
  const nested = dir('empty/nested');
  const parent = dir('empty');
  rollupAllFolders([parent, nested], new Set(['', 'empty', 'empty/nested']));
  assert.strictEqual(nested.status, 'same');
  assert.strictEqual(parent.status, 'same');
  assert.strictEqual(parent.pending, false);
}

// A real child difference must keep the directory different.
{
  const child = file('bundle/item.txt', 'different');
  const parent = dir('bundle');
  rollupAllFolders([parent, child], new Set(['', 'bundle']));
  assert.strictEqual(parent.status, 'different');
  assert.strictEqual(parent.pending, false);
}

// Type conflicts and errors must never be rewritten by empty-directory logic.
{
  const conflict = { rel_path: 'conflict', left: { is_dir: true }, right: { is_dir: false }, status: 'type_conflict' };
  const error = dir('broken', 'error', { error: 'scan failed' });
  rollupAllFolders([conflict, error], new Set(['', 'conflict', 'broken']));
  assert.strictEqual(conflict.status, 'type_conflict');
  assert.strictEqual(error.status, 'error');
}

// Save is enabled only after a complete versioned read and a dirty edit.
{
  const valid = { source: { kind: 'local' }, version: 'v1', loadState: 'ready', dirty: true, saving: false };
  assert.strictEqual(canSaveComparedFile(valid), true);
  assert.strictEqual(canSaveComparedFile({ ...valid, version: null }), false);
  assert.strictEqual(canSaveComparedFile({ ...valid, loadState: 'error' }), false);
  assert.strictEqual(canSaveComparedFile({ ...valid, source: { kind: 'text' } }), false);
  assert.strictEqual(canSaveComparedFile({ ...valid, saving: true }), false);
}

console.log('compare-folder-regression: 10 cases passed');
