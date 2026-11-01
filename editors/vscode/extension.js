// VS Code extension for osb configurations: completion, hover documentation,
// validation (osb validate) and render commands. The language support is
// shared with the website (lib/osb-lang.js); the action catalog comes from
// `osb actions list -o json`, with a bundled copy as fallback.
'use strict';

const vscode = require('vscode');
const cp = require('child_process');
const fs = require('fs');
const os = require('os');
const path = require('path');
const osbLang = require('./lib/osb-lang.js');

let lang = null;
let diagnostics;
const validateTimers = new Map();

function config() {
  return vscode.workspace.getConfiguration('osb');
}

/** Whether a document is an osb configuration or library. */
function isOsbDocument(doc) {
  if (!doc || doc.languageId !== 'yaml') return false;
  const name = doc.fileName.replace(/\\/g, '/');
  for (const glob of config().get('files') || []) {
    if (globToRegExp(glob).test(name)) return true;
  }
  return /^(steps|actions|tests):/m.test(doc.getText());
}

function globToRegExp(glob) {
  let re = '';
  for (let i = 0; i < glob.length; i++) {
    const c = glob[i];
    if (glob.startsWith('**/', i)) {
      re += '(?:.*/)?';
      i += 2;
    } else if (c === '*') re += '[^/]*';
    else if (c === '?') re += '[^/]';
    else re += c.replace(/[.+^${}()|[\]\\/]/g, '\\$&');
  }
  return new RegExp('(?:^|/)' + re.replace(/^\(\?:\.\*\/\)\?/, '') + '$');
}

function run(args, cwd) {
  return new Promise((resolve) => {
    cp.execFile(config().get('path') || 'osb', args, { cwd, maxBuffer: 64 << 20 }, (err, stdout, stderr) => {
      resolve({ code: err ? (typeof err.code === 'number' ? err.code : -1) : 0, stdout, stderr, missing: err && err.code === 'ENOENT' });
    });
  });
}

/** Loads the catalog from the osb binary, falling back to the bundled one. */
async function loadCatalog(context) {
  const res = await run(['actions', 'list', '-o', 'json']);
  if (res.code === 0) {
    try {
      return JSON.parse(res.stdout);
    } catch (e) { /* fall back */ }
  }
  return JSON.parse(fs.readFileSync(path.join(context.extensionPath, 'data', 'catalog.json'), 'utf8'));
}

// --- providers (pure functions of the text, tested in test/) -------------------

const KINDS = {
  action: 'Function',
  arg: 'Property',
  key: 'Field',
  value: 'Value',
};

function completionItems(L, doc, position) {
  const text = doc.getText();
  const offset = doc.offsetAt(position);
  const res = L.complete(text, offset, { all: true });
  if (!res) return [];
  const range = new vscode.Range(doc.positionAt(res.from), doc.positionAt(res.to));
  const line = doc.lineAt(position.line).text;
  const indent = line.slice(0, doc.positionAt(res.from).character).replace(/\S/g, ' ');
  return res.items.map((it, i) => {
    const item = new vscode.CompletionItem(it.label, vscode.CompletionItemKind[KINDS[it.kind] || 'Text']);
    item.range = range;
    item.detail = it.detail || '';
    if (it.doc) item.documentation = new vscode.MarkdownString(it.doc);
    const insert = it.insert || it.label;
    if (insert.endsWith(':')) {
      item.insertText = new vscode.SnippetString(insert.replace(/\$/g, '\\$') + '\n' + indent + '  $0');
    } else {
      item.insertText = insert;
    }
    if (insert.endsWith(': ')) item.command = { command: 'editor.action.triggerSuggest', title: 'Suggest' };
    item.sortText = String(i).padStart(4, '0');
    return item;
  });
}

function hoverAt(L, doc, position) {
  const h = L.hover(doc.getText(), doc.offsetAt(position));
  if (!h) return null;
  return new vscode.Hover(new vscode.MarkdownString(h.markdown), new vscode.Range(doc.positionAt(h.from), doc.positionAt(h.to)));
}

/** Parses `file:line: message` lines of osb's error output into diagnostics for file. */
function parseDiagnostics(output, file) {
  const out = [];
  const seen = new Set();
  const lines = output.split('\n');
  for (let i = 0; i < lines.length; i++) {
    const m = lines[i].match(/^(?:error:\s*|\s*)(?:[\w-]+\/[\w-]+: )?(.+?):(\d+): (.*)$/);
    if (!m) continue;
    if (path.resolve(m[1]) !== path.resolve(file) && path.basename(m[1]) !== path.basename(file)) continue;
    let msg = m[3];
    const where = (lines[i + 1] || '').match(/^\s+\((.*)\)$/);
    if (where && where[1] !== 'all flavors and targets') msg += ` (${where[1]})`;
    const key = m[2] + msg;
    if (seen.has(key)) continue;
    seen.add(key);
    const line = Math.max(0, parseInt(m[2], 10) - 1);
    const d = new vscode.Diagnostic(new vscode.Range(line, 0, line, 1000), msg, vscode.DiagnosticSeverity.Error);
    d.source = 'osb';
    out.push(d);
  }
  return out;
}

// --- validation and commands ---------------------------------------------------

async function validate(doc) {
  if (!isOsbDocument(doc) || doc.uri.scheme !== 'file' || config().get('validate') === 'off') return;
  if (/\.test\.ya?ml$/.test(doc.fileName)) return;
  let file = doc.fileName;
  let tmp = null;
  if (doc.isDirty) {
    // Validate the unsaved text next to the original (relative paths keep working).
    tmp = path.join(path.dirname(file), `.${path.basename(file)}.osb-check.yaml`);
    fs.writeFileSync(tmp, doc.getText());
    file = tmp;
  }
  try {
    const res = await run(['validate', '-f', file, '--no-prompt'], path.dirname(doc.fileName));
    if (res.missing) {
      diagnostics.delete(doc.uri);
      return;
    }
    const found = parseDiagnostics(res.stderr, file);
    if (res.code !== 0 && !found.length) {
      const d = new vscode.Diagnostic(new vscode.Range(0, 0, 0, 1), res.stderr.trim().replace(/^error: /, ''), vscode.DiagnosticSeverity.Error);
      d.source = 'osb';
      found.push(d);
    }
    diagnostics.set(doc.uri, found);
  } finally {
    if (tmp) fs.rmSync(tmp, { force: true });
  }
}

async function pickFlavor(doc) {
  const configured = config().get('flavor');
  if (configured) return configured;
  const res = await run(['validate', '-f', doc.fileName, '--no-prompt'], path.dirname(doc.fileName));
  const flavors = (res.stdout.match(/^(\S+)\s+\d+ steps$/gm) || []).map((l) => l.split(/\s+/)[0]);
  const list = flavors.length ? flavors : ['ubuntu', 'debian', 'archlinux'];
  return list.length === 1 ? list[0] : vscode.window.showQuickPick(list, { placeHolder: 'Flavor to render for' });
}

async function render(kind) {
  const editor = vscode.window.activeTextEditor;
  if (!editor || !isOsbDocument(editor.document)) {
    vscode.window.showWarningMessage('Open an osb configuration first.');
    return;
  }
  const doc = editor.document;
  if (doc.isDirty) await doc.save();
  const flavor = await pickFlavor(doc);
  if (!flavor) return;
  const cwd = path.dirname(doc.fileName);
  let content;
  let language;
  if (kind === 'shell') {
    const res = await run(['render', 'shell', '-f', doc.fileName, '--flavor', flavor, '--no-prompt', '--redact'], cwd);
    if (res.code !== 0) return showError(res);
    content = res.stdout;
    language = 'shellscript';
  } else {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'osb-'));
    const res = await run(['render', kind, '-f', doc.fileName, '--flavor', flavor, '--no-prompt', '-o', dir], cwd);
    if (res.code !== 0) return showError(res);
    const file = kind === 'dockerfile' ? 'Dockerfile' : 'user-data';
    content = fs.readFileSync(path.join(dir, file), 'utf8');
    fs.rmSync(dir, { recursive: true, force: true });
    language = kind === 'dockerfile' ? 'dockerfile' : 'yaml';
  }
  const out = await vscode.workspace.openTextDocument({ content, language });
  await vscode.window.showTextDocument(out, { viewColumn: vscode.ViewColumn.Beside, preview: true });
}

function showError(res) {
  if (res.missing) {
    vscode.window.showErrorMessage(`osb was not found. Install it (https://github.com/SvenTo/osb/releases) or set "osb.path".`);
  } else {
    vscode.window.showErrorMessage((res.stderr || res.stdout).trim().replace(/^error: /, '').slice(0, 500));
  }
}

async function showAction() {
  const items = (lang.catalog.actions || []).map((a) => ({ label: a.name, description: (a.extra ? 'extra · ' : '') + a.category, detail: a.description.split(/\.\s+(?=[A-Z])/)[0], action: a }));
  const pick = await vscode.window.showQuickPick(items, { placeHolder: 'Action', matchOnDescription: true, matchOnDetail: true });
  if (!pick) return;
  const doc = await vscode.workspace.openTextDocument({ content: osbLang.actionMarkdown(pick.action), language: 'markdown' });
  await vscode.commands.executeCommand('markdown.showPreview', doc.uri);
}

function updateContext(editor) {
  vscode.commands.executeCommand('setContext', 'osb.isConfig', !!editor && isOsbDocument(editor.document));
}

async function activate(context) {
  diagnostics = vscode.languages.createDiagnosticCollection('osb');
  context.subscriptions.push(diagnostics);
  try {
    lang = new osbLang.Lang(await loadCatalog(context));
  } catch (e) {
    vscode.window.showErrorMessage('osb: could not load the action catalog: ' + e.message);
    return;
  }
  const selector = { language: 'yaml' };
  context.subscriptions.push(
    vscode.languages.registerCompletionItemProvider(selector, {
      provideCompletionItems: (doc, pos) => (isOsbDocument(doc) ? completionItems(lang, doc, pos) : undefined),
    }, ' ', '[', ','),
    vscode.languages.registerHoverProvider(selector, {
      provideHover: (doc, pos) => (isOsbDocument(doc) ? hoverAt(lang, doc, pos) : undefined),
    }),
    vscode.workspace.onDidSaveTextDocument((doc) => validate(doc)),
    vscode.workspace.onDidOpenTextDocument((doc) => validate(doc)),
    vscode.workspace.onDidCloseTextDocument((doc) => diagnostics.delete(doc.uri)),
    vscode.workspace.onDidChangeTextDocument((e) => {
      if (config().get('validate') !== 'onType' || !isOsbDocument(e.document)) return;
      clearTimeout(validateTimers.get(e.document.uri.toString()));
      validateTimers.set(e.document.uri.toString(), setTimeout(() => validate(e.document), 600));
    }),
    vscode.window.onDidChangeActiveTextEditor(updateContext),
    vscode.commands.registerCommand('osb.validate', () => {
      const ed = vscode.window.activeTextEditor;
      if (ed) validate(ed.document);
    }),
    vscode.commands.registerCommand('osb.renderShell', () => render('shell')),
    vscode.commands.registerCommand('osb.renderDockerfile', () => render('dockerfile')),
    vscode.commands.registerCommand('osb.renderCloudInit', () => render('cloud-init')),
    vscode.commands.registerCommand('osb.showAction', showAction),
  );
  updateContext(vscode.window.activeTextEditor);
  vscode.workspace.textDocuments.forEach((doc) => validate(doc));
}

function deactivate() {}

module.exports = { activate, deactivate, isOsbDocument, completionItems, hoverAt, parseDiagnostics, globToRegExp };
