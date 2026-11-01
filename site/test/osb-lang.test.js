// Tests for site/static/js/osb-lang.js. Run: make test-js
// Needs the catalog: osb actions list -o json > site/test/catalog.json (make site-test does it).
const test = require('node:test');
const assert = require('node:assert');
const path = require('node:path');
const fs = require('node:fs');
const lang = require('../static/js/osb-lang.js');

const catalog = JSON.parse(fs.readFileSync(path.join(__dirname, 'catalog.json'), 'utf8'));
const L = new lang.Lang(catalog);

// "|" marks the cursor.
function at(src) {
  const offset = src.indexOf('|');
  return { text: src.slice(0, offset) + src.slice(offset + 1), offset };
}
function labels(src, opts) {
  const { text, offset } = at(src);
  const r = L.complete(text, offset, opts);
  return r ? r.items.map((i) => i.label) : [];
}

test('top-level keys', () => {
  const l = labels('name: x\n|');
  for (const k of ['steps', 'arguments', 'flavors', 'artifacts', 'imports']) assert.ok(l.includes(k), k);
  assert.ok(!l.includes('name'), 'existing key is not offered');
});

test('step keys', () => {
  const l = labels('steps:\n  - action: log\n    |');
  for (const k of ['args', 'if', 'title', 'only-if', 'foreach']) assert.ok(l.includes(k), k);
  assert.ok(!l.includes('action'), 'existing key is not offered');
  const first = labels('steps:\n  - |');
  assert.ok(first.includes('action'));
});

test('action names', () => {
  const l = labels('steps:\n  - action: inst|');
  assert.ok(l.includes('install-packages'));
  assert.ok(l.includes('install-aur-package'), 'extra actions');
  assert.ok(labels('steps:\n  - title: x\n    action: |', { all: true }).includes('write-file'));
});

test('action arguments', () => {
  const l = labels('steps:\n  - action: write-file\n    args:\n      path: /x\n      |');
  assert.ok(l.includes('content') && l.includes('mode'));
  assert.ok(!l.includes('path'), 'existing argument is not offered');
  const flow = labels('steps:\n  - action: add-user\n    args:\n      na|');
  assert.deepStrictEqual(flow, ['name']);
  const extra = labels('steps:\n  - args:\n      |\n    action: install-aur-package');
  assert.ok(extra.includes('commit'), 'action after args');
});

test('nested steps and custom actions', () => {
  const src = 'actions:\n  greet:\n    arguments:\n      who: { type: string, required: true }\n    steps:\n      - action: log\n        args: { message: hi }\nsteps:\n  - title: block\n    steps:\n      - action: greet\n        args:\n          |';
  assert.deepStrictEqual(labels(src), ['who']);
  assert.ok(labels('steps:\n  - steps:\n      - action: wri|').includes('write-file'));
});

test('values', () => {
  assert.deepStrictEqual(labels('steps:\n  - action: log\n    on-error: |', { all: true }).sort(), ['continue', 'fail']);
  assert.ok(labels('steps:\n  - action: log\n    flavors: [|', { all: true }).includes('archlinux'));
  assert.ok(labels('targets: [|', { all: true }).includes('qcow2'));
  assert.deepStrictEqual(labels('steps:\n  - action: add-user\n    args:\n      sudo: |', { all: true }).sort(), ['false', 'true']);
  assert.ok(labels('arguments:\n  x:\n    type: |', { all: true }).includes('secret'));
});

test('keys of nested schema objects', () => {
  assert.ok(labels('artifacts:\n  oci:\n    |').includes('tags'));
  assert.ok(labels('arguments:\n  user:\n    |').includes('pattern'));
  assert.ok(labels('flavors:\n  mint:\n    |').includes('based-on'));
});

test('hover', () => {
  let { text, offset } = at('steps:\n  - action: write-fi|le\n    args: { path: /x }');
  let h = L.hover(text, offset);
  assert.match(h.markdown, /\*\*write-file\*\*/);
  ({ text, offset } = at('steps:\n  - action: add-user\n    args:\n      sh|ell: /bin/bash'));
  h = L.hover(text, offset);
  assert.match(h.markdown, /Login shell/);
  ({ text, offset } = at('steps:\n  - action: log\n    only-i|f: true'));
  h = L.hover(text, offset);
  assert.match(h.markdown, /only-if/);
  ({ text, offset } = at('steps:\n  - action: install-aur-pa|ckage'));
  assert.match(L.hover(text, offset).markdown, /extra action/);
});

test('context help', () => {
  const { text, offset } = at('steps:\n  - action: mkdir-home\n    args:\n      user: x\n      |');
  assert.strictEqual(L.stepAction(text, offset).name, 'mkdir-home');
});

test('highlighting', () => {
  const y = lang.highlight('a: "x ${{ args.y }}" # c\n- b: [1, two]\nc: |\n  key: not a key\n', 'yaml');
  assert.match(y, /<span class="tk">a<\/span>/);
  assert.match(y, /<span class="te">\$\{\{ args\.y \}\}<\/span>/);
  assert.match(y, /<span class="tc"># c<\/span>/);
  assert.ok(!/class="tk">key</.test(y), 'block scalar content is not a key');
  const d = lang.highlight("FROM debian\nRUN <<'EOF'\nif true; then echo \"$HOME\"; fi\nEOF\n", 'dockerfile');
  assert.match(d, /<span class="ti">FROM<\/span>/);
  assert.match(d, /<span class="tk">if<\/span>/);
  assert.match(d, /<span class="tv">\$HOME<\/span>/);
  assert.match(lang.highlight('{"a": 1}', 'json'), /class="tk">"a"/);
  assert.strictEqual(lang.highlight('<b>', 'text'), '&lt;b&gt;');
});
