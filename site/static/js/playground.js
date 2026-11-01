import { render, catalog, OutputView, debounce, el, decodeShare, encodeShare, saveFile, flash, osbLang, TARGET_LABELS, FLAVOR_LABELS } from './osb.js';
import { Editor } from './editor.js';

const EXAMPLES = {
  'Hello': `name: hello
steps:
  - action: log
    args: { message: "Hello from \${{ flavor.name }} (\${{ target.name }})" }
  - action: write-file
    args:
      path: /etc/motd
      content: |
        Configured by osb for \${{ flavor.name }}.
`,
  'Developer workstation': `name: devbox
description: A developer workstation

arguments:
  user: { default: dev, pattern: "^[a-z_][a-z0-9_-]*$" }
  languages: { type: array, default: [go, python] }
  password: { type: secret, description: Password of the user }

steps:
  - action: install-packages
    args:
      packages: [git, build-essential, curl, jq, vim, openssh-server]

  - title: Languages
    foreach: "\${{ args.languages }}"
    action: install-packages
    args:
      packages:
        by-flavor:
          arch: "\${{ {go: ['go'], python: ['python'], node: ['nodejs', 'npm']}[item] }}"
          default: "\${{ {go: ['golang'], python: ['python3'], node: ['nodejs', 'npm']}[item] }}"

  - name: user
    action: add-user
    args: { name: "\${{ args.user }}", shell: /bin/bash, sudo: true }

  - if: args.password
    action: set-password
    args: { user: "\${{ args.user }}", password: "\${{ args.password }}" }

  - action: mkdir-home
    args: { user: "\${{ args.user }}", paths: [Projects, .local/bin] }

  - action: set-config-value
    args: { path: /etc/osb/devbox.json, format: json, key: owner, value: "\${{ args.user }}" }
`,
  'Container image with a web server': `name: web
artifacts:
  dockerfile:
    expose: [80]
    cmd: [nginx, -g, "daemon off;"]
  oci:
    tags: ["web:\${{ flavor.name }}"]
steps:
  - action: install-packages
    args: { packages: [nginx] }
  - action: write-file
    args:
      path:
        by-flavor: { arch: /usr/share/nginx/html/index.html, default: /var/www/html/index.html }
      content: <h1>Built by osb</h1>
  - title: Live systems only
    targets: [shell, apply, cloud-init]
    action: enable-systemd-unit
    args: { units: [nginx.service] }
`,
  'Files from the Files panel': {
    config: `name: with-files
steps:
  - title: Message of the day
    action: template-file
    args: { path: /etc/motd, source: files/motd.tmpl }
  - title: Configuration directory
    action: copy-files
    args: { source: files/app/, dest: /etc/app/, mode: "0644" }
  - action: log
    args: { message: '\${{ file("files/app/app.ini").split("\\n")[0] }}' }
`,
    files: [
      { path: 'files/motd.tmpl', content: 'Welcome to ${{ flavor.name }}!\n' },
      { path: 'files/app/app.ini', content: '[server]\nport = 8080\n' },
      { path: 'files/app/conf.d/extra.conf', content: 'debug = false\n' },
    ],
  },
  'KDE remote desktop (Arch)': `name: kde-vm
flavors:
  ubuntu: { enabled: false }
  debian: { enabled: false }
arguments:
  user: { default: dev }
steps:
  - action: remove-pacman-repositories
    args: { names: [community] }
  - action: set-pacman-mirrors
  - action: update-pacman-keyring
  - action: install-packages
    args: { packages: [plasma-meta, plasma-login-manager, krdp, konsole] }
  - action: add-user
    args: { name: "\${{ args.user }}", shell: /bin/bash, sudo: true }
  - action: ensure-password
    args: { user: "\${{ args.user }}" }
  - action: plasma-autologin
    args: { user: "\${{ args.user }}" }
  - action: set-kde-config
    args:
      user: \${{ args.user }}
      settings:
        - { file: kscreenlockerrc, group: Daemon, key: Autolock, value: false }
  - action: krdp-server
    args: { user: "\${{ args.user }}" }
  - action: install-aur-package
    args: { name: yay-bin, commit: a2812a593d4017a657bbedf98761db78072b5e4c }
  - action: disable-sleep
`,
};

const $ = (id) => document.getElementById(id);
const CONFIG = 'osb.yaml';
const MAX_STORE = 1 << 20; // bytes of files kept in the browser
const view = new OutputView($('pg-out'), { title: 'Output', showPlan: true });

let target = 'shell';
let flavor = '';
const argValues = {};
// Files: osb.yaml and the user's files ({path, content, base64})
let files = [{ path: CONFIG, content: '' }];
let current = CONFIG;

const editor = new Editor($('pg-editor'), { label: 'Configuration', help: $('pg-help'), onChange: (v) => { file(current).content = v; later(); } });
view.onErrorLine = (line) => { select(CONFIG); editor.gotoLine(line); };
osbLang().then((l) => editor.setLang(l));

for (const [t, label] of Object.entries(TARGET_LABELS)) $('pg-target').append(el('option', { value: t }, `${label} (${t})`));
for (const name of Object.keys(EXAMPLES)) $('pg-example').append(el('option', { value: name }, name));

function file(path) {
  return files.find((f) => f.path === path);
}

function save() {
  try {
    const data = JSON.stringify(files);
    if (data.length < MAX_STORE) localStorage.setItem('osb-playground-files', data);
    localStorage.setItem('osb-playground', file(CONFIG).content);
  } catch (e) { /* no storage */ }
}

function select(path) {
  current = path;
  const f = file(path);
  editor.setLanguage(f.base64 ? 'text' : window.osbLang.languageOf(path === CONFIG ? 'x.yaml' : path));
  editor.value = f.base64 ? `(binary file, ${Math.round(f.content.length * 0.75)} bytes – replace it by uploading again)` : f.content;
  editor.ta.readOnly = !!f.base64;
  renderFiles();
}

function renderFiles() {
  const bar = $('pg-files');
  bar.replaceChildren(...files.map((f) => el('span', { class: 'file-tab', role: 'tab', 'aria-selected': String(f.path === current), tabindex: '0',
    onclick: () => select(f.path), onkeydown: (e) => { if (e.key === 'Enter') select(f.path); } },
  f.path, f.path === CONFIG ? null : el('button', { class: 'x', type: 'button', 'aria-label': 'Delete ' + f.path, title: 'Delete',
    onclick: (e) => { e.stopPropagation(); remove(f.path); } }, '×'))),
  el('button', { class: 'btn', type: 'button', onclick: newFile }, '+ New file'),
  el('label', { class: 'btn', role: 'button', tabindex: '0' }, 'Upload…',
    el('input', { type: 'file', multiple: true, hidden: true, onchange: (e) => upload(e.target.files) })));
}

function newFile() {
  const name = prompt('File name, relative to osb.yaml (e.g. files/motd)', 'files/');
  if (!name) return;
  const path = name.replace(/^\.?\/+/, '');
  if (path === CONFIG || !path || path.endsWith('/')) return;
  if (!file(path)) files.push({ path, content: '' });
  select(path);
  save();
  later();
}

function remove(path) {
  files = files.filter((f) => f.path !== path);
  if (current === path) select(CONFIG);
  else renderFiles();
  save();
  later();
}

async function upload(list) {
  const dir = (prompt('Directory for the uploaded files (relative to osb.yaml)', 'files') || '').replace(/^\.?\/+|\/+$/g, '');
  for (const f of list) {
    const bytes = new Uint8Array(await f.arrayBuffer());
    const path = (dir ? dir + '/' : '') + f.name;
    let entry;
    try {
      const text = new TextDecoder('utf-8', { fatal: true }).decode(bytes);
      if (/\u0000/.test(text)) throw new Error('binary');
      entry = { path, content: text };
    } catch (e) {
      let bin = '';
      for (const b of bytes) bin += String.fromCharCode(b);
      entry = { path, content: btoa(bin), base64: true };
    }
    files = files.filter((x) => x.path !== path).concat([entry]);
  }
  renderFiles();
  save();
  later();
}

function userFiles() {
  return files.filter((f) => f.path !== CONFIG).map((f) => ({ path: f.path, content: f.content, base64: !!f.base64 }));
}

async function init() {
  const params = new URLSearchParams(location.hash.slice(1));
  let loaded = false;
  if (params.get('p')) {
    try {
      files = JSON.parse(await decodeShare(params.get('p')));
      loaded = true;
    } catch (e) { /* bad link */ }
  } else if (params.get('c')) {
    try {
      files = [{ path: CONFIG, content: await decodeShare(params.get('c')) }];
      loaded = true;
    } catch (e) { /* bad link */ }
  }
  if (!loaded) {
    try {
      const saved = JSON.parse(localStorage.getItem('osb-playground-files') || 'null');
      if (Array.isArray(saved) && saved.some((f) => f.path === CONFIG)) files = saved;
      else if (localStorage.getItem('osb-playground')) files = [{ path: CONFIG, content: localStorage.getItem('osb-playground') }];
      else files = [{ path: CONFIG, content: EXAMPLES['Developer workstation'] }];
    } catch (e) {
      files = [{ path: CONFIG, content: EXAMPLES['Developer workstation'] }];
    }
  }
  target = params.get('t') || target;
  flavor = params.get('f') || '';
  $('pg-target').value = target;
  select(CONFIG);
  update();
  buildPalette();
}

function renderArgs(resp) {
  const form = $('pg-args');
  const args = resp.arguments || [];
  form.hidden = !args.length;
  const sig = JSON.stringify(args);
  if (form.dataset.sig === sig) return;
  form.dataset.sig = sig;
  form.replaceChildren(...args.map((a) => {
    let input;
    if (a.type === 'bool') {
      input = el('input', { type: 'checkbox', checked: argValues[a.name] ? argValues[a.name] === 'true' : !!a.default });
      input.addEventListener('change', () => { argValues[a.name] = String(input.checked); later(); });
    } else if (a.type === 'option') {
      input = el('select', {}, ...(a.options || []).map((o) => el('option', { value: String(o) }, String(o))));
      input.value = argValues[a.name] ?? String(a.default ?? '');
      input.addEventListener('change', () => { argValues[a.name] = input.value; later(); });
    } else {
      input = el('input', {
        type: a.type === 'secret' ? 'password' : 'text', autocomplete: 'off', spellcheck: 'false',
        placeholder: a.default == null ? (a.required ? 'required' : '') : (Array.isArray(a.default) ? a.default.join(',') : String(a.default)),
      });
      input.value = argValues[a.name] ?? '';
      input.addEventListener('input', () => { argValues[a.name] = input.value; later(); });
    }
    return el('label', { title: a.description || '' }, el('span', { class: a.required ? 'req' : '' }, a.name), input);
  }));
}

async function update() {
  save();
  const resp = await render({ config: file(CONFIG).content, flavor, target, args: argValues, placeholders: true, files: userFiles() })
    .catch((e) => ({ error: String(e) }));
  if (resp.flavors?.length) {
    const sel = $('pg-flavor');
    const sig = resp.flavors.join();
    if (sel.dataset.sig !== sig) {
      sel.dataset.sig = sig;
      sel.replaceChildren(...resp.flavors.map((f) => el('option', { value: f }, FLAVOR_LABELS[f] || f)));
    }
    if (!resp.flavors.includes(flavor)) flavor = resp.flavor || resp.flavors[0];
    sel.value = flavor;
  }
  editor.markError(current === CONFIG && resp.error ? +(/osb\.yaml:(\d+)/.exec(resp.error) || [])[1] : 0);
  renderArgs(resp);
  view.show(resp);
}
const later = debounce(update, 250);

$('pg-target').addEventListener('change', (e) => { target = e.target.value; update(); });
$('pg-flavor').addEventListener('change', (e) => { flavor = e.target.value; update(); });
$('pg-example').addEventListener('change', (e) => {
  const ex = EXAMPLES[e.target.value];
  e.target.value = '';
  if (!ex) return;
  if (typeof ex === 'string') files = [{ path: CONFIG, content: ex }];
  else files = [{ path: CONFIG, content: ex.config }, ...ex.files];
  select(CONFIG);
  update();
});
$('pg-download').addEventListener('click', () => saveFile('osb.yaml', file(CONFIG).content));
$('pg-share').addEventListener('click', async (e) => {
  const extra = userFiles();
  const small = JSON.stringify(extra).length < 20000;
  const hash = (extra.length && small ? 'p=' + await encodeShare(JSON.stringify(files)) : 'c=' + await encodeShare(file(CONFIG).content)) +
    '&t=' + target + (flavor ? '&f=' + flavor : '');
  history.replaceState(null, '', '#' + hash);
  navigator.clipboard?.writeText(location.href).then(() => flash(e.target, extra.length && !small ? 'Copied (without files)' : 'Link copied'));
});

function exampleFor(a) {
  if (a.example) {
    const lines = a.example.replace(/\n$/, '').split('\n');
    return lines.map((l, i) => (i === 0 ? '  - ' : '    ') + l).join('\n') + '\n';
  }
  const req = (a.args || []).filter((x) => x.required);
  let s = `  - action: ${a.name}\n`;
  if (req.length) {
    s += '    args:\n' + req.map((x) => `      ${x.name}: ${x.type === 'array' ? '[]' : x.type === 'bool' ? 'true' : '""'}\n`).join('');
  }
  return s;
}

async function buildPalette() {
  const cat = await catalog();
  const list = $('pal-list');
  const draw = () => {
    const term = $('pal-search').value.trim().toLowerCase();
    list.replaceChildren(...cat.actions
      .filter((a) => !term || (a.name + ' ' + a.category + ' ' + a.description).toLowerCase().includes(term))
      .map((a) => el('button', {
        class: 'palette-item', type: 'button', title: a.description,
        onclick: () => {
          if (current !== CONFIG) select(CONFIG);
          let v = file(CONFIG).content;
          if (!/^steps:\s*$/m.test(v)) v = v.replace(/\s*$/, '\n') + 'steps:\n';
          v = v.replace(/\s*$/, '\n') + exampleFor(a);
          file(CONFIG).content = v;
          editor.value = v;
          editor.ta.scrollTop = editor.ta.scrollHeight;
          update();
        },
      }, el('b', {}, a.name), a.extra ? el('i', {}, 'extra') : null, el('span', {}, a.description.split(/\.\s+(?=[A-Z])/)[0]))));
  };
  $('pal-search').addEventListener('input', draw);
  draw();
}

init();
