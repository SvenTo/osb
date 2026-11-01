import { render, catalog, OutputView, debounce, el, decodeShare, encodeShare, saveFile, flash, gotoLine, tabIndent, TARGET_LABELS, FLAVOR_LABELS } from './osb.js';

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
const yaml = $('pg-yaml');
const view = new OutputView($('pg-out'), { title: 'Output', showPlan: true });
view.onErrorLine = (line) => gotoLine(yaml, line);
tabIndent(yaml);

let target = 'shell';
let flavor = '';
const argValues = {};

for (const [t, label] of Object.entries(TARGET_LABELS)) $('pg-target').append(el('option', { value: t }, `${label} (${t})`));
for (const name of Object.keys(EXAMPLES)) $('pg-example').append(el('option', { value: name }, name));

async function init() {
  const params = new URLSearchParams(location.hash.slice(1));
  let text = null;
  if (params.get('c')) {
    try { text = await decodeShare(params.get('c')); } catch (e) { text = null; }
  }
  if (text == null) {
    try { text = localStorage.getItem('osb-playground'); } catch (e) { /* no storage */ }
  }
  yaml.value = text || EXAMPLES['Developer workstation'];
  target = params.get('t') || target;
  flavor = params.get('f') || '';
  $('pg-target').value = target;
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
  try { localStorage.setItem('osb-playground', yaml.value); } catch (e) { /* no storage */ }
  const resp = await render({ config: yaml.value, flavor, target, args: argValues, placeholders: true }).catch((e) => ({ error: String(e) }));
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
  renderArgs(resp);
  view.show(resp);
}
const later = debounce(update, 250);

yaml.addEventListener('input', later);
$('pg-target').addEventListener('change', (e) => { target = e.target.value; update(); });
$('pg-flavor').addEventListener('change', (e) => { flavor = e.target.value; update(); });
$('pg-example').addEventListener('change', (e) => {
  if (!e.target.value) return;
  yaml.value = EXAMPLES[e.target.value];
  e.target.value = '';
  update();
});
$('pg-download').addEventListener('click', () => saveFile('osb.yaml', yaml.value));
$('pg-share').addEventListener('click', async (e) => {
  const hash = 'c=' + await encodeShare(yaml.value) + '&t=' + target + (flavor ? '&f=' + flavor : '');
  history.replaceState(null, '', '#' + hash);
  navigator.clipboard?.writeText(location.href).then(() => flash(e.target, 'Link copied'));
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
          if (!/\bsteps:\s*(\n|$)/.test(yaml.value)) yaml.value = yaml.value.replace(/\s*$/, '\n') + 'steps:\n';
          yaml.value = yaml.value.replace(/\s*$/, '\n') + exampleFor(a);
          yaml.scrollTop = yaml.scrollHeight;
          update();
        },
      }, el('b', {}, a.name), a.extra ? el('i', {}, 'extra') : null, el('span', {}, a.description.split(/\.\s+(?=[A-Z])/)[0]))));
  };
  $('pal-search').addEventListener('input', draw);
  draw();
}

init();
