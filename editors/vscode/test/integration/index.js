// Runs inside VS Code (extension host): `make vscode-test`.
const assert = require('assert');
const path = require('path');
const vscode = require('vscode');

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function run() {
  const file = path.join(__dirname, 'fixture', 'osb.yaml');
  const doc = await vscode.workspace.openTextDocument(file);
  await vscode.window.showTextDocument(doc);
  const ext = vscode.extensions.all.find((e) => e.packageJSON.name === 'osb');
  assert.ok(ext, 'extension found');
  await ext.activate();

  // completion: arguments of write-file
  const lines = doc.getText().split('\n');
  const row = lines.findIndex((l) => l.includes('contnt'));
  const list = await vscode.commands.executeCommand('vscode.executeCompletionItemProvider', doc.uri, new vscode.Position(row, 8));
  const labels = list.items.map((i) => (typeof i.label === 'string' ? i.label : i.label.label));
  assert.ok(labels.includes('content'), 'argument completion: ' + labels.slice(0, 10).join(','));

  // hover on an extra action
  const hrow = lines.findIndex((l) => l.includes('mkdir-home'));
  const hovers = await vscode.commands.executeCommand('vscode.executeHoverProvider', doc.uri, new vscode.Position(hrow, 15));
  const text = hovers.map((h) => h.contents.map((c) => c.value || c).join('')).join('');
  assert.match(text, /mkdir-home/, 'hover');

  // diagnostics from osb validate (the fixture has a misspelled argument)
  for (let i = 0; i < 40 && !vscode.languages.getDiagnostics(doc.uri).length; i++) await sleep(250);
  const diags = vscode.languages.getDiagnostics(doc.uri);
  assert.ok(diags.some((d) => d.range.start.line === row && /contnt/.test(d.message)), 'diagnostic: ' + JSON.stringify(diags.map((d) => d.message)));

  // render command opens a shell script beside the editor
  await vscode.workspace.getConfiguration('osb').update('flavor', 'debian', vscode.ConfigurationTarget.Global);
  const fixed = new vscode.WorkspaceEdit();
  fixed.replace(doc.uri, new vscode.Range(row, 0, row, lines[row].length), '      content: hello');
  await vscode.workspace.applyEdit(fixed);
  await doc.save();
  await vscode.commands.executeCommand('osb.renderShell');
  for (let i = 0; i < 40 && !vscode.workspace.textDocuments.some((d) => d.languageId === 'shellscript'); i++) await sleep(250);
  const script = vscode.workspace.textDocuments.find((d) => d.languageId === 'shellscript');
  assert.ok(script && /write_file \/etc\/motd/.test(script.getText()), 'rendered shell script');
  console.log('VSCODE-INTEGRATION-OK');
}

module.exports = { run };
