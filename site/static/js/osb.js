// Shared helpers: loading the osb WebAssembly build, rendering, output views.

let ready;

/** Loads osb.wasm once; resolves with the global `osb` API. */
export function loadOsb() {
  if (!ready) {
    ready = new Promise((resolve, reject) => {
      const script = document.createElement('script');
      script.src = new URL('wasm_exec.js', import.meta.url).href;
      script.onerror = () => reject(new Error('could not load wasm_exec.js'));
      script.onload = async () => {
        try {
          const go = new Go();
          window.osbReady = () => resolve(window.osb);
          const url = new URL('../osb.wasm', import.meta.url).href;
          let result;
          try {
            result = await WebAssembly.instantiateStreaming(fetch(url), go.importObject);
          } catch (e) {
            // Servers that do not send application/wasm
            const buf = await (await fetch(url)).arrayBuffer();
            result = await WebAssembly.instantiate(buf, go.importObject);
          }
          go.run(result.instance);
        } catch (e) {
          reject(e);
        }
      };
      document.head.append(script);
    });
  }
  return ready;
}

/** Renders a configuration: {config, flavor, target, args, placeholders}. */
export async function render(req) {
  const osb = await loadOsb();
  return JSON.parse(osb.render(JSON.stringify(req)));
}

let catalogCache;
/** Returns the catalog: actions (built-in and extra), flavors, targets, schema. */
export async function catalog() {
  if (!catalogCache) {
    const osb = await loadOsb();
    catalogCache = JSON.parse(osb.catalog());
  }
  return catalogCache;
}

export const TARGET_LABELS = {
  shell: 'Shell script',
  dockerfile: 'Dockerfile',
  oci: 'Container image',
  wsl: 'WSL distribution',
  'cloud-init': 'cloud-init',
  qcow2: 'VM image (qcow2)',
  ova: 'OVA appliance',
  apply: 'Apply to this machine',
};

export const FLAVOR_LABELS = { ubuntu: 'Ubuntu 24.04', debian: 'Debian 13', archlinux: 'Arch Linux' };

export function debounce(fn, ms) {
  let t;
  return (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), ms); };
}

export function el(tag, attrs = {}, ...children) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v == null || v === false) continue;
    if (k === 'class') e.className = v;
    else if (k.startsWith('on')) e.addEventListener(k.slice(2), v);
    else if (k === 'text') e.textContent = v;
    else e.setAttribute(k, v === true ? '' : v);
  }
  for (const c of children.flat()) if (c != null) e.append(c);
  return e;
}

/** Syntax highlighting (osb-lang.js). */
export function highlight(text, kind) {
  return window.osbLang.highlight(text, kind);
}

function kindOf(path) {
  return window.osbLang.languageOf(path.replace(/ \(.*\)$/, ''));
}

let langPromise;
/** The completion and documentation engine for the editors (needs the WASM catalog). */
export function osbLang() {
  if (!langPromise) langPromise = catalog().then((c) => new window.osbLang.Lang(c));
  return langPromise;
}

/**
 * An output view: tabs for the generated files, the content, a status line,
 * the osb command and notes. `root` gets the panel content.
 */
export class OutputView {
  constructor(root, { title = 'Output', showPlan = false } = {}) {
    this.root = root;
    this.showPlan = showPlan;
    this.tabs = el('div', { class: 'tabs', role: 'tablist' });
    this.copyBtn = el('button', { class: 'btn btn-small', type: 'button', onclick: () => this.copy() }, 'Copy');
    this.dlBtn = el('button', { class: 'btn btn-small', type: 'button', onclick: () => this.download() }, 'Download');
    this.pre = el('pre', { class: 'out', tabindex: '0' });
    this.pre.innerHTML = '<span class="c">Loading osb (WebAssembly)…</span>';
    this.status = el('div', { class: 'status', role: 'status' });
    this.note = el('div', { class: 'note', hidden: true });
    this.cmd = el('div', { class: 'cmd', hidden: true });
    this.plan = el('ol', { class: 'plan-list', hidden: true });
    root.append(
      el('div', { class: 'panel-head' }, el('span', { class: 'panel-title' }, title), this.tabs, this.copyBtn, this.dlBtn),
      el('div', { class: 'code-wrap' }, this.pre),
      this.status, this.note, this.cmd, this.plan,
    );
    this.files = [];
    this.current = 0;
    this.onErrorLine = null;
  }

  show(resp) {
    this.resp = resp;
    if (resp.error) {
      this.setStatus('err', resp.error);
      this.pre.classList.add('stale');
      return;
    }
    const prev = this.files[this.current]?.path;
    this.files = resp.files || [];
    const idx = this.files.findIndex((f) => f.path === prev);
    this.current = idx >= 0 ? idx : 0;
    this.renderTabs();
    this.renderFile();
    const warnings = resp.warnings || [];
    if (warnings.length) this.setStatus('warn', warnings.join('\n'));
    else this.setStatus('ok', `${(resp.steps || []).length} steps for ${resp.flavor}`);
    this.note.hidden = !resp.note;
    this.note.textContent = resp.note || '';
    this.cmd.hidden = !resp.command;
    this.cmd.replaceChildren(el('span', {}, 'On your machine:'), el('code', { text: resp.command || '' }));
    if (this.showPlan) {
      this.plan.hidden = !(resp.steps || []).length;
      this.plan.replaceChildren(...(resp.steps || []).map((s) =>
        el('li', {}, s.title + ' ', s.action && !s.title.endsWith(s.action) ? el('code', { text: s.action }) : null)));
    }
  }

  setStatus(kind, text) {
    this.status.className = 'status ' + kind;
    const code = el('code', { text });
    this.status.replaceChildren(code);
    const m = /osb\.yaml:(\d+)/.exec(text);
    if (kind === 'err' && m && this.onErrorLine) {
      this.status.append(el('button', { class: 'btn btn-small', type: 'button', onclick: () => this.onErrorLine(+m[1]) }, `Go to line ${m[1]}`));
    }
  }

  renderTabs() {
    this.tabs.replaceChildren(...this.files.map((f, i) => el('button', {
      class: 'tab', type: 'button', role: 'tab', 'aria-selected': String(i === this.current),
      onclick: () => { this.current = i; this.renderTabs(); this.renderFile(); },
    }, f.path)));
  }

  renderFile() {
    const f = this.files[this.current];
    this.pre.classList.remove('stale');
    this.pre.innerHTML = f ? highlight(f.content, kindOf(f.path)) : '';
    // Start at the first step instead of the helper library of shell scripts.
    const lines = f ? f.content.split('\n') : [];
    const first = lines.findIndex((l) => /^# Step 1\//.test(l));
    const lh = parseFloat(getComputedStyle(this.pre).lineHeight) || 20;
    this.pre.scrollTop = first > 2 ? (first - 2) * lh : 0;
  }

  copy() {
    const f = this.files[this.current];
    if (f) navigator.clipboard?.writeText(f.content).then(() => flash(this.copyBtn, 'Copied'));
  }

  download() {
    const f = this.files[this.current];
    if (f) saveFile(f.path.replace(/[^\w.-]+/g, '_').replace(/^_+/, ''), f.content);
  }
}

export function flash(btn, text) {
  const old = btn.textContent;
  btn.textContent = text;
  setTimeout(() => { btn.textContent = old; }, 1200);
}

export function saveFile(name, content) {
  const a = el('a', { href: URL.createObjectURL(new Blob([content], { type: 'text/plain' })), download: name });
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(a.href), 1000);
}

/** Encodes text for a URL fragment (deflate + base64url). */
export async function encodeShare(text) {
  const stream = new Blob([text]).stream().pipeThrough(new CompressionStream('deflate-raw'));
  const bytes = new Uint8Array(await new Response(stream).arrayBuffer());
  let bin = '';
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

export async function decodeShare(s) {
  const bin = atob(s.replace(/-/g, '+').replace(/_/g, '/'));
  const bytes = Uint8Array.from(bin, (c) => c.charCodeAt(0));
  const stream = new Blob([bytes]).stream().pipeThrough(new DecompressionStream('deflate-raw'));
  return new Response(stream).text();
}

/** Selects a line in a textarea (1-based). */
export function gotoLine(textarea, line) {
  const lines = textarea.value.split('\n');
  let start = 0;
  for (let i = 0; i < line - 1 && i < lines.length; i++) start += lines[i].length + 1;
  const end = start + (lines[line - 1] || '').length;
  textarea.focus();
  textarea.setSelectionRange(start, end);
  const lh = parseFloat(getComputedStyle(textarea).lineHeight) || 20;
  textarea.scrollTop = Math.max(0, (line - 5) * lh);
}

/** Makes Tab insert two spaces in a textarea. */
export function tabIndent(textarea) {
  textarea.addEventListener('keydown', (e) => {
    if (e.key !== 'Tab' || e.shiftKey || e.ctrlKey || e.metaKey || e.altKey) return;
    e.preventDefault();
    const { selectionStart: s, selectionEnd: t, value } = textarea;
    textarea.value = value.slice(0, s) + '  ' + value.slice(t);
    textarea.selectionStart = textarea.selectionEnd = s + 2;
    textarea.dispatchEvent(new Event('input'));
  });
}
