// Tests of the extension's providers with a minimal mock of the vscode API.
// Run: npm test (after `make vscode` has copied lib/ and data/).
const test = require('node:test');
const assert = require('node:assert');
const Module = require('node:module');
const path = require('node:path');

class Position { constructor(line, character) { this.line = line; this.character = character; } }
class Range {
  constructor(a, b, c, d) {
    if (a instanceof Position) { this.start = a; this.end = b; } else { this.start = new Position(a, b); this.end = new Position(c, d); }
  }
}
const vscode = {
  Position, Range,
  CompletionItem: class { constructor(label, kind) { this.label = label; this.kind = kind; } },
  CompletionItemKind: { Function: 2, Property: 9, Field: 4, Value: 11, Text: 0 },
  MarkdownString: class { constructor(v) { this.value = v; } },
  SnippetString: class { constructor(v) { this.value = v; } },
  Hover: class { constructor(contents, range) { this.contents = contents; this.range = range; } },
  Diagnostic: class { constructor(range, message, severity) { this.range = range; this.message = message; this.severity = severity; } },
  DiagnosticSeverity: { Error: 0 },
  workspace: { getConfiguration: () => ({ get: (k) => ({ files: ['**/osb.yaml', '**/*.osb.yaml'], validate: 'onSave', path: 'osb' })[k] }) },
};
const resolve = Module._resolveFilename;
Module._resolveFilename = function (request, ...rest) {
  return request === 'vscode' ? 'vscode' : resolve.call(this, request, ...rest);
};
require.cache.vscode = { id: 'vscode', filename: 'vscode', loaded: true, exports: vscode };

const ext = require('../extension.js');
const osbLang = require('../lib/osb-lang.js');
const L = new osbLang.Lang(require('../data/catalog.json'));

function doc(src, fileName = '/w/osb.yaml') {
  const offset = src.indexOf('|');
  const text = offset >= 0 ? src.slice(0, offset) + src.slice(offset + 1) : src;
  const lines = text.split('\n');
  const d = {
    languageId: 'yaml', fileName, getText: () => text,
    offsetAt: (p) => lines.slice(0, p.line).reduce((n, l) => n + l.length + 1, 0) + p.character,
    positionAt: (o) => {
      let line = 0;
      while (line < lines.length - 1 && o > lines[line].length) { o -= lines[line].length + 1; line++; }
      return new Position(line, o);
    },
    lineAt: (n) => ({ text: lines[n] }),
  };
  return { d, pos: offset >= 0 ? d.positionAt(offset) : null };
}

test('recognizes osb documents', () => {
  assert.ok(ext.isOsbDocument(doc('name: x', '/w/osb.yaml').d));
  assert.ok(ext.isOsbDocument(doc('name: x', '/w/dev.osb.yaml').d));
  assert.ok(ext.isOsbDocument(doc('actions:\n  a: {}', '/w/library/users.yaml').d));
  assert.ok(!ext.isOsbDocument(doc('version: 2', '/w/docker-compose.yaml').d));
});

test('completion items', () => {
  const { d, pos } = doc('steps:\n  - action: write-file\n    args:\n      con|');
  const items = ext.completionItems(L, d, pos);
  const content = items.find((i) => i.label === 'content');
  assert.ok(content, 'content argument');
  assert.strictEqual(content.insertText, 'content: ');
  assert.match(content.documentation.value, /File content/);
  assert.strictEqual(content.range.start.character, 6);
  const keys = ext.completionItems(L, ...Object.values(doc('|')));
  const steps = keys.find((i) => i.label === 'steps');
  assert.match(steps.insertText.value, /^steps:\n  \$0$/, 'nested key snippet');
  const actions = ext.completionItems(L, ...Object.values(doc('steps:\n  - action: |')));
  assert.ok(actions.some((i) => i.label === 'install-aur-package' && /extra/.test(i.detail)));
});

test('hover', () => {
  const { d, pos } = doc('steps:\n  - action: mkdir-ho|me');
  const h = ext.hoverAt(L, d, pos);
  assert.match(h.contents.value, /mkdir-home/);
  assert.strictEqual(h.range.start.character, 12);
});

test('diagnostics from osb validate', () => {
  const out = 'error: /w/osb.yaml:3: steps[0].args: additional properties \'contnt\' not allowed\n';
  const ds = ext.parseDiagnostics(out, '/w/osb.yaml');
  assert.strictEqual(ds.length, 1);
  assert.strictEqual(ds[0].range.start.line, 2);
  const grouped = 'error: validation failed:\n  /w/osb.yaml:2: step "nope": unknown action "nope"\n    (all flavors and targets)\n  /w/osb.yaml:5: bad\n    (debian/shell, ubuntu/shell)\n';
  const g = ext.parseDiagnostics(grouped, '/w/osb.yaml');
  assert.deepStrictEqual(g.map((x) => [x.range.start.line, x.message]), [[1, 'step "nope": unknown action "nope"'], [4, 'bad (debian/shell, ubuntu/shell)']]);
  assert.strictEqual(ext.parseDiagnostics('error: /w/other.yaml:1: x', '/w/osb.yaml').length, 0);
});
