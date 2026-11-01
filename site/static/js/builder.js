import { render, OutputView, debounce, el, highlight, encodeShare, saveFile, flash, FLAVOR_LABELS } from './osb.js';
import { FEATURES, q } from './features.js';

const TARGETS = [
  ['oci', 'a container image'],
  ['dockerfile', 'a Dockerfile'],
  ['qcow2', 'a VM image (qcow2)'],
  ['ova', 'an OVA appliance'],
  ['wsl', 'a WSL distribution'],
  ['cloud-init', 'a cloud-init configuration'],
  ['shell', 'a shell script'],
  ['apply', 'my current machine set up'],
];
const FLAVOR_FAMILY = { ubuntu: 'debian', debian: 'debian', archlinux: 'arch' };

const $ = (id) => document.getElementById(id);
const state = { target: 'oci', flavor: 'archlinux', user: 'dev', name: 'my-machine', on: new Set(), params: {} };

// Restore the last builder state of this browser; otherwise start with an example.
let restored = false;
try {
  const saved = JSON.parse(localStorage.getItem('osb-builder') || 'null');
  if (saved) {
    Object.assign(state, saved, { on: new Set(saved.on) });
    restored = true;
  }
} catch (e) { /* no storage */ }
if (!restored) {
  for (const f of FEATURES) if (f.default) state.on.add(f.id);
  for (const id of ['ssh', 'kde', 'rdp', 'nosleep', 'devtools']) state.on.add(id);
}

for (const [v, label] of TARGETS) $('b-target').append(el('option', { value: v }, label));
for (const [v, label] of Object.entries(FLAVOR_LABELS)) $('b-flavor').append(el('option', { value: v }, label));
$('b-target').value = state.target;
$('b-flavor').value = state.flavor;
$('b-user').value = state.user;
$('b-name').value = state.name;

const view = new OutputView($('b-out'), { title: 'Rendered by osb', showPlan: true });

function supported(f) {
  return !f.flavors || f.flavors.includes(state.flavor);
}

function params(f) {
  const p = {};
  for (const d of f.params || []) {
    const v = state.params[f.id]?.[d.id];
    p[d.id] = v === undefined ? d.value : v;
  }
  return p;
}

function renderFeatures() {
  const root = $('features');
  root.replaceChildren();
  for (const f of FEATURES) {
    const ok = supported(f);
    const on = ok && state.on.has(f.id);
    const box = el('input', { type: 'checkbox', id: 'f-' + f.id, checked: on, disabled: !ok });
    const paramsBox = el('div', { class: 'f-params', hidden: !on || !(f.params || []).length });
    for (const d of f.params || []) {
      const cur = params(f)[d.id];
      let input;
      if (d.type === 'check') {
        input = el('input', { type: 'checkbox', checked: !!cur });
        input.addEventListener('change', () => setParam(f, d, input.checked));
        paramsBox.append(el('label', { class: 'param check' }, input, d.label));
      } else {
        input = d.type === 'textarea'
          ? el('textarea', { rows: 2, spellcheck: 'false' })
          : el('input', { spellcheck: 'false' });
        input.value = cur;
        input.addEventListener('input', () => setParam(f, d, input.value));
        paramsBox.append(el('label', { class: 'param' }, d.label, input));
      }
      input.addEventListener('click', (e) => e.stopPropagation());
    }
    const badge = f.flavors ? el('span', { class: 'badge' }, f.flavors.map((x) => FLAVOR_LABELS[x]).join(', ') + ' only') : null;
    const card = el('div', { class: 'feature' + (on ? ' on' : '') + (ok ? '' : ' disabled'), title: ok ? '' : 'Not available for ' + FLAVOR_LABELS[state.flavor] },
      box, el('label', { class: 'f-title', for: 'f-' + f.id }, f.title, badge), el('div', { class: 'f-desc' }, f.desc), paramsBox);
    card.addEventListener('click', (e) => {
      if (!ok || e.target.closest('.f-params') || e.target === box || e.target.tagName === 'LABEL') return;
      box.click();
    });
    box.addEventListener('change', () => toggle(f, box.checked));
    root.append(card);
  }
}

function toggle(f, on) {
  if (on) {
    state.on.add(f.id);
    for (const r of f.requires || []) state.on.add(r);
  } else {
    state.on.delete(f.id);
    for (const g of FEATURES) if ((g.requires || []).includes(f.id)) state.on.delete(g.id);
  }
  renderFeatures();
  update();
}

function setParam(f, d, v) {
  (state.params[f.id] ||= {})[d.id] = v;
  later();
}

function active() {
  return FEATURES.filter((f) => state.on.has(f.id) && supported(f));
}

function sentence() {
  const t = TARGETS.find(([v]) => v === state.target)[1];
  const names = active().filter((f) => f.id !== 'sudo').map((f) => f.phrase);
  const withPart = names.length ? ' with ' + (names.length > 1 ? names.slice(0, -1).join(', ') + ' and ' + names.at(-1) : names[0]) : '';
  return `I want ${t} running ${FLAVOR_LABELS[state.flavor]} for the user ${state.user || 'dev'}${withPart}.`;
}

function generate() {
  const feats = active();
  const restricted = feats.filter((f) => f.flavors);
  const flavors = restricted.length
    ? restricted.reduce((acc, f) => acc.filter((x) => f.flavors.includes(x)), Object.keys(FLAVOR_LABELS))
    : Object.keys(FLAVOR_LABELS);
  const disabled = Object.keys(FLAVOR_LABELS).filter((x) => !flavors.includes(x));
  const artifacts = {};
  for (const f of feats) {
    if (!f.artifacts) continue;
    for (const [t, kv] of Object.entries(f.artifacts(params(f)))) Object.assign(artifacts[t] ||= {}, kv);
  }
  let y = `# Generated by the osb builder. Edit freely: https://svento.github.io/osb/
# ${sentence()}
name: ${q(state.name || 'my-machine')}
description: ${q(sentence().replace(/^I want /, '').replace(/\.$/, ''))}

arguments:
  user:
    description: The main user
    default: ${q(state.user || 'dev')}
    pattern: "^[a-z_][a-z0-9_-]*$"
`;
  if (disabled.length) {
    y += `\n# Some features are only available for ${flavors.map((x) => FLAVOR_LABELS[x]).join(', ')}.\nflavors:\n` +
      disabled.map((x) => `  ${x}: { enabled: false }\n`).join('');
  }
  if (Object.keys(artifacts).length) {
    y += '\nartifacts:\n' + Object.entries(artifacts).map(([t, kv]) =>
      `  ${t}:\n` + Object.entries(kv).map(([k, v]) => `    ${k}: ${v}\n`).join('')).join('');
  }
  y += `
steps:
  - title: User \${{ args.user }}
    action: add-user
    args:
      name: "\${{ args.user }}"
      shell: /bin/bash${state.on.has('sudo') ? '\n      sudo: true' : ''}
`;
  for (const f of feats) y += f.steps(params(f), { family: FLAVOR_FAMILY[state.flavor] });
  return y;
}

let yaml = '';
async function update() {
  state.user = $('b-user').value.trim();
  state.name = $('b-name').value.trim();
  try {
    localStorage.setItem('osb-builder', JSON.stringify({ ...state, on: [...state.on] }));
  } catch (e) { /* no storage */ }
  $('summary').textContent = sentence();
  yaml = generate();
  $('b-yaml').innerHTML = highlight(yaml, 'yaml');
  const resp = await render({ config: yaml, flavor: state.flavor, target: state.target }).catch((e) => ({ error: String(e) }));
  view.show(resp);
}
const later = debounce(update, 250);

$('b-target').addEventListener('change', (e) => { state.target = e.target.value; update(); });
$('b-flavor').addEventListener('change', (e) => { state.flavor = e.target.value; renderFeatures(); update(); });
$('b-user').addEventListener('input', later);
$('b-name').addEventListener('input', later);
$('b-copy').addEventListener('click', (e) => navigator.clipboard?.writeText(yaml).then(() => flash(e.target, 'Copied')));
$('b-download').addEventListener('click', () => saveFile('osb.yaml', yaml));
$('b-open').addEventListener('click', async () => {
  location.href = 'playground.html#c=' + await encodeShare(yaml) + '&f=' + state.flavor + '&t=' + state.target;
});

renderFeatures();
update();
